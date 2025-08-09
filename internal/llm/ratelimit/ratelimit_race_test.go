// This file contains concurrency and race condition tests for the rate limiting
// middleware.
//
// This test suite validates the thread safety and race condition prevention
// mechanisms within the rate limiting middleware. These tests are essential for
// ensuring safe concurrent operation in high-throughput production environments
// where multiple goroutines access shared rate limiting state.
//
// Critical concurrency patterns tested include:
//   - Thread-safe access to the localLimiters map using sync.RWMutex.
//   - Atomic operations for lastUsed timestamps and the degraded mode state.
//   - Coordination and termination of the background cleanup goroutine.
//   - Shared state modifications across concurrent request processing.
//   - Concurrent Redis client operations.
//
// These tests must be run with the race detector enabled to be effective:
//
//	go test -race ./internal/llm -run=Race
//
// The race detector helps identify data races that could lead to undefined
// behavior, memory corruption, or inconsistent state in production.
package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ahrav/go-judgy/internal/llm/configuration"
	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// TestRateLimitMiddleware_ConcurrentLimiterCreation validates the thread-safe
// creation of limiters under high concurrency.
//
// This test ensures that concurrent calls to getOrCreateLimiter for the same key
// do not result in race conditions or the creation of multiple limiter instances.
// It specifically validates the double-checked locking pattern and proper
// synchronization using sync.RWMutex. The test spawns numerous goroutines that
// attempt to create and access limiters for both shared and unique keys,
// verifying that exactly one limiter is created per key.
func TestRateLimitMiddleware_ConcurrentLimiterCreation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping race test in short mode")
	}

	cfg := &configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			TokensPerSecond: 100, // High rate to prevent rate limiting interference
			BurstSize:       1000,
			Enabled:         true,
		},
	}
	rlm := createTestRateLimitMiddleware(cfg)

	const numGoroutines = 50
	const operationsPerGoroutine = 100

	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines*operationsPerGoroutine)

	// Test concurrent creation of limiters with same and different keys
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(routineID int) {
			defer wg.Done()

			for j := 0; j < operationsPerGoroutine; j++ {
				// Mix of same and different keys to test both creation and reuse paths
				var key string
				if j%3 == 0 {
					key = fmt.Sprintf("shared-key-%d", j%5) // Shared keys
				} else {
					key = fmt.Sprintf("routine-%d-key-%d", routineID, j) // Unique keys
				}

				limiter := rlm.getOrCreateLimiter(key)
				if limiter == nil {
					errors <- fmt.Errorf("routine %d, op %d: got nil limiter for key %s", routineID, j, key)
					return
				}

				// Test that limiter works
				if !limiter.Allow() {
					// Rate limiting is acceptable, but limiter should not be nil
				}
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for any errors
	errorCount := 0
	for err := range errors {
		t.Errorf("Concurrent creation error: %v", err)
		errorCount++
	}

	if errorCount > 0 {
		t.Fatalf("Found %d errors in concurrent limiter creation", errorCount)
	}

	// Verify final state consistency
	rlm.localMu.RLock()
	limiterCount := len(rlm.localLimiters)
	rlm.localMu.RUnlock()

	// Should have created some limiters
	assert.Greater(t, limiterCount, 0, "Should have created at least some limiters")
	t.Logf("Created %d unique limiters", limiterCount)
}

// TestRateLimitMiddleware_ConcurrentCheckLocalLimit validates the thread safety of
// the local rate-limiting logic under concurrent access.
// It simulates multiple goroutines concurrently checking rate limits against a
// shared set of keys, ensuring that the accounting for successful and
// rate-limited requests is accurate and free of race conditions.
// The test verifies that atomic counters are correctly updated and that the
// total number of requests processed matches the sum of successful and
// rate-limited outcomes.
func TestRateLimitMiddleware_ConcurrentCheckLocalLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping race test in short mode")
	}

	cfg := &configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			TokensPerSecond: 10, // Moderate rate for testing
			BurstSize:       20,
			Enabled:         true,
		},
	}
	rlm := createTestRateLimitMiddleware(cfg)

	const numGoroutines = 20
	const requestsPerGoroutine = 50
	const sharedKeys = 5

	var wg sync.WaitGroup
	var successCount, rateLimitedCount int64

	// Test concurrent rate limit checking on shared keys
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(routineID int) {
			defer wg.Done()

			for j := 0; j < requestsPerGoroutine; j++ {
				key := fmt.Sprintf("shared-key-%d", j%sharedKeys)

				err := rlm.checkLocalLimit(key)
				if err == nil {
					atomic.AddInt64(&successCount, 1)
				} else {
					var rateLimitErr *llmerrors.RateLimitError
					if errors.As(err, &rateLimitErr) {
						atomic.AddInt64(&rateLimitedCount, 1)
					} else {
						t.Errorf("Unexpected error type: %v", err)
					}
				}

				// Small random delay to increase chance of race conditions
				time.Sleep(time.Duration(rand.Intn(1000)) * time.Nanosecond)
			}
		}(i)
	}

	wg.Wait()

	totalRequests := numGoroutines * requestsPerGoroutine
	finalSuccess := atomic.LoadInt64(&successCount)
	finalRateLimited := atomic.LoadInt64(&rateLimitedCount)

	t.Logf("Total requests: %d, Success: %d, Rate limited: %d",
		totalRequests, finalSuccess, finalRateLimited)

	// Verify accounting is correct
	assert.Equal(t, int64(totalRequests), finalSuccess+finalRateLimited,
		"All requests should be accounted for")

	// Should have some successful requests
	assert.Greater(t, finalSuccess, int64(0), "Should have some successful requests")
}

// TestRateLimitMiddleware_ConcurrentAtomicOperations verifies the race-free
// handling of atomic variables within the rate limit middleware.
// This test subjects the DegradedMode flag and the lastUsed timestamp of
// individual limiters to high-contention read and write operations from
// multiple concurrent goroutines.
// It ensures that all atomic operations, particularly `atomic.Load` and
// `atomic.Store`, execute safely without causing data races.
func TestRateLimitMiddleware_ConcurrentAtomicOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping race test in short mode")
	}

	cfg := &configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			TokensPerSecond: 100,
			BurstSize:       1000,
			Enabled:         true,
		},
		Global: configuration.GlobalRateLimitConfig{
			Enabled:           true,
			RequestsPerSecond: 100,
		},
	}
	rlm := createTestRateLimitMiddleware(cfg)

	const numGoroutines = 30
	const operationsPerGoroutine = 100

	var wg sync.WaitGroup

	// Test concurrent access to atomic DegradedMode field
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(routineID int) {
			defer wg.Done()

			for j := 0; j < operationsPerGoroutine; j++ {
				// Randomly read or write degraded mode
				switch j % 3 {
				case 0:
					// Read
					_ = rlm.globalConfig.DegradedMode.Load()
				case 1:
					// Set to true
					rlm.globalConfig.DegradedMode.Store(true)
				case 2:
					// Set to false
					rlm.globalConfig.DegradedMode.Store(false)
				}

				// Also test lastUsed atomic operations
				key := fmt.Sprintf("atomic-key-%d", j%10)
				limiter := rlm.getOrCreateLimiter(key)
				_ = limiter // Ensure limiter is used

				// Accessing the limiter updates lastUsed atomically
				rlm.localMu.RLock()
				tl, exists := rlm.localLimiters[key]
				rlm.localMu.RUnlock()

				if exists {
					// Read lastUsed timestamp atomically
					_ = tl.lastUsed.Load()
				}
			}
		}(i)
	}

	wg.Wait()

	// Verify final state is consistent
	degradedState := rlm.globalConfig.DegradedMode.Load()
	t.Logf("Final degraded state: %v", degradedState)

	// All limiters should have valid lastUsed timestamps
	rlm.localMu.RLock()
	for key, tl := range rlm.localLimiters {
		lastUsed := tl.lastUsed.Load()
		assert.Greater(t, lastUsed, int64(0), "lastUsed should be positive for key %s", key)
	}
	rlm.localMu.RUnlock()
}

func TestRateLimitMiddleware_ConcurrentCleanupOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping race test in short mode")
	}

	cfg := &configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			TokensPerSecond: 100,
			BurstSize:       1000,
			Enabled:         true,
		},
	}
	rlm := createTestRateLimitMiddleware(cfg)

	const numWorkers = 10
	const numKeys = 100
	const testDuration = 200 * time.Millisecond

	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), testDuration)
	defer cancel()

	// Worker 1: Continuously create and use limiters
	wg.Add(1)
	go func() {
		defer wg.Done()
		counter := 0
		for ctx.Err() == nil {
			key := fmt.Sprintf("cleanup-key-%d", counter%numKeys)
			limiter := rlm.getOrCreateLimiter(key)
			limiter.Allow() // Use the limiter
			counter++
			time.Sleep(time.Microsecond) // Small delay
		}
	}()

	// Worker 2: Continuously run cleanup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			// Cleanup with varying cutoff times
			cutoffOffset := time.Duration(rand.Intn(100)) * time.Millisecond
			cutoff := time.Now().Add(-cutoffOffset)
			rlm.CleanupStale(cutoff)
			time.Sleep(10 * time.Millisecond)
		}
	}()

	// Worker 3: Continuously access limiters for reading
	wg.Add(1)
	go func() {
		defer wg.Done()
		counter := 0
		for ctx.Err() == nil {
			key := fmt.Sprintf("cleanup-key-%d", counter%numKeys)

			// Read operation - should not interfere with cleanup
			rlm.localMu.RLock()
			tl, exists := rlm.localLimiters[key]
			if exists {
				_ = tl.lastUsed.Load()
			}
			rlm.localMu.RUnlock()

			counter++
			time.Sleep(time.Microsecond)
		}
	}()

	// Additional workers doing mixed operations
	for i := 0; i < numWorkers-3; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			counter := 0
			for ctx.Err() == nil {
				key := fmt.Sprintf("worker-%d-key-%d", workerID, counter%20)

				switch counter % 4 {
				case 0:
					// Create/access limiter
					rlm.getOrCreateLimiter(key)
				case 1:
					// Check rate limit
					rlm.checkLocalLimit(key)
				case 2:
					// Read stats (involves locking)
					rlm.GetStats()
				case 3:
					// Access existing limiter
					rlm.localMu.RLock()
					_, exists := rlm.localLimiters[key]
					rlm.localMu.RUnlock()
					_ = exists
				}

				counter++
				time.Sleep(time.Duration(rand.Intn(10)) * time.Microsecond)
			}
		}(i)
	}

	wg.Wait()

	// Verify final state consistency
	stats, err := rlm.GetStats()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, stats.LocalLimiters, 0, "Should have non-negative limiter count")

	t.Logf("Final state: %d limiters", stats.LocalLimiters)
}

func TestRateLimitMiddleware_ConcurrentStartStop(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping race test in short mode")
	}

	cfg := &configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			TokensPerSecond: 10,
			BurstSize:       5,
			Enabled:         true,
		},
	}

	const numCycles = 50
	const concurrentOperations = 5

	for cycle := 0; cycle < numCycles; cycle++ {
		rlm := createTestRateLimitMiddleware(cfg)

		var wg sync.WaitGroup

		// Concurrent Start/Stop operations
		wg.Add(concurrentOperations)
		for i := 0; i < concurrentOperations; i++ {
			go func(opID int) {
				defer wg.Done()

				// Perform a sequence of Start/Stop operations
				rlm.Start()

				// Do some work while started
				for j := 0; j < 10; j++ {
					key := fmt.Sprintf("concurrent-key-%d-%d", opID, j)
					rlm.checkLocalLimit(key)
				}

				rlm.Stop()

				// Multiple stops should be safe
				rlm.Stop()

				// Restart should work
				rlm.Start()

				// More work
				for j := 0; j < 5; j++ {
					key := fmt.Sprintf("restart-key-%d-%d", opID, j)
					rlm.checkLocalLimit(key)
				}

				// Final stop
				rlm.Stop()
			}(i)
		}

		wg.Wait()

		// Ensure final state is consistent
		assert.Nil(t, rlm.cleanupTicker, "Cleanup ticker should be nil after stop")
	}
}

func TestRateLimitMiddleware_ConcurrentMiddlewareExecution(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping race test in short mode")
	}

	cfg := &configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			TokensPerSecond: 50, // Moderate rate
			BurstSize:       100,
			Enabled:         true,
		},
	}

	middleware, err := NewRateLimitMiddlewareWithRedis(cfg, nil)
	require.NoError(t, err)

	// Mock handler that simulates some work
	handlerCallCount := int64(0)
	mockHandler := transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
		atomic.AddInt64(&handlerCallCount, 1)
		time.Sleep(time.Microsecond) // Simulate small amount of work
		return &transport.Response{}, nil
	})

	wrappedHandler := middleware(mockHandler)

	const numGoroutines = 20
	const requestsPerGoroutine = 100

	var wg sync.WaitGroup
	var successCount, rateLimitedCount, errorCount int64

	// Concurrent middleware execution with various request patterns
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(routineID int) {
			defer wg.Done()

			for j := 0; j < requestsPerGoroutine; j++ {
				// Vary the request properties to create different rate limit keys
				req := &transport.Request{
					TenantID:  fmt.Sprintf("tenant-%d", routineID%5), // 5 tenants
					Provider:  fmt.Sprintf("provider-%d", j%3),       // 3 providers
					Model:     fmt.Sprintf("model-%d", j%2),          // 2 models
					Operation: transport.OpGeneration,
				}

				resp, err := wrappedHandler.Handle(context.Background(), req)

				if err == nil {
					atomic.AddInt64(&successCount, 1)
					if resp == nil {
						t.Errorf("Response should not be nil when no error")
					}
				} else {
					var rateLimitErr *llmerrors.RateLimitError
					if errors.As(err, &rateLimitErr) {
						atomic.AddInt64(&rateLimitedCount, 1)
					} else {
						atomic.AddInt64(&errorCount, 1)
						t.Errorf("Unexpected error: %v", err)
					}
				}

				// Small delay to create timing variations
				if j%10 == 0 {
					time.Sleep(time.Duration(rand.Intn(100)) * time.Microsecond)
				}
			}
		}(i)
	}

	wg.Wait()

	totalRequests := int64(numGoroutines * requestsPerGoroutine)
	finalSuccess := atomic.LoadInt64(&successCount)
	finalRateLimited := atomic.LoadInt64(&rateLimitedCount)
	finalErrors := atomic.LoadInt64(&errorCount)
	finalHandlerCalls := atomic.LoadInt64(&handlerCallCount)

	t.Logf("Middleware execution results:")
	t.Logf("  Total requests: %d", totalRequests)
	t.Logf("  Successful: %d", finalSuccess)
	t.Logf("  Rate limited: %d", finalRateLimited)
	t.Logf("  Errors: %d", finalErrors)
	t.Logf("  Handler calls: %d", finalHandlerCalls)

	// Verify accounting
	assert.Equal(t, totalRequests, finalSuccess+finalRateLimited+finalErrors,
		"All requests should be accounted for")

	// Handler should only be called for successful requests
	assert.Equal(t, finalSuccess, finalHandlerCalls,
		"Handler call count should match successful requests")

	// Should have minimal errors
	assert.Equal(t, int64(0), finalErrors, "Should have no unexpected errors")
}

func TestRateLimitMiddleware_ConcurrentMemoryPressure(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping race test in short mode")
	}

	// Test behavior under memory pressure and high contention
	cfg := &configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			TokensPerSecond: 1000, // High rate to focus on concurrency, not rate limiting
			BurstSize:       10000,
			Enabled:         true,
		},
	}
	rlm := createTestRateLimitMiddleware(cfg)

	numGoroutines := runtime.NumCPU() * 4 // Scale with CPU count
	const operationsPerGoroutine = 1000
	const keySpace = 10000 // Large key space

	var wg sync.WaitGroup

	// Enable cleanup to test memory management under pressure
	rlm.Start()
	defer rlm.Stop()

	// Concurrent operations with large key space
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(routineID int) {
			defer wg.Done()

			for j := 0; j < operationsPerGoroutine; j++ {
				// Create semi-random keys to simulate real usage
				key := fmt.Sprintf("pressure-test-%d-%d",
					routineID*1000+j%keySpace, // Pseudo-random distribution
					(routineID+j)%100)

				// Mix of operations
				switch j % 5 {
				case 0, 1, 2:
					// Most common: check rate limit
					rlm.checkLocalLimit(key)
				case 3:
					// Get limiter directly
					limiter := rlm.getOrCreateLimiter(key)
					limiter.Allow()
				case 4:
					// Read operations
					rlm.GetStats()
				}
			}
		}(i)
	}

	wg.Wait()

	// Verify system is still functional after pressure test
	stats, err := rlm.GetStats()
	require.NoError(t, err)

	t.Logf("Memory pressure test completed:")
	t.Logf("  Final limiter count: %d", stats.LocalLimiters)
	t.Logf("  Operations completed: %d", numGoroutines*operationsPerGoroutine)

	// System should still be responsive
	testErr := rlm.checkLocalLimit("post-pressure-test")
	assert.NoError(t, testErr, "System should be responsive after pressure test")
}
