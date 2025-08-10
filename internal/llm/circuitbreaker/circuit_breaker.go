// Package circuitbreaker provides mechanisms to build robust and fault-tolerant LLM client applications.
// It includes implementations for circuit breaking to prevent cascading failures.
package circuitbreaker

import (
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sync/atomic"
	"time"

	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
)

// Static errors for circuit breaker operations.
var (
	// ErrUnknownCircuitState is returned when the circuit is in an unknown state.
	ErrUnknownCircuitState = errors.New("unknown circuit state")
	// ErrCircuitBreakerNotFound is returned when a circuit breaker is not found.
	ErrCircuitBreakerNotFound = errors.New("circuit breaker not found")
)

// Constants for circuit breaker jitter calculation.
const (
	// JitterDivisor is used to calculate jitter as a fraction of open timeout.
	jitterDivisor = 10
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

// circuitResult represents the outcome of a circuit breaker request evaluation.
// It encapsulates the request decision, required cleanup operations, and probe state
// to provide clear semantics for circuit breaker operations and eliminate parameter confusion
// in method signatures while maintaining thread-safe probe management.
type circuitResult struct {
	// Allowed indicates whether the circuit breaker permits the request to proceed.
	Allowed bool
	// Cleanup is the function that must be called when the request completes to properly
	// manage probe counters and maintain accurate half-open state tracking.
	Cleanup func()
	// IsHalfOpenProbe indicates if this request is a half-open probe, used to eliminate
	// race conditions and coordinate distributed probe testing with Redis.
	IsHalfOpenProbe bool
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
func newCircuitBreaker(cfg Config) *circuitBreaker {
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
func (cb *circuitBreaker) handleHalfOpenProbe() (*circuitResult, error) {
	for {
		current := cb.halfOpenProbes.Load()
		if int(current) >= cb.maxHalfOpenProbes {
			cb.metrics.requestsRejected.Add(1)
			return &circuitResult{
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
			// Create cleanup function to release the in-flight slot.
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
			return &circuitResult{
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
func (cb *circuitBreaker) allow() (*circuitResult, error) {
	state := CircuitState(cb.state.Load())

	switch state {
	case StateClosed:
		cb.metrics.requestsAllowed.Add(1)
		return &circuitResult{
			Allowed:         true,
			Cleanup:         func() {},
			IsHalfOpenProbe: false,
		}, nil

	case StateOpen, StateHalfOpen:
		// For StateOpen, check timeout and transition if ready.
		if state == StateOpen {
			lastFailureNano := cb.lastFailureTime.Load()
			lastFailure := time.Unix(0, lastFailureNano)
			timeout := cb.openTimeout + cb.getJitter()
			if time.Since(lastFailure) <= timeout {
				cb.metrics.requestsRejected.Add(1)
				return &circuitResult{
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

		// Common half-open probe logic for both states.
		return cb.handleHalfOpenProbe()

	default:
		return &circuitResult{
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
					cb.halfOpenProbes.Store(0) // Reset probe counter on transition to closed
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
				cb.halfOpenProbes.Store(0) // Reset probe counter on transition to open
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
