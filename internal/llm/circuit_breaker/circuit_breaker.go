// Package circuit_breaker Package resilience provides mechanisms to build robust and fault-tolerant LLM client applications.
// It includes implementations for retry, circuit breaking, rate limiting, and caching.
package circuit_breaker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// Static errors for circuit breaker operations.
var (
	// ErrUnknownCircuitState is returned when the circuit is in an unknown state.
	ErrUnknownCircuitState = errors.New("unknown circuit state")
	// ErrCircuitBreakerNotFound is returned when a circuit breaker is not found.
	ErrCircuitBreakerNotFound = errors.New("circuit breaker not found")
)

// Constants for circuit breaker configuration.
const (
	// MinRequestsForAdjustment is the minimum number of requests before adjusting thresholds.
	minRequestsForAdjustment = 10
	// HighErrorRateThreshold is the error rate threshold for aggressive reduction.
	highErrorRateThreshold = 0.5
	// MediumErrorRateThreshold is the error rate threshold for moderate reduction.
	mediumErrorRateThreshold = 0.3
	// MediumThresholdMultiplier is the multiplier for medium error rate threshold adjustment.
	mediumThresholdMultiplier = 0.75
	// HighThresholdDivisor is used to calculate the threshold for high error rates.
	highThresholdDivisor = 2
	// JitterDivisor is used to calculate jitter as a fraction of open timeout.
	jitterDivisor = 10
	// HashMultiplier is used in the hash function for shard selection.
	hashMultiplier = 31
	// BuilderGrowthBuffer is the extra capacity for string builder growth.
	builderGrowthBuffer = 20
	// DefaultProbeTimeoutSeconds is the default timeout for probe guards in seconds.
	defaultProbeTimeoutSeconds = 60
)

// circuitBreakerContextKey is an unexported type for context keys to prevent external forgery.
type circuitBreakerContextKey struct{}

// halfOpenProbeKey is the context key for circuit breaker half-open probe indication.
var halfOpenProbeKey = circuitBreakerContextKey{}

// CircuitState represents the current state of a circuit breaker.
// The circuit breaker operates as a state machine with three possible states
// that control request flow based on system health and failure patterns.
type CircuitState int32

const (
	// StateClosed allows requests through.
	StateClosed CircuitState = iota
	// StateOpen blocks all requests.
	StateOpen
	// StateHalfOpen allows limited requests for testing.
	StateHalfOpen
)

// String returns the string representation of the circuit state.
func (s CircuitState) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// CircuitResult represents the outcome of a circuit breaker request evaluation.
// It encapsulates the request decision, required cleanup operations, and probe state
// to provide clear semantics for circuit breaker operations and eliminate parameter confusion
// in method signatures while maintaining thread-safe probe management.
type CircuitResult struct {
	// Allowed indicates whether the circuit breaker permits the request to proceed.
	Allowed bool
	// Cleanup is the function that must be called when the request completes to properly
	// manage probe counters and maintain accurate half-open state tracking.
	Cleanup func()
	// IsHalfOpenProbe indicates if this request is a half-open probe, used to eliminate
	// race conditions and coordinate distributed probe testing with Redis.
	IsHalfOpenProbe bool
}

// adaptiveThresholds manages dynamic threshold adjustment based on error rates.
// It implements a sliding window algorithm that adjusts failure thresholds based on
// historical error patterns, enabling more sensitive detection during high error periods
// and more permissive behavior during stable periods.
type adaptiveThresholds struct {
	mu                   sync.Mutex // Protects window reset operations
	baseFailureThreshold int
	currentThreshold     atomic.Int32
	totalRequests        atomic.Int64
	totalFailures        atomic.Int64
	windowStart          atomic.Int64
	windowDuration       time.Duration
}

// newAdaptiveThresholds creates a new adaptive threshold manager.
func newAdaptiveThresholds(baseThreshold int) *adaptiveThresholds {
	at := &adaptiveThresholds{
		baseFailureThreshold: baseThreshold,
		windowDuration:       1 * time.Minute,
	}
	// Ensure baseThreshold fits in int32
	var threshold int32
	if baseThreshold < 0 || baseThreshold > math.MaxInt32 {
		threshold = math.MaxInt32
	} else {
		threshold = int32(baseThreshold)
	}
	at.currentThreshold.Store(threshold)
	at.windowStart.Store(time.Now().UnixNano())
	return at
}

// recordRequest records a request and its outcome.
func (at *adaptiveThresholds) recordRequest(success bool) {
	now := time.Now().UnixNano()

	// Check if window needs reset (lock-free fast path)
	if now-at.windowStart.Load() > int64(at.windowDuration) {
		at.mu.Lock()
		// Re-check under lock to avoid double resets
		if now-at.windowStart.Load() > int64(at.windowDuration) {
			// Zero counters first, then publish the new windowStart
			// This prevents other goroutines from seeing a half-reset state
			at.totalRequests.Store(0)
			at.totalFailures.Store(0)
			at.windowStart.Store(now)
		}
		at.mu.Unlock()
	}

	at.totalRequests.Add(1)
	if !success {
		at.totalFailures.Add(1)
	}

	at.adjust()
}

// adjust updates the threshold based on current error rate.
func (at *adaptiveThresholds) adjust() {
	total := at.totalRequests.Load()
	if total < minRequestsForAdjustment {
		return
	}

	failures := at.totalFailures.Load()
	errorRate := float64(failures) / float64(total)

	var newThreshold int32
	switch {
	case errorRate > highErrorRateThreshold:
		// Ensure division result fits in int32
		halfThreshold := at.baseFailureThreshold / highThresholdDivisor
		if halfThreshold < 0 || halfThreshold > math.MaxInt32 {
			newThreshold = math.MaxInt32
		} else {
			newThreshold = int32(halfThreshold)
		}
	case errorRate > mediumErrorRateThreshold:
		// Ensure multiplication result fits in int32
		adjustedThreshold := float64(at.baseFailureThreshold) * mediumThresholdMultiplier
		if adjustedThreshold > math.MaxInt32 {
			newThreshold = math.MaxInt32
		} else {
			newThreshold = int32(adjustedThreshold)
		}
	default:
		// Ensure baseFailureThreshold fits in int32
		if at.baseFailureThreshold < 0 || at.baseFailureThreshold > math.MaxInt32 {
			newThreshold = math.MaxInt32
		} else {
			newThreshold = int32(at.baseFailureThreshold)
		}
	}

	if newThreshold < 1 {
		newThreshold = 1
	}

	at.currentThreshold.Store(newThreshold)
}

// getThreshold returns the current failure threshold.
func (at *adaptiveThresholds) getThreshold() int {
	return int(at.currentThreshold.Load())
}

// circuitBreakerMetrics tracks circuit breaker performance metrics.
// These metrics provide observability into circuit breaker behavior and
// can be used for monitoring, alerting, and performance analysis.
type circuitBreakerMetrics struct {
	stateTransitions    atomic.Int64 // Total state transitions
	requestsAllowed     atomic.Int64 // Total requests allowed
	requestsRejected    atomic.Int64 // Total requests rejected
	probeAttempts       atomic.Int64 // Total probe attempts
	probeSuccesses      atomic.Int64 // Total successful probes
	probeGuardConflicts atomic.Int64 // Redis probe guard conflicts
	timeInClosed        atomic.Int64 // Nanoseconds in closed state
	timeInOpen          atomic.Int64 // Nanoseconds in open state
	timeInHalfOpen      atomic.Int64 // Nanoseconds in half-open state
	lastStateChange     atomic.Int64 // Timestamp of last state change
}

// updateStateTime updates the time spent in the current state.
func (m *circuitBreakerMetrics) updateStateTime(currentState CircuitState) {
	now := time.Now().UnixNano()
	lastChange := m.lastStateChange.Load()
	if lastChange == 0 {
		m.lastStateChange.Store(now)
		return
	}

	duration := now - lastChange
	switch currentState {
	case StateClosed:
		m.timeInClosed.Add(duration)
	case StateOpen:
		m.timeInOpen.Add(duration)
	case StateHalfOpen:
		m.timeInHalfOpen.Add(duration)
	}
	m.lastStateChange.Store(now)
}

// circuitBreaker implements per-provider circuit breaking with failure detection.
// It manages state transitions through atomic operations and provides thread-safe
// failure tracking with optional adaptive threshold adjustment for dynamic
// responsiveness to changing error patterns.
type circuitBreaker struct {
	state           atomic.Int32
	failures        atomic.Int32
	successes       atomic.Int32
	lastFailureTime atomic.Int64
	halfOpenProbes  atomic.Int32

	failureThreshold  int
	successThreshold  int
	openTimeout       time.Duration
	maxHalfOpenProbes int

	adaptive *adaptiveThresholds    // Optional adaptive thresholds
	metrics  *circuitBreakerMetrics // Performance metrics
}

// getJitter returns a random jitter duration up to 10% of the open timeout.
func (cb *circuitBreaker) getJitter() time.Duration {
	if cb.openTimeout == 0 {
		return 0
	}

	jit := cb.openTimeout / jitterDivisor
	if jit <= 0 {
		return 0
	}
	//nolint:gosec // Using weak random for jitter is acceptable
	return time.Duration(rand.Int63n(int64(jit)))
}

// newCircuitBreaker creates a new circuit breaker instance.
func newCircuitBreaker(cfg CircuitBreakerConfig) *circuitBreaker {
	cb := &circuitBreaker{
		failureThreshold:  cfg.FailureThreshold,
		successThreshold:  cfg.SuccessThreshold,
		openTimeout:       cfg.OpenTimeout,
		maxHalfOpenProbes: cfg.HalfOpenProbes,
		metrics:           &circuitBreakerMetrics{},
	}

	if cfg.AdaptiveThresholds {
		cb.adaptive = newAdaptiveThresholds(cfg.FailureThreshold)
	}

	cb.state.Store(int32(StateClosed))
	cb.metrics.lastStateChange.Store(time.Now().UnixNano())
	return cb
}

// handleHalfOpenProbe manages the half-open probe logic shared by StateOpen and StateHalfOpen.
// It handles probe slot allocation, cleanup function creation, and metrics tracking.
// Returns a CircuitResult containing the request decision and required cleanup operations.
func (cb *circuitBreaker) handleHalfOpenProbe() (*CircuitResult, error) {
	for {
		current := cb.halfOpenProbes.Load()
		if int(current) >= cb.maxHalfOpenProbes {
			cb.metrics.requestsRejected.Add(1)
			return &CircuitResult{
					Allowed:         false,
					Cleanup:         func() {},
					IsHalfOpenProbe: false,
				}, &llmerrors.ProviderError{
					Code:    "CIRCUIT_HALF_OPEN_LIMIT",
					Message: "half-open probe limit reached",
					Type:    llmerrors.ErrorTypeCircuitBreaker,
				}
		}
		if cb.halfOpenProbes.CompareAndSwap(current, current+1) {
			// Create cleanup function to release the in-flight slot
			cleanup := func() {
				// Release the in-flight slot; saturate at 0 if a concurrent transition reset it.
				for {
					cur := cb.halfOpenProbes.Load()
					if cur == 0 {
						return
					}
					if cb.halfOpenProbes.CompareAndSwap(cur, cur-1) {
						return
					}
				}
			}
			cb.metrics.probeAttempts.Add(1)
			cb.metrics.requestsAllowed.Add(1)
			return &CircuitResult{
				Allowed:         true,
				Cleanup:         cleanup,
				IsHalfOpenProbe: true,
			}, nil
		}
	}
}

// allow checks if a request should be allowed through based on circuit state.
// Returns a CircuitResult containing the request decision and required cleanup operations.
// The cleanup function must be called when the request completes to properly
// manage probe counters and maintain accurate half-open state tracking across
// concurrent operations. The probe status indicates if this request is a
// half-open probe to eliminate race conditions.
func (cb *circuitBreaker) allow() (*CircuitResult, error) {
	state := CircuitState(cb.state.Load())

	switch state {
	case StateClosed:
		cb.metrics.requestsAllowed.Add(1)
		return &CircuitResult{
			Allowed:         true,
			Cleanup:         func() {},
			IsHalfOpenProbe: false,
		}, nil

	case StateOpen, StateHalfOpen:
		// For StateOpen, check timeout and transition if ready
		if state == StateOpen {
			lastFailureNano := cb.lastFailureTime.Load()
			lastFailure := time.Unix(0, lastFailureNano)
			timeout := cb.openTimeout + cb.getJitter()
			if time.Since(lastFailure) <= timeout {
				cb.metrics.requestsRejected.Add(1)
				return &CircuitResult{
						Allowed:         false,
						Cleanup:         func() {},
						IsHalfOpenProbe: false,
					}, &llmerrors.ProviderError{
						Code:    "CIRCUIT_OPEN",
						Message: "circuit breaker is open",
						Type:    llmerrors.ErrorTypeCircuitBreaker,
					}
			}
			cb.transitionTo(StateHalfOpen)
		}

		// Common half-open probe logic for both states
		return cb.handleHalfOpenProbe()

	default:
		return &CircuitResult{
			Allowed:         false,
			Cleanup:         func() {},
			IsHalfOpenProbe: false,
		}, fmt.Errorf("%w: %v", ErrUnknownCircuitState, state)
	}
}

// recordSuccess records a successful request and manages state transitions.
// In half-open state, it tracks success count and transitions to closed when
// the success threshold is reached. Updates adaptive thresholds when enabled.
func (cb *circuitBreaker) recordSuccess() {
	if cb.adaptive != nil {
		cb.adaptive.recordRequest(true)
	}

	for {
		state := cb.state.Load()
		switch CircuitState(state) {
		case StateClosed:
			cb.failures.Store(0)
			return

		case StateHalfOpen:
			successes := cb.successes.Add(1)
			cb.metrics.probeSuccesses.Add(1)
			if int(successes) >= cb.successThreshold {
				if cb.state.CompareAndSwap(state, int32(StateClosed)) {
					cb.failures.Store(0)
					cb.successes.Store(0)
					cb.metrics.stateTransitions.Add(1)
					cb.metrics.updateStateTime(StateHalfOpen)
					slog.Info("circuit breaker state transition",
						"from", StateHalfOpen.String(),
						"to", StateClosed.String())
					return
				}
				cb.successes.Add(-1)
				continue
			}
			return

		case StateOpen:
			slog.Warn("success recorded in open state")
			return
		}
	}
}

// recordFailure records a failed request and manages state transitions.
// In the closed state, it tracks the failure count and transitions to open
// when the failure threshold is reached.
// In the half-open state, it immediately transitions back to open.
// It also updates adaptive thresholds if they are enabled.
func (cb *circuitBreaker) recordFailure() {
	cb.lastFailureTime.Store(time.Now().UnixNano())

	if cb.adaptive != nil {
		cb.adaptive.recordRequest(false)
	}

	for {
		state := cb.state.Load()
		switch CircuitState(state) {
		case StateClosed:
			failures := cb.failures.Add(1)
			threshold := cb.failureThreshold
			if cb.adaptive != nil {
				threshold = cb.adaptive.getThreshold()
			}
			if int(failures) >= threshold {
				if cb.state.CompareAndSwap(state, int32(StateOpen)) {
					cb.failures.Store(0)
					cb.successes.Store(0)
					cb.metrics.stateTransitions.Add(1)
					cb.metrics.updateStateTime(StateClosed)
					slog.Info("circuit breaker state transition",
						"from", StateClosed.String(),
						"to", StateOpen.String())
					return
				}
				continue
			}
			return

		case StateHalfOpen:
			if cb.state.CompareAndSwap(state, int32(StateOpen)) {
				cb.failures.Store(0)
				cb.successes.Store(0)
				cb.metrics.stateTransitions.Add(1)
				cb.metrics.updateStateTime(StateHalfOpen)
				slog.Info("circuit breaker state transition",
					"from", StateHalfOpen.String(),
					"to", StateOpen.String())
				return
			}
			continue

		case StateOpen:
			return
		}
	}
}

// transitionTo changes the circuit breaker state.
func (cb *circuitBreaker) transitionTo(newState CircuitState) {
	oldState := CircuitState(cb.state.Swap(int32(newState)))

	if oldState != newState {
		switch newState {
		case StateClosed:
			cb.failures.Store(0)
			cb.successes.Store(0)
			cb.halfOpenProbes.Store(0)
		case StateOpen:
			cb.failures.Store(0)
			cb.successes.Store(0)
			cb.halfOpenProbes.Store(0)
		case StateHalfOpen:
			cb.successes.Store(0)
			cb.halfOpenProbes.Store(0)
		}

		cb.metrics.stateTransitions.Add(1)
		cb.metrics.updateStateTime(oldState)

		slog.Info("circuit breaker state transition",
			"from", oldState.String(),
			"to", newState.String())
	}
}

// shardedBreakers implements sharded locking to reduce contention.
// It distributes circuit breakers across 16 shards using a hash function.
// This minimizes lock contention in high-concurrency scenarios while maintaining
// consistent access patterns and performance characteristics.
type shardedBreakers struct {
	shards [16]struct {
		sync.RWMutex
		breakers map[string]*circuitBreaker
	}
	total atomic.Int64 // Thread-safe total count of circuit breakers
}

// newShardedBreakers creates a new sharded breaker store.
func newShardedBreakers() *shardedBreakers {
	sb := &shardedBreakers{}
	for i := range sb.shards {
		sb.shards[i].breakers = make(map[string]*circuitBreaker)
	}
	return sb
}

// getShard returns the shard index for a given key.
func (sb *shardedBreakers) getShard(key string) int {
	var hash uint32
	for i := 0; i < len(key); i++ {
		hash = hash*hashMultiplier + uint32(key[i])
	}
	return int(hash % uint32(len(sb.shards)))
}

// get returns a circuit breaker for the key if it exists.
func (sb *shardedBreakers) get(key string) (*circuitBreaker, bool) {
	shard := &sb.shards[sb.getShard(key)]
	shard.RLock()
	breaker, exists := shard.breakers[key]
	shard.RUnlock()
	return breaker, exists
}

// getOrCreate returns an existing breaker or creates a new one atomically.
// It implements double-checked locking to minimize contention while ensuring thread safety.
// It returns the breaker and an error if the maximum breaker limit is reached.
func (sb *shardedBreakers) getOrCreate(
	key string,
	create func() *circuitBreaker,
	maxBreakers int,
) (*circuitBreaker, error) {
	if breaker, exists := sb.get(key); exists {
		return breaker, nil
	}

	shard := &sb.shards[sb.getShard(key)]
	shard.Lock()
	defer shard.Unlock()

	if breaker, exists := shard.breakers[key]; exists {
		return breaker, nil
	}

	if maxBreakers > 0 && int(sb.total.Load()) >= maxBreakers {
		return nil, &llmerrors.ProviderError{
			Code:    "CIRCUIT_BREAKER_LIMIT",
			Message: fmt.Sprintf("circuit breaker limit reached (%d), cannot create new breaker for key: %s", maxBreakers, key),
			Type:    llmerrors.ErrorTypeCircuitBreaker,
		}
	}

	breaker := create()
	shard.breakers[key] = breaker
	sb.total.Add(1)
	return breaker, nil
}

// circuitBreakerMiddleware manages circuit breakers for multiple providers.
// It provides a middleware layer that intercepts requests and applies circuit breaking
// logic for each provider, model, and region combination.
// It coordinates with Redis for distributed probe synchronization to prevent thundering herd effects.
type circuitBreakerMiddleware struct {
	breakers          *shardedBreakers
	config            CircuitBreakerConfig
	redisClient       *redis.Client
	probeGuardEnabled bool
	logger            *slog.Logger
}

// CircuitBreakerConfig controls the behavior of a circuit breaker.
// It defines failure thresholds, success requirements, and timing parameters
// for automatic failure detection and recovery.
type CircuitBreakerConfig struct {
	// FailureThreshold is the number of consecutive failures required to open the circuit.
	FailureThreshold int `json:"failure_threshold"`
	// SuccessThreshold is the number of consecutive successes required to close the circuit.
	SuccessThreshold int `json:"success_threshold"`
	// OpenTimeout is the duration the circuit remains open before transitioning to half-open.
	OpenTimeout time.Duration `json:"open_timeout"`
	// HalfOpenProbes is the number of test requests allowed in the half-open state.
	HalfOpenProbes int `json:"half_open_probes"`
	// ProbeTimeout is the timeout for the Redis guard on a half-open probe.
	// This prevents multiple instances from probing simultaneously.
	ProbeTimeout time.Duration `json:"probe_timeout"`
	// MaxBreakers is the maximum number of circuit breakers to create.
	// This prevents unbounded growth in the number of breakers.
	MaxBreakers int `json:"max_breakers"`
	// AdaptiveThresholds enables dynamic adjustment of failure thresholds based on error rates.
	AdaptiveThresholds bool `json:"adaptive_thresholds"`
}

// NewCircuitBreakerMiddlewareWithRedis creates circuit breaker middleware with a Redis probe guard.
// It implements per-provider circuit breaking with coordinated probe testing
// to prevent a thundering herd during recovery.
func NewCircuitBreakerMiddlewareWithRedis(
	cfg CircuitBreakerConfig,
	client *redis.Client,
) (transport.Middleware, error) {
	cbm := &circuitBreakerMiddleware{
		breakers:          newShardedBreakers(),
		config:            cfg,
		redisClient:       client,
		probeGuardEnabled: client != nil,
		logger:            slog.Default().With("component", "circuit_breaker"),
	}

	return cbm.middleware(), nil
}

// middleware returns the circuit breaker middleware function.
func (c *circuitBreakerMiddleware) middleware() transport.Middleware {
	return func(next transport.Handler) transport.Handler {
		return transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
			key := c.buildKey(req)
			breaker, err := c.getOrCreateBreaker(key)
			if err != nil {
				// Return the error directly - it's already a properly formatted ProviderError
				return nil, err
			}

			result, err := breaker.allow()
			if err != nil || !result.Allowed {
				return nil, err
			}
			defer result.Cleanup()

			if result.IsHalfOpenProbe && c.probeGuardEnabled {
				if !c.acquireProbeGuard(ctx, key) {
					if breaker.metrics != nil {
						breaker.metrics.probeGuardConflicts.Add(1)
					}
					return nil, &llmerrors.ProviderError{
						Code:    "PROBE_IN_PROGRESS",
						Message: "another instance is testing the provider",
						Type:    llmerrors.ErrorTypeCircuitBreaker,
					}
				}
				defer c.releaseProbeGuard(ctx, key)
			}

			requestCtx := ctx
			if result.IsHalfOpenProbe {
				requestCtx = context.WithValue(ctx, halfOpenProbeKey, true)
			}
			resp, err := next.Handle(requestCtx, req)
			if err != nil {
				breaker.recordFailure()
				return nil, err
			}

			breaker.recordSuccess()
			return resp, nil
		})
	}
}

// buildKey creates a unique key for a circuit breaker from the request.
// The key format is {provider}:{model}:{region}.
// It uses strings.Builder to avoid allocations in a hot path.
func (c *circuitBreakerMiddleware) buildKey(req *transport.Request) string {
	var builder strings.Builder
	builder.Grow(len(req.Provider) + len(req.Model) + builderGrowthBuffer)

	builder.WriteString(req.Provider)
	builder.WriteByte(':')
	builder.WriteString(req.Model)

	if req.Metadata != nil {
		if region, exists := req.Metadata["region"]; exists && region != "" {
			builder.WriteByte(':')
			builder.WriteString(region)
		}
	}

	return builder.String()
}

// getOrCreateBreaker returns a circuit breaker for the key.
// Returns nil and an error if the maximum breaker limit is reached.
func (c *circuitBreakerMiddleware) getOrCreateBreaker(key string) (*circuitBreaker, error) {
	maxBreakers := c.config.MaxBreakers
	if maxBreakers == 0 {
		maxBreakers = 1000
	}

	breaker, err := c.breakers.getOrCreate(key, func() *circuitBreaker {
		return newCircuitBreaker(c.config)
	}, maxBreakers)
	if err != nil {
		c.logger.Warn("circuit breaker limit reached",
			"key", key,
			"limit", maxBreakers,
			"error", err)
		return nil, err
	}

	return breaker, nil
}

// acquireProbeGuard attempts to acquire exclusive probe rights via Redis.
// Returns true if this instance should probe, false if another is probing.
func (c *circuitBreakerMiddleware) acquireProbeGuard(ctx context.Context, key string) bool {
	if c.redisClient == nil {
		return true // No coordination, allow probe
	}

	guardKey := fmt.Sprintf("cb:probe:%s", key)
	ttl := c.config.ProbeTimeout
	if ttl == 0 {
		ttl = defaultProbeTimeoutSeconds * time.Second
	}

	ok, err := c.redisClient.SetNX(ctx, guardKey, "1", ttl).Result()
	if err != nil {
		c.logger.Warn("failed to acquire probe guard", "error", err, "key", key)
		return true
	}

	return ok
}

// releaseProbeGuard releases the probe guard in Redis.
func (c *circuitBreakerMiddleware) releaseProbeGuard(ctx context.Context, key string) {
	if c.redisClient == nil {
		return
	}

	guardKey := fmt.Sprintf("cb:probe:%s", key)
	if err := c.redisClient.Del(ctx, guardKey).Err(); err != nil {
		c.logger.Warn("failed to release probe guard", "error", err, "key", key)
	}
}

// GetState returns the current state of a circuit breaker.
func (c *circuitBreakerMiddleware) GetState(key string) (CircuitState, error) {
	breaker, exists := c.breakers.get(key)
	if !exists {
		return StateClosed, ErrCircuitBreakerNotFound
	}

	return CircuitState(breaker.state.Load()), nil
}

// Reset resets a circuit breaker to closed state.
func (c *circuitBreakerMiddleware) Reset(key string) error {
	breaker, exists := c.breakers.get(key)
	if !exists {
		return ErrCircuitBreakerNotFound
	}

	breaker.transitionTo(StateClosed)
	return nil
}

// GetMetrics returns the metrics for a circuit breaker (for testing).
func (c *circuitBreakerMiddleware) GetMetrics(
	key string,
) (stateTransitions, requestsAllowed, requestsRejected int64, err error) {
	breaker, exists := c.breakers.get(key)
	if !exists {
		return 0, 0, 0, ErrCircuitBreakerNotFound
	}

	return breaker.metrics.stateTransitions.Load(),
		breaker.metrics.requestsAllowed.Load(),
		breaker.metrics.requestsRejected.Load(),
		nil
}

// CircuitBreakerStats provides comprehensive circuit breaker performance metrics.
type CircuitBreakerStats struct {
	// TotalBreakers is the total number of active circuit breakers.
	TotalBreakers int `json:"total_breakers"`
	// StateCount maps each circuit state to the number of breakers in that state.
	StateCount map[string]int `json:"state_count"`

	// TotalStateTransitions is the total number of state changes across all breakers.
	TotalStateTransitions int64 `json:"total_state_transitions"`
	// TotalRequestsAllowed is the total number of requests permitted by all breakers.
	TotalRequestsAllowed int64 `json:"total_requests_allowed"`
	// TotalRequestsRejected is the total number of requests blocked by all breakers.
	TotalRequestsRejected int64 `json:"total_requests_rejected"`
	// TotalProbeAttempts is the total number of half-open probe requests initiated.
	TotalProbeAttempts int64 `json:"total_probe_attempts"`
	// TotalProbeSuccesses is the total number of successful half-open probe requests.
	TotalProbeSuccesses int64 `json:"total_probe_successes"`
	// TotalProbeGuardConflicts is the total number of Redis probe guard conflicts.
	TotalProbeGuardConflicts int64 `json:"total_probe_guard_conflicts"`
}

// GetStats returns comprehensive statistics for all circuit breakers.
// It aggregates metrics across all breakers to provide system-wide visibility.
func (c *circuitBreakerMiddleware) GetStats() (*CircuitBreakerStats, error) {
	stats := &CircuitBreakerStats{
		StateCount: make(map[string]int),
	}

	// Initialize state counts
	stats.StateCount[StateClosed.String()] = 0
	stats.StateCount[StateOpen.String()] = 0
	stats.StateCount[StateHalfOpen.String()] = 0

	// Aggregate metrics across all shards
	for i := range c.breakers.shards {
		shard := &c.breakers.shards[i]
		shard.RLock()

		for _, breaker := range shard.breakers {
			stats.TotalBreakers++

			// Count by state
			state := CircuitState(breaker.state.Load())
			stats.StateCount[state.String()]++

			// Aggregate metrics
			if breaker.metrics != nil {
				stats.TotalStateTransitions += breaker.metrics.stateTransitions.Load()
				stats.TotalRequestsAllowed += breaker.metrics.requestsAllowed.Load()
				stats.TotalRequestsRejected += breaker.metrics.requestsRejected.Load()
				stats.TotalProbeAttempts += breaker.metrics.probeAttempts.Load()
				stats.TotalProbeSuccesses += breaker.metrics.probeSuccesses.Load()
				stats.TotalProbeGuardConflicts += breaker.metrics.probeGuardConflicts.Load()
			}
		}

		shard.RUnlock()
	}

	return stats, nil
}
