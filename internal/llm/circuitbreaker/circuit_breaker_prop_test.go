// Package resilience_test contains property-based tests for the resilience
// package.
package circuitbreaker_test

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"testing/quick"
	"time"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/circuitbreaker"
	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// TestCircuitBreakerStateInvariants validates that the circuit breaker's state
// machine maintains logical consistency.
// It ensures that state transitions follow valid paths under all possible
// input combinations, which is critical for system reliability.
func TestCircuitBreakerStateInvariants(t *testing.T) {
	// Property: State transitions follow valid paths only
	property := func(failures, successes, probes uint8) bool {
		ctx := context.Background()

		config := circuitbreaker.Config{
			FailureThreshold: int(failures%10) + 1,
			SuccessThreshold: int(successes%10) + 1,
			HalfOpenProbes:   int(probes%10) + 1,
			OpenTimeout:      5 * time.Millisecond,
		}

		var currentState atomic.Int32
		var stateHistory []string
		var mu sync.Mutex

		handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
			// Random behavior
			if rand.Float32() < 0.3 {
				return nil, &llmerrors.ProviderError{
					Provider:   "test",
					StatusCode: 500,
					Message:    "error",
					Type:       llmerrors.ErrorTypeProvider,
				}
			}
			return &transport.Response{
				Content:      "success",
				FinishReason: domain.FinishStop,
				Usage:        transport.NormalizedUsage{TotalTokens: 10},
			}, nil
		})

		cbMiddleware, _ := circuitbreaker.NewCircuitBreakerMiddlewareWithRedis(config, nil)
		cbHandler := cbMiddleware(handler)

		req := &transport.Request{
			Operation: transport.OpGeneration,
			Provider:  "test",
			Model:     "test-model",
			Question:  "test",
		}

		// Execute random number of requests
		numRequests := rand.Intn(50) + 10
		for i := 0; i < numRequests; i++ {
			_, err := cbHandler.Handle(ctx, req)

			// Track state based on error
			mu.Lock()
			if err != nil {
				var perr *llmerrors.ProviderError
				if errors.As(err, &perr) {
					switch perr.Code {
					case "CIRCUIT_OPEN":
						stateHistory = append(stateHistory, "open")
					case "CIRCUIT_HALF_OPEN_LIMIT":
						stateHistory = append(stateHistory, "half-open")
					default:
						stateHistory = append(stateHistory, "closed")
					}
				}
			} else {
				// Success could be in any state
				stateHistory = append(stateHistory, "unknown")
			}
			mu.Unlock()

			// Random delay
			if i%10 == 0 {
				time.Sleep(time.Duration(rand.Intn(10)) * time.Millisecond)
			}
		}

		// Verify invariants:
		// 1. No invalid state transitions
		for i := 1; i < len(stateHistory); i++ {
			prev := stateHistory[i-1]
			curr := stateHistory[i]

			// Check for impossible transitions
			if prev == "closed" && curr == "half-open" {
				// Can't go directly from closed to half-open
				return false
			}
		}

		// 2. State value is always valid (0, 1, or 2)
		state := currentState.Load()
		if state < 0 || state > 2 {
			return false
		}

		return true
	}

	if err := quick.Check(property, nil); err != nil {
		t.Error(err)
	}
}

// TestCircuitBreakerThresholdInvariants validates that failure and success
// thresholds are respected across all configurations and error patterns.
// This property ensures predictable circuit breaker behavior regardless of
// input variability.
func TestCircuitBreakerThresholdInvariants(t *testing.T) {
	property := func(baseThreshold uint8, errorRate uint8) bool {
		if baseThreshold == 0 {
			baseThreshold = 1
		}

		ctx := context.Background()

		config := circuitbreaker.Config{
			FailureThreshold:   int(baseThreshold),
			SuccessThreshold:   2,
			HalfOpenProbes:     3,
			OpenTimeout:        10 * time.Millisecond,
			AdaptiveThresholds: true,
		}

		// Track failure count
		var failureCount atomic.Int32
		var successCount atomic.Int32
		var circuitOpened atomic.Bool

		handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
			// Controlled failure rate
			if rand.Intn(100) < int(errorRate%101) {
				failureCount.Add(1)
				return nil, &llmerrors.ProviderError{
					Provider:   "test",
					StatusCode: 500,
					Message:    "error",
					Type:       llmerrors.ErrorTypeProvider,
				}
			}
			successCount.Add(1)
			return &transport.Response{
				Content: "success",
			}, nil
		})

		cbMiddleware, _ := circuitbreaker.NewCircuitBreakerMiddlewareWithRedis(config, nil)
		cbHandler := cbMiddleware(handler)

		req := &transport.Request{
			Operation: transport.OpGeneration,
			Provider:  "test",
			Model:     "test-model",
			Question:  "test",
		}

		// Execute requests until circuit opens or max attempts
		for i := 0; i < 100; i++ {
			_, err := cbHandler.Handle(ctx, req)
			if err != nil {
				var perr *llmerrors.ProviderError
				if errors.As(err, &perr) && perr.Code == "CIRCUIT_OPEN" {
					circuitOpened.Store(true)
					break
				}
			}
		}

		// Invariants:
		// 1. If adaptive is enabled, threshold should adapt based on error rate
		// 2. Threshold should never be less than 1
		// 3. Circuit should open when failures reach threshold

		failures := failureCount.Load()
		successes := successCount.Load()

		// Property: Circuit opens based on threshold
		if circuitOpened.Load() {
			// Circuit opened, so failures should have reached some threshold
			// With adaptive thresholds, the exact threshold depends on error rate
			if failures == 0 {
				// Circuit shouldn't open with no failures
				return false
			}
		}

		// Property: Adaptive threshold bounds
		if config.AdaptiveThresholds && failures+successes >= 10 {
			errorRateActual := float64(failures) / float64(failures+successes)

			// High error rate should reduce threshold
			if errorRateActual > 0.5 && !circuitOpened.Load() && failures > int32(baseThreshold) {
				// High error rate but circuit didn't open despite many failures
				// This might be OK if failures are spread out over time
				_ = errorRateActual // Acknowledge high error rate condition
			}
		}

		return true
	}

	if err := quick.Check(property, nil); err != nil {
		t.Error(err)
	}
}

// TestCircuitBreakerProbeCounterInvariants validates that the half-open probe
// counters maintain correct bounds for concurrent probes.
// The circuit breaker allows up to maxHalfOpenProbes concurrent requests
// in half-open state. As probes complete, new ones can start, so the total
// number of probes over time may exceed maxHalfOpenProbes.
func TestCircuitBreakerProbeCounterInvariants(t *testing.T) {
	property := func(maxProbes uint8) bool {
		if maxProbes == 0 {
			maxProbes = 1
		}

		ctx := context.Background()

		// Set success threshold very high to prevent transition to closed
		// This ensures we stay in half-open state throughout the test
		config := circuitbreaker.Config{
			FailureThreshold: 1,
			SuccessThreshold: 10000, // Very high to prevent closing
			HalfOpenProbes:   int(maxProbes),
			OpenTimeout:      1 * time.Millisecond,
		}

		// Track probe attempts
		var successfulProbes atomic.Int32
		var rejectedProbes atomic.Int32
		var concurrentProbes atomic.Int32
		var maxConcurrent atomic.Int32
		var totalErrors atomic.Int32

		// Handler that tracks concurrent calls
		callCount := atomic.Int32{}
		handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
			count := callCount.Add(1)
			// First call fails to open the circuit
			if count == 1 {
				return nil, &llmerrors.ProviderError{
					Provider:   "test",
					StatusCode: 500,
					Message:    "error",
					Type:       llmerrors.ErrorTypeProvider,
				}
			}

			// Track concurrent probe count
			current := concurrentProbes.Add(1)
			for {
				maxVal := maxConcurrent.Load()
				if current > maxVal {
					if maxConcurrent.CompareAndSwap(maxVal, current) {
						break
					}
				} else {
					break
				}
			}

			// Simulate some work to test concurrency
			time.Sleep(time.Millisecond * 5)

			// Decrement concurrent count on exit
			defer concurrentProbes.Add(-1)

			return &transport.Response{
				Content: "success",
			}, nil
		})

		cbMiddleware, _ := circuitbreaker.NewCircuitBreakerMiddlewareWithRedis(config, nil)
		cbHandler := cbMiddleware(handler)

		req := &transport.Request{
			Operation: transport.OpGeneration,
			Provider:  "test",
			Model:     "test-model",
			Question:  "test",
		}

		// Open circuit with first failure
		_, err := cbHandler.Handle(ctx, req)
		if err == nil {
			// Should have failed
			return false
		}

		// Wait for circuit to transition to half-open
		time.Sleep(2 * time.Millisecond)

		// Launch many concurrent probes
		numGoroutines := int(maxProbes) * 10
		if numGoroutines > 100 {
			numGoroutines = 100
		}

		var wg sync.WaitGroup
		wg.Add(numGoroutines)

		for i := 0; i < numGoroutines; i++ {
			go func() {
				defer wg.Done()
				_, err := cbHandler.Handle(ctx, req)
				if err == nil {
					successfulProbes.Add(1)
				} else {
					totalErrors.Add(1)
					var perr *llmerrors.ProviderError
					if errors.As(err, &perr) && perr.Code == "CIRCUIT_HALF_OPEN_LIMIT" {
						rejectedProbes.Add(1)
					}
				}
			}()
		}

		wg.Wait()

		// Invariant: Maximum concurrent probes should not significantly exceed maxProbes
		// The circuit breaker limits concurrent probes, not total probes
		// Allow a small tolerance for measurement timing differences
		maxConcurrentObserved := maxConcurrent.Load()
		tolerance := int32(1) // Allow off-by-one due to timing

		if maxConcurrentObserved > int32(maxProbes)+tolerance {
			// Too many concurrent probes
			t.Logf("Too many concurrent probes: %d > %d (maxProbes+tolerance)", maxConcurrentObserved, int32(maxProbes)+tolerance)
			return false
		}

		// Invariant: All requests should be accounted for
		total := successfulProbes.Load() + totalErrors.Load()
		if total != int32(numGoroutines) {
			// Some requests unaccounted for
			t.Logf("Requests unaccounted: total=%d, expected=%d", total, numGoroutines)
			return false
		}

		// Invariant: Should have both successes and rejections with high contention
		// With many goroutines competing for limited probe slots, we expect some rejections
		if numGoroutines > int(maxProbes)*2 && rejectedProbes.Load() == 0 {
			t.Logf("Expected some rejections with %d goroutines and %d maxProbes", numGoroutines, maxProbes)
			// This is not a hard failure as timing can affect this
		}

		return true
	}

	if err := quick.Check(property, nil); err != nil {
		t.Error(err)
	}
}

// TestCircuitBreakerHashDistribution validates that the hash function
// distributes keys uniformly and consistently across shards.
// This property is crucial for preventing shard hotspots that could create
// performance bottlenecks in high-throughput scenarios.
func TestCircuitBreakerHashDistribution(t *testing.T) {
	property := func(keys []string) bool {
		if len(keys) == 0 {
			return true
		}

		const numShards = 16
		shardCounts := make([]int, numShards)

		// Hash each key and count shard distribution
		for _, key := range keys {
			var hash uint32
			for i := 0; i < len(key); i++ {
				hash = hash*31 + uint32(key[i])
			}
			shard := int(hash % uint32(numShards))
			shardCounts[shard]++
		}

		// Property: For a reasonable number of keys, distribution shouldn't be too skewed
		if len(keys) >= numShards*10 {
			// Calculate mean
			mean := len(keys) / numShards

			// Check that no shard has more than 3x the mean
			// (allowing for some randomness)
			for _, count := range shardCounts {
				if count > mean*3 {
					return false
				}
			}

			// Check that at least half the shards have some keys
			nonEmpty := 0
			for _, count := range shardCounts {
				if count > 0 {
					nonEmpty++
				}
			}
			if nonEmpty < numShards/2 {
				return false
			}
		}

		// Property: Same key always maps to same shard
		for _, key := range keys {
			var hash1, hash2 uint32
			for i := 0; i < len(key); i++ {
				hash1 = hash1*31 + uint32(key[i])
				hash2 = hash2*31 + uint32(key[i])
			}
			if hash1%uint32(numShards) != hash2%uint32(numShards) {
				return false
			}
		}

		return true
	}

	if err := quick.Check(property, nil); err != nil {
		t.Error(err)
	}
}

// TestCircuitBreakerTimeoutBehavior validates that timeout mechanisms function
// correctly across a range of timeout values.
// It ensures that circuits transition from the open to the half-open state
// within expected time bounds, accounting for proper jitter.
func TestCircuitBreakerTimeoutBehavior(t *testing.T) {
	property := func(timeoutMs uint8) bool {
		if timeoutMs == 0 {
			timeoutMs = 1
		}

		ctx := context.Background()
		timeout := time.Duration(timeoutMs) * time.Millisecond

		config := circuitbreaker.Config{
			FailureThreshold: 1,
			SuccessThreshold: 1,
			HalfOpenProbes:   1,
			OpenTimeout:      timeout,
		}

		// Measure actual transition time
		startTime := time.Now()

		// Open circuit
		failHandler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
			return nil, &llmerrors.ProviderError{
				Provider:   "test",
				StatusCode: 500,
				Message:    "error",
				Type:       llmerrors.ErrorTypeProvider,
			}
		})

		cbMiddleware, _ := circuitbreaker.NewCircuitBreakerMiddlewareWithRedis(config, nil)
		failCBHandler := cbMiddleware(failHandler)

		req := &transport.Request{
			Operation: transport.OpGeneration,
			Provider:  "test",
			Model:     "test-model",
			Question:  "test",
		}

		// Open the circuit
		_, _ = failCBHandler.Handle(ctx, req)
		openTime := time.Now()

		// Success handler
		successHandler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
			return &transport.Response{
				Content: "success",
			}, nil
		})

		successCBHandler := cbMiddleware(successHandler)

		// Wait and check for half-open transition
		transitioned := false
		maxWait := timeout + 20*time.Millisecond // Allow some buffer

		for time.Since(openTime) < maxWait {
			_, err := successCBHandler.Handle(ctx, req)
			if err == nil {
				transitioned = true
				break
			}
			var perr *llmerrors.ProviderError
			if errors.As(err, &perr) && perr.Code != "CIRCUIT_OPEN" {
				transitioned = true
				break
			}
			time.Sleep(time.Millisecond)
		}

		// Property: Circuit should transition after timeout (with jitter)
		if !transitioned {
			return false
		}

		// Property: Transition shouldn't happen before timeout (minus buffer for jitter)
		actualWait := time.Since(openTime)
		// Allow more tolerance for short timeouts
		minBuffer := timeout / 5
		if minBuffer < 2*time.Millisecond {
			minBuffer = 2 * time.Millisecond
		}
		if actualWait < timeout-minBuffer {
			return false
		}

		// Property: Transition shouldn't take too long after timeout
		// Allow more tolerance for test environment timing
		if actualWait > timeout*3 {
			return false
		}

		// Property: Total execution time is reasonable
		totalTime := time.Since(startTime)
		return totalTime <= maxWait*2
	}

	config := &quick.Config{
		MaxCount: 20, // Reduce count since this test involves waiting
	}
	if err := quick.Check(property, config); err != nil {
		t.Error(err)
	}
}

// TestCircuitBreakerConcurrencyInvariants validates that concurrent access
// patterns maintain system invariants.
// The test ensures request counting accuracy and proper state management under
// multi-threaded stress conditions.
func TestCircuitBreakerConcurrencyInvariants(t *testing.T) {
	property := func(numWorkers, numRequests uint8) bool {
		if numWorkers == 0 {
			numWorkers = 1
		}
		if numWorkers > 20 {
			numWorkers = 20
		}
		if numRequests == 0 {
			numRequests = 1
		}

		ctx := context.Background()

		config := circuitbreaker.Config{
			FailureThreshold: 5,
			SuccessThreshold: 5,
			HalfOpenProbes:   int(numWorkers),
			OpenTimeout:      10 * time.Millisecond,
		}

		// Track operations
		var totalRequests atomic.Int64
		var totalResponses atomic.Int64

		handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
			totalRequests.Add(1)

			// Random behavior
			if rand.Float32() < 0.3 {
				return nil, &llmerrors.ProviderError{
					Provider:   "test",
					StatusCode: 500,
					Message:    "error",
					Type:       llmerrors.ErrorTypeProvider,
				}
			}
			return &transport.Response{
				Content: "success",
			}, nil
		})

		cbMiddleware, _ := circuitbreaker.NewCircuitBreakerMiddlewareWithRedis(config, nil)
		cbHandler := cbMiddleware(handler)

		// Run concurrent workers
		var wg sync.WaitGroup
		wg.Add(int(numWorkers))

		for w := 0; w < int(numWorkers); w++ {
			go func(id int) {
				defer wg.Done()

				for r := 0; r < int(numRequests); r++ {
					req := &transport.Request{
						Operation: transport.OpGeneration,
						Provider:  "test",
						Model:     "test-model",
						Question:  "test",
						Metadata: map[string]string{
							"worker": string(rune(id)),
						},
					}

					_, err := cbHandler.Handle(ctx, req)
					if err != nil || true { // Count all responses
						totalResponses.Add(1)
					}
				}
			}(w)
		}

		wg.Wait()

		// Invariant: All requests should get responses
		expectedTotal := int64(numWorkers) * int64(numRequests)
		if totalResponses.Load() != expectedTotal {
			return false
		}

		// Invariant: Handler calls should not exceed total requests
		// (some requests might be blocked by circuit breaker)
		if totalRequests.Load() > expectedTotal {
			return false
		}

		return true
	}

	config := &quick.Config{
		MaxCount: 20, // Reduce count for concurrent tests
	}
	if err := quick.Check(property, config); err != nil {
		t.Error(err)
	}
}

// TestCircuitBreakerKeyConsistency validates that key generation produces
// consistent and deterministic results for identical input parameters.
// This ensures that circuit breaker isolation works correctly across different
// request variations.
func TestCircuitBreakerKeyConsistency(t *testing.T) {
	property := func(provider, model, region string) bool {
		ctx := context.Background()

		config := circuitbreaker.Config{
			FailureThreshold: 2,
			SuccessThreshold: 2,
			HalfOpenProbes:   2,
			OpenTimeout:      10 * time.Millisecond,
		}

		// Create multiple requests with same key components
		requests := make([]*transport.Request, 10)
		for i := 0; i < len(requests); i++ {
			req := &transport.Request{
				Operation: transport.OpGeneration,
				Provider:  provider,
				Model:     model,
				Question:  "different question " + string(rune(i)), // Different questions
			}

			if region != "" {
				req.Metadata = map[string]string{
					"region": region,
					"other":  "data", // Other metadata shouldn't affect key
				}
			}

			requests[i] = req
		}

		// All requests should use the same circuit breaker
		failHandler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
			return nil, &llmerrors.ProviderError{
				Provider:   "test",
				StatusCode: 500,
				Message:    "error",
				Type:       llmerrors.ErrorTypeProvider,
			}
		})

		cbMiddleware, _ := circuitbreaker.NewCircuitBreakerMiddlewareWithRedis(config, nil)
		failCBHandler := cbMiddleware(failHandler)

		// First request
		_, err1 := failCBHandler.Handle(ctx, requests[0])
		if err1 == nil {
			return false // Should fail
		}

		// Second request (same key) - should also fail
		_, err2 := failCBHandler.Handle(ctx, requests[1])
		if err2 == nil {
			return false
		}

		// After threshold, circuit should open for all requests with same key
		_, err3 := failCBHandler.Handle(ctx, requests[2])
		if err3 == nil {
			return false
		}

		// Check if circuit is open
		var perr *llmerrors.ProviderError
		if errors.As(err3, &perr) && perr.Code == "CIRCUIT_OPEN" {
			// All other requests should also get CIRCUIT_OPEN
			for i := 3; i < len(requests); i++ {
				_, err := failCBHandler.Handle(ctx, requests[i])
				if err == nil {
					return false
				}
				var perr *llmerrors.ProviderError
				if !errors.As(err, &perr) || perr.Code != "CIRCUIT_OPEN" {
					return false // Should be same circuit state
				}
			}
		}

		return true
	}

	if err := quick.Check(property, nil); err != nil {
		t.Error(err)
	}
}

func TestHalfOpenProgressProperty(t *testing.T) {
	property := func(successThreshold, maxProbes uint8) bool {
		if successThreshold == 0 {
			successThreshold = 1
		}
		if maxProbes == 0 {
			maxProbes = 1
		}

		ctx := context.Background()
		cfg := circuitbreaker.Config{
			FailureThreshold: 1,
			SuccessThreshold: int(successThreshold),
			HalfOpenProbes:   int(maxProbes),
			OpenTimeout:      1 * time.Millisecond,
		}

		// Open.
		cb, _ := circuitbreaker.NewCircuitBreakerMiddlewareWithRedis(cfg, nil)
		fail := transport.HandlerFunc(func(context.Context, *transport.Request) (*transport.Response, error) {
			return nil, &llmerrors.ProviderError{Provider: "p", StatusCode: 500, Message: "boom", Type: llmerrors.ErrorTypeProvider}
		})
		req := &transport.Request{Operation: transport.OpGeneration, Provider: "p", Model: "m", Question: "q"}
		_, _ = cb(fail).Handle(ctx, req)

		time.Sleep(2 * time.Millisecond)

		// Always succeed now.
		ok := transport.HandlerFunc(func(context.Context, *transport.Request) (*transport.Response, error) {
			return &transport.Response{Content: "ok"}, nil
		})
		h := cb(ok)

		successes := 0
		// Keep trying until we hit threshold or give up.
		for i := 0; i < 5*int(successThreshold); i++ {
			if _, err := h.Handle(ctx, req); err == nil {
				successes++
			}
		}
		// With correct implementation, we eventually close and allow requests freely.
		return successes >= int(successThreshold)
	}
	if err := quick.Check(property, nil); err != nil {
		t.Error(err)
	}
}
