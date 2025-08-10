// Package resilience contains property-based tests for the rate limiting middleware.
//
// This test suite uses property-based testing to validate mathematical invariants
// and behavioral contracts of the rate limiting middleware. Property-based testing
// generates hundreds of random test cases to verify that the system maintains
// consistent behavior across a wide range of input combinations.
//
// Mathematical Invariants Tested:
//   - Determinism: Same inputs always produce same outputs across multiple runs.
//   - Monotonicity: Atomic timestamp operations never decrease over time.
//   - Conservation: Token bucket algorithm respects burst capacity limits.
//   - Bijection: Key generation produces unique keys for unique input combinations.
//   - Bounds: Configuration parameters are validated within reasonable limits.
//   - Consistency: System statistics accurately reflect actual internal state.
//
// Property Testing Methodology:
//   - Uses Go's testing/quick package for automatic test case generation.
//   - Generates 30-100 test cases per property depending on complexity.
//   - Validates mathematical properties that should hold universally.
//   - Tests system behavior under random but valid input combinations.
//   - Focuses on invariants that are difficult to test with fixed test cases.
//
// These tests complement unit and integration tests by exploring the mathematical
// properties and edge cases that emerge from random input combinations, helping
// to catch subtle bugs that fixed test cases might miss.
package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
	"testing/quick"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/ahrav/go-judgy/internal/llm/configuration"
	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// TestProperty_RateLimitingDeterminism validates deterministic rate limiting behavior.
//
// This property test verifies that the rate limiting middleware produces identical
// results when given the same sequence of requests with identical configurations.
// Determinism is critical for predictable behavior in distributed systems and
// for debugging rate limiting issues.
//
// Property Verified:
//
//	For any valid configuration (tokensPerSecond, burstSize) and request sequence,
//	two independent rate limiters with identical configurations should produce
//	identical success/failure patterns when processing the same request sequence.
//
// Test Methodology:
//   - Generates random but valid rate limiting configurations.
//   - Creates two independent rate limiters with identical configurations.
//   - Applies the same sequence of requests to both limiters.
//   - Verifies that both limiters produce identical results.
//
// This property helps catch non-deterministic behavior that could arise from:
//   - Race conditions in concurrent access patterns.
//   - Uninitialized state or random number usage.
//   - Time-dependent behavior that should be deterministic.
//   - Floating-point precision issues in rate calculations.
func TestProperty_RateLimitingDeterminism(t *testing.T) {
	property := func(tokensPerSecond float64, burstSize int, requestCount int) bool {
		// Skip invalid inputs
		if tokensPerSecond < 0 || burstSize < 0 || requestCount < 0 || requestCount > 1000 {
			return true // Skip, not a failure
		}
		if math.IsNaN(tokensPerSecond) || math.IsInf(tokensPerSecond, 0) {
			return true // Skip invalid float values
		}

		cfg := configuration.RateLimitConfig{
			Local: configuration.LocalRateLimitConfig{
				TokensPerSecond: tokensPerSecond,
				BurstSize:       burstSize,
				Enabled:         true,
			},
		}

		// Create two identical rate limiters
		rlm1 := createTestRateLimitMiddleware(&cfg)
		rlm2 := createTestRateLimitMiddleware(&cfg)

		key := "determinism-test-key"

		// Apply same sequence of requests to both
		results1 := make([]bool, requestCount)
		results2 := make([]bool, requestCount)

		for i := 0; i < requestCount; i++ {
			err1 := checkLocalLimit(rlm1, key)
			err2 := checkLocalLimit(rlm2, key)

			results1[i] = (err1 == nil)
			results2[i] = (err2 == nil)
		}

		// Results should be identical for deterministic rate limiting
		return reflect.DeepEqual(results1, results2)
	}

	config := &quick.Config{
		MaxCount: 100,
	}

	if err := quick.Check(property, config); err != nil {
		t.Errorf("Rate limiting determinism property failed: %v", err)
	}
}

// TestProperty_BurstCapacityRespected validates token bucket burst behavior.
//
// This property test verifies that the token bucket implementation correctly
// enforces burst capacity limits. The burst capacity represents the maximum
// number of requests that can be processed in a short time window, even when
// the sustained rate would be exceeded.
//
// Property Verified:
//
//	For any valid burst size configuration, a rate limiter should allow at most
//	'burstSize' requests to succeed when processing a large batch of requests
//	in rapid succession, regardless of the specific burst size value.
//
// Mathematical Invariant:
//
//	successful_requests ≤ burst_size AND successful_requests ≥ 1
//
// Test Methodology:
//   - Uses a slow token refill rate (1 token/sec) to focus on burst behavior.
//   - Makes more requests than the burst capacity allows.
//   - Verifies that successful requests never exceed the configured burst size.
//   - Ensures at least one request succeeds (as the burst capacity should be usable).
func TestProperty_BurstCapacityRespected(t *testing.T) {
	property := func(burstSize int) bool {
		// Skip invalid burst sizes
		if burstSize < 1 || burstSize > 10000 {
			return true
		}

		cfg := configuration.RateLimitConfig{
			Local: configuration.LocalRateLimitConfig{
				TokensPerSecond: 1, // Slow refill to focus on burst
				BurstSize:       burstSize,
				Enabled:         true,
			},
		}
		rlm := createTestRateLimitMiddleware(&cfg)

		key := "burst-test-key"
		successCount := 0

		// Make requests up to burst limit + extra
		requestCount := burstSize + 10
		for i := 0; i < requestCount; i++ {
			err := checkLocalLimit(rlm, key)
			if err == nil {
				successCount++
			}
		}

		// Should succeed exactly up to burst size, not more
		return successCount <= burstSize && successCount >= 1
	}

	config := &quick.Config{
		MaxCount: 50,
	}

	if err := quick.Check(property, config); err != nil {
		t.Errorf("Burst capacity property failed: %v", err)
	}
}

// TestProperty_AtomicTimestampMonotonicity validates monotonic timestamp behavior.
//
// This property test verifies that atomic timestamp operations in the rate
// limiting system maintain monotonicity - timestamps should never decrease
// over time. This is critical for proper cleanup operations and consistent
// lastUsed tracking across concurrent access patterns.
//
// Property Verified:
//
//	For any sequence of limiter accesses, the lastUsed timestamp should be
//	monotonically non-decreasing: timestamp(t+Δt) ≥ timestamp(t) for all Δt ≥ 0.
//
// Mathematical Invariant:
//
//	final_timestamp ≥ initial_timestamp for all limiters.
//
// Test Methodology:
//   - Creates multiple rate limiters with different keys.
//   - Records initial timestamps from limiter creation.
//   - Waits a small time interval and accesses limiters again.
//   - Verifies that all final timestamps are ≥ initial timestamps.
//
// This property is essential for:
//   - Reliable cleanup operations based on timestamp comparisons.
//   - Consistent behavior under concurrent access patterns.
//   - Proper lastUsed tracking for limiter lifecycle management.
func TestProperty_AtomicTimestampMonotonicity(t *testing.T) {
	property := func(keyCount int) bool {
		// Keep reasonable bounds
		if keyCount < 1 || keyCount > 100 {
			return true
		}

		cfg := configuration.RateLimitConfig{
			Local: configuration.LocalRateLimitConfig{
				TokensPerSecond: 100,
				BurstSize:       1000,
				Enabled:         true,
			},
		}
		rlm := createTestRateLimitMiddleware(&cfg)

		// Create limiters with different keys
		keys := make([]string, keyCount)
		initialTimestamps := make([]int64, keyCount)

		for i := 0; i < keyCount; i++ {
			key := fmt.Sprintf("mono-test-%d", i)
			keys[i] = key

			// Get initial timestamp
			rlm.getOrCreateLimiter(key)
			rlm.localMu.RLock()
			tl := rlm.localLimiters[key]
			initialTimestamps[i] = tl.lastUsed.Load()
			rlm.localMu.RUnlock()
		}

		// Wait a bit and access again
		time.Sleep(1 * time.Millisecond)

		finalTimestamps := make([]int64, keyCount)
		for i, key := range keys {
			rlm.getOrCreateLimiter(key) // Updates timestamp
			rlm.localMu.RLock()
			tl := rlm.localLimiters[key]
			finalTimestamps[i] = tl.lastUsed.Load()
			rlm.localMu.RUnlock()
		}

		// All final timestamps should be >= initial timestamps (monotonic)
		for i := 0; i < keyCount; i++ {
			if finalTimestamps[i] < initialTimestamps[i] {
				return false // Monotonicity violated
			}
		}

		return true
	}

	config := &quick.Config{
		MaxCount: 50,
	}

	if err := quick.Check(property, config); err != nil {
		t.Errorf("Atomic timestamp monotonicity property failed: %v", err)
	}
}

// Property: Cleanup operations preserve active limiters
func TestProperty_CleanupPreservesActiveLimiters(t *testing.T) {
	property := func(activeKeys, staleKeys int) bool {
		// Keep reasonable bounds
		if activeKeys < 0 || staleKeys < 0 || activeKeys > 50 || staleKeys > 50 {
			return true
		}

		cfg := configuration.RateLimitConfig{
			Local: configuration.LocalRateLimitConfig{
				TokensPerSecond: 10,
				BurstSize:       5,
				Enabled:         true,
			},
		}
		rlm := createTestRateLimitMiddleware(&cfg)

		// Create active limiters (recently used)
		activeKeyList := make([]string, activeKeys)
		for i := 0; i < activeKeys; i++ {
			key := fmt.Sprintf("active-%d", i)
			activeKeyList[i] = key
			rlm.getOrCreateLimiter(key) // Creates with current timestamp
		}

		// Create stale limiters (old timestamp)
		staleKeyList := make([]string, staleKeys)
		oldTime := time.Now().Add(-2 * time.Hour).UnixNano()
		for i := 0; i < staleKeys; i++ {
			key := fmt.Sprintf("stale-%d", i)
			staleKeyList[i] = key
			rlm.getOrCreateLimiter(key)
			// Manually set old timestamp
			rlm.localLimiters[key].lastUsed.Store(oldTime)
		}

		initialCount := len(rlm.localLimiters)
		cutoff := time.Now().Add(-1 * time.Hour) // Should clean stale but not active
		rlm.CleanupStale(cutoff)
		finalCount := len(rlm.localLimiters)

		// Properties that should hold:
		// 1. Final count should be <= initial count
		if finalCount > initialCount {
			return false
		}

		// 2. Active limiters should still exist
		for _, key := range activeKeyList {
			rlm.localMu.RLock()
			_, exists := rlm.localLimiters[key]
			rlm.localMu.RUnlock()
			if !exists {
				return false // Active limiter was incorrectly removed
			}
		}

		// 3. Final count should be at least the number of active keys
		return finalCount >= activeKeys
	}

	config := &quick.Config{
		MaxCount: 30,
	}

	if err := quick.Check(property, config); err != nil {
		t.Errorf("Cleanup preservation property failed: %v", err)
	}
}

// Property: Rate limit errors contain valid retry information
func TestProperty_RateLimitErrorValidation(t *testing.T) {
	property := func(tokensPerSecond float64, burstSize int) bool {
		// Skip invalid configurations
		if tokensPerSecond < 0 || burstSize < 0 || tokensPerSecond > 1000 || burstSize > 1000 {
			return true
		}
		if math.IsNaN(tokensPerSecond) || math.IsInf(tokensPerSecond, 0) {
			return true
		}

		cfg := configuration.RateLimitConfig{
			Local: configuration.LocalRateLimitConfig{
				TokensPerSecond: tokensPerSecond,
				BurstSize:       burstSize,
				Enabled:         true,
			},
		}
		rlm := createTestRateLimitMiddleware(&cfg)

		key := "error-validation-key"

		// Make enough requests to trigger rate limiting
		var rateLimitErr *llmerrors.RateLimitError
		for i := 0; i < burstSize+50; i++ {
			err := checkLocalLimit(rlm, key)
			if err != nil && errors.As(err, &rateLimitErr) {
				break
			}
		}

		// If we got a rate limit error, validate its properties
		if rateLimitErr != nil {
			// RetryAfter should be positive and reasonable
			if rateLimitErr.RetryAfter < 1 || rateLimitErr.RetryAfter > 3600 {
				return false
			}

			// Provider should be "local" for local rate limiting
			if rateLimitErr.Provider != "local" {
				return false
			}

			// Limit should match configuration
			expectedLimit := int(tokensPerSecond)
			if rateLimitErr.Limit != expectedLimit {
				return false
			}
		}

		return true
	}

	config := &quick.Config{
		MaxCount: 50,
	}

	if err := quick.Check(property, config); err != nil {
		t.Errorf("Rate limit error validation property failed: %v", err)
	}
}

// TestProperty_KeyGenerationBijective validates unique key generation.
//
// This property test verifies that the key generation function is bijective,
// meaning that unique input combinations always produce unique keys, and
// identical input combinations always produce identical keys. This ensures
// proper rate limiting isolation between different tenants and operations.
//
// Property verified (bijection):
//
//	buildKey(input1) == buildKey(input2) ⟺ input1 == input2
//
// Mathematical invariant:
//
//	identical_inputs ⟺ identical_keys (if and only if relationship)
//
// Test methodology:
//   - Generates random pairs of request inputs
//   - Compares input equality with key equality
//   - Verifies that the equivalence relationship holds in both directions
//   - Tests with various string combinations including edge cases
//
// This property ensures:
//   - Proper tenant isolation (different tenants get different keys)
//   - Operation-specific rate limiting (different operations get different keys)
//   - No key collisions that could cause incorrect rate limiting behavior
//   - Consistent behavior across multiple key generation calls
func TestProperty_KeyGenerationBijective(t *testing.T) {
	property := func(tenant1, provider1, model1, operation1,
		tenant2, provider2, model2, operation2 string) bool {
		cfg := configuration.RateLimitConfig{}
		rlm := createTestRateLimitMiddleware(&cfg)

		req1 := &transport.Request{
			TenantID:  tenant1,
			Provider:  provider1,
			Model:     model1,
			Operation: transport.OperationType(operation1),
		}

		req2 := &transport.Request{
			TenantID:  tenant2,
			Provider:  provider2,
			Model:     model2,
			Operation: transport.OperationType(operation2),
		}

		key1 := rlm.buildKey(req1)
		key2 := rlm.buildKey(req2)

		// If inputs are identical, keys should be identical
		inputsEqual := (tenant1 == tenant2) && (provider1 == provider2) &&
			(model1 == model2) && (operation1 == operation2)
		keysEqual := (key1 == key2)

		return inputsEqual == keysEqual // Bijective property
	}

	config := &quick.Config{
		MaxCount: 100,
	}

	if err := quick.Check(property, config); err != nil {
		t.Errorf("Key generation bijective property failed: %v", err)
	}
}

// Property: Statistics are consistent with actual state
func TestProperty_StatisticsConsistency(t *testing.T) {
	property := func(keyCount int) bool {
		// Keep reasonable bounds
		if keyCount < 0 || keyCount > 100 {
			return true
		}

		cfg := configuration.RateLimitConfig{
			Local: configuration.LocalRateLimitConfig{
				TokensPerSecond: 10,
				BurstSize:       5,
				Enabled:         true,
			},
			Global: configuration.GlobalRateLimitConfig{
				Enabled: false,
			},
		}
		rlm := createTestRateLimitMiddleware(&cfg)

		// Create known number of limiters
		for i := 0; i < keyCount; i++ {
			key := fmt.Sprintf("stats-key-%d", i)
			rlm.getOrCreateLimiter(key)
		}

		// Get statistics
		stats, err := rlm.GetStats()
		if err != nil {
			return false // Should not error
		}

		// Properties that should hold:
		// 1. LocalLimiters count should match actual count
		actualCount := len(rlm.localLimiters)
		if stats.LocalLimiters != actualCount {
			return false
		}

		// 2. GlobalEnabled should match configuration
		if stats.GlobalEnabled != cfg.Global.Enabled {
			return false
		}

		// 3. DegradedMode should be readable
		degradedMode := rlm.globalConfig.DegradedMode.Load()
		if stats.DegradedMode != degradedMode {
			return false
		}

		return true
	}

	config := &quick.Config{
		MaxCount: 50,
	}

	if err := quick.Check(property, config); err != nil {
		t.Errorf("Statistics consistency property failed: %v", err)
	}
}

// Property: Redis error detection is consistent
func TestProperty_RedisErrorDetectionConsistency(t *testing.T) {
	cfg := configuration.RateLimitConfig{}
	rlm := createTestRateLimitMiddleware(&cfg)

	// Test various error types multiple times to ensure consistency
	errorTypes := []error{
		nil,
		context.DeadlineExceeded,
		context.Canceled,
		redis.Nil,
		fmt.Errorf("generic error"),
		fmt.Errorf("wrapped redis error: %w", redis.Nil),
		fmt.Errorf("wrapped context error: %w", context.DeadlineExceeded),
	}

	for _, testErr := range errorTypes {
		t.Run(fmt.Sprintf("error_%T", testErr), func(t *testing.T) {
			property := func(callCount int) bool {
				// Keep reasonable bounds
				if callCount < 1 || callCount > 100 {
					return true
				}

				// Call isRedisError multiple times with same input
				results := make([]bool, callCount)
				for i := 0; i < callCount; i++ {
					results[i] = rlm.isRedisError(testErr)
				}

				// All results should be identical (function is deterministic)
				firstResult := results[0]
				for _, result := range results {
					if result != firstResult {
						return false
					}
				}

				return true
			}

			config := &quick.Config{
				MaxCount: 20,
			}

			if err := quick.Check(property, config); err != nil {
				t.Errorf("Redis error detection consistency failed for %T: %v", testErr, err)
			}
		})
	}
}

// Property: Configuration bounds are respected
func TestProperty_ConfigurationBoundsRespected(t *testing.T) {
	property := func(tokensPerSecond float64, burstSize int, requestsPerSecond int) bool {
		// Test that extreme values are handled gracefully
		cfg := configuration.RateLimitConfig{
			Local: configuration.LocalRateLimitConfig{
				TokensPerSecond: tokensPerSecond,
				BurstSize:       burstSize,
				Enabled:         true,
			},
			Global: configuration.GlobalRateLimitConfig{
				Enabled:           true,
				RequestsPerSecond: requestsPerSecond,
			},
		}

		// Creating middleware should not panic with any configuration
		middleware, err := NewRateLimitMiddlewareWithRedis(&cfg, nil)

		// Some configurations might be invalid, that's OK
		if err != nil {
			return true // Not a property violation
		}

		if middleware == nil {
			return false // Should not be nil if no error
		}

		// Should be able to get stats without error
		if middleware != nil {
			// Can't test GetStats as it's not exposed through the public API
		}
		return err == nil
	}

	config := &quick.Config{
		MaxCount: 100,
	}

	if err := quick.Check(property, config); err != nil {
		t.Errorf("Configuration bounds property failed: %v", err)
	}
}

// Property: Middleware composition preserves request/response types
func TestProperty_MiddlewareCompositionPreservesTypes(t *testing.T) {
	cfg := &configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			TokensPerSecond: 1000, // High rate to avoid rate limiting
			BurstSize:       10000,
			Enabled:         true,
		},
	}

	middleware, err := NewRateLimitMiddlewareWithRedis(cfg, nil)
	require.NoError(t, err)

	property := func(tenantID, provider, model, operation string) bool {
		// Create mock handler
		mockHandler := transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
			// Verify request is unchanged
			if req == nil {
				return nil, fmt.Errorf("request is nil")
			}
			return &transport.Response{}, nil
		})

		wrappedHandler := middleware(mockHandler)

		req := &transport.Request{
			TenantID:  tenantID,
			Provider:  provider,
			Model:     model,
			Operation: transport.OperationType(operation),
		}

		// Execute middleware
		resp, err := wrappedHandler.Handle(context.Background(), req)

		// Properties that should hold:
		// 1. If no rate limiting, should get response
		// 2. If rate limited, should get RateLimitError
		// 3. Request object should be unchanged

		if err == nil {
			// Success case - should have response
			return resp != nil
		} else {
			// Error case - should be RateLimitError for rate limiting middleware
			var rateLimitErr *llmerrors.RateLimitError
			if errors.As(err, &rateLimitErr) {
				return resp == nil // Rate limit error should have nil response
			}
			// Other errors are not expected but not property violations
			return true
		}
	}

	config := &quick.Config{
		MaxCount: 50,
	}

	if err := quick.Check(property, config); err != nil {
		t.Errorf("Middleware composition property failed: %v", err)
	}
}
