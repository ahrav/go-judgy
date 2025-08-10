package circuit_breaker

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/transport"
	"github.com/redis/go-redis/v9"
)

// Constants for middleware configuration.
const (
	// BuilderGrowthBuffer is the extra capacity for string builder growth.
	builderGrowthBuffer = 20
	// DefaultProbeTimeoutSeconds is the default timeout for probe guards in seconds.
	defaultProbeTimeoutSeconds = 60
)

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

// GetStats returns comprehensive statistics for all circuit breakers.
// It aggregates metrics across all breakers to provide system-wide visibility.
func (c *circuitBreakerMiddleware) GetStats() (*CircuitBreakerStats, error) {
	stats := &CircuitBreakerStats{
		StateCount: make(map[string]int),
	}

	// Initialize state counts.
	stats.StateCount[StateClosed.String()] = 0
	stats.StateCount[StateOpen.String()] = 0
	stats.StateCount[StateHalfOpen.String()] = 0

	// Aggregate metrics across all shards.
	for i := range c.breakers.shards {
		shard := &c.breakers.shards[i]
		shard.RLock()

		for _, breaker := range shard.breakers {
			stats.TotalBreakers++

			// Count by state.
			state := CircuitState(breaker.state.Load())
			stats.StateCount[state.String()]++

			// Aggregate metrics.
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
