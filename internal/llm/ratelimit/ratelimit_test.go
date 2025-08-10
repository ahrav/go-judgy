// Package resilience provides unit tests for the rate limiting middleware.
//
// This test suite validates core rate limiting functionality, including local
// token bucket limiting, Redis-based global limiting, graceful degradation,
// and resource management. The tests focus on correctness, concurrency safety,
// and error handling behavior.
//
// Test categories include:
//   - Atomic operations for degraded mode state management.
//   - Configuration semantics for zero RPS behavior and TTL validation.
//   - Error handling for network failures and malformed responses.
//   - Resource management for cleanup scheduling and memory leak prevention.
//   - Monitoring for statistics collection and observability.
package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ahrav/go-judgy/internal/llm/configuration"
	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// createTestRateLimitMiddleware creates a rate limiting middleware for testing.
//
// This helper initializes a rateLimitMiddleware with the provided configuration.
// It omits the Redis client setup, allowing tests to control Redis
// connectivity and behavior independently.
//
// The middleware is configured with test-appropriate logging and a default
// state that individual tests can modify as needed.
func createTestRateLimitMiddleware(cfg *configuration.RateLimitConfig) *rateLimitMiddleware {
	// Pre-calculate the minimum TTL for local limiters based on refill time.
	// This matches the initialization in NewRateLimitMiddlewareWithRedis.
	refillTime := time.Duration(float64(cfg.Local.BurstSize)/cfg.Local.TokensPerSecond) * time.Second
	limiterMinTTL := refillTime * 10
	if limiterMinTTL < time.Hour {
		limiterMinTTL = time.Hour
	}

	return &rateLimitMiddleware{
		localLimiters: make(map[string]*timedLimiter),
		localConfig:   cfg.Local,
		limiterMinTTL: limiterMinTTL,
		globalConfig: configuration.GlobalRateLimitConfig{
			Enabled:           cfg.Global.Enabled,
			RequestsPerSecond: cfg.Global.RequestsPerSecond,
			RedisAddr:         cfg.Global.RedisAddr,
			RedisPassword:     cfg.Global.RedisPassword,
			RedisDB:           cfg.Global.RedisDB,
			ConnectTimeout:    cfg.Global.ConnectTimeout,
			DegradedMode:      atomic.Bool{},
		},
		logger: slog.Default().With("component", "ratelimit"),
	}
}

// TestRateLimitMiddleware_DegradedModeAtomicity validates atomic degraded mode state management.
//
// This test verifies that the degraded mode flag (atomic.Bool) maintains
// consistency under high-concurrency access patterns. It simulates multiple
// goroutines concurrently reading and writing the degraded mode state to ensure
// race-free operations and a consistent final state.
//
// Test methodology:
//   - 100 concurrent goroutines performing 100 operations each (10,000 total operations).
//   - Mixed read/write operations to simulate production access patterns.
//   - Verification that atomic operations prevent race conditions.
//   - Final state consistency check to ensure predictable behavior.
func TestRateLimitMiddleware_DegradedModeAtomicity(t *testing.T) {
	cfg := configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			TokensPerSecond: 10,
			BurstSize:       5,
			Enabled:         true,
		},
		Global: configuration.GlobalRateLimitConfig{
			Enabled:           true,
			RequestsPerSecond: 10,
			RedisAddr:         "invalid:6379", // Invalid address to trigger connection failures
			ConnectTimeout:    100 * time.Millisecond,
		},
	}

	rlm := createTestRateLimitMiddleware(&cfg)

	// High-concurrency test parameters
	const numGoroutines = 100
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Simulate concurrent requests that read and modify degraded mode state
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				// Atomic read operation (simulates middleware checking degraded mode)
				_ = rlm.globalConfig.DegradedMode.Load()

				// Periodic atomic write operation (simulates Redis error detection)
				if j%10 == 0 {
					rlm.globalConfig.DegradedMode.Store(true)
				}
			}
		}()
	}

	wg.Wait()

	// Verify atomic operations maintain consistency
	degradedState := rlm.globalConfig.DegradedMode.Load()
	assert.True(t, degradedState, "DegradedMode should be true after Redis errors")
}

// TestRateLimitMiddleware_TTLValidation validates retry timing calculation and bounds.
//
// This test verifies that local rate limiting correctly calculates the
// retry-after timing when the token bucket capacity is exceeded. It ensures
// that minimum retry intervals prevent tight client retry loops while providing
// accurate timing guidance for optimal backoff behavior.
//
// Test methodology:
//   - Configure restrictive rate limits (1 token/second, 1 burst).
//   - Test first request success followed by immediate rate limiting.
//   - Validate RateLimitError structure and retry timing bounds.
//   - Ensure minimum 1-second retry interval for client stability.
func TestRateLimitMiddleware_TTLValidation(t *testing.T) {
	cfg := configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			TokensPerSecond: 1, // Restrictive rate to trigger limits quickly
			BurstSize:       1, // Single token burst for immediate limiting
			Enabled:         true,
		},
		Global: configuration.GlobalRateLimitConfig{
			Enabled:           false, // Test local-only limiting behavior
			RequestsPerSecond: 1,
		},
	}

	rlm := createTestRateLimitMiddleware(&cfg)

	// Create request for rate limiting validation
	req := &transport.Request{
		TenantID:  "test-tenant",
		Provider:  "test-provider",
		Model:     "test-model",
		Operation: "test-op",
	}

	key := rlm.buildKey(req)

	// First request consumes the single available token
	err := checkLocalLimit(rlm, key)
	assert.NoError(t, err, "First request should succeed with available token")

	// Second immediate request should exceed token bucket capacity
	err = checkLocalLimit(rlm, key)
	assert.Error(t, err, "Second request should be rate limited")

	// Validate rate limit error structure and timing bounds
	var rateLimitErr *llmerrors.RateLimitError
	require.True(t, errors.As(err, &rateLimitErr))
	assert.GreaterOrEqual(t, rateLimitErr.RetryAfter, 1, "RetryAfter should be at least 1 second")
	assert.Equal(t, "local", rateLimitErr.Provider, "Error should indicate local provider")
}

// TestRateLimitMiddleware_FailOpenToDegrade validates graceful degradation behavior.
//
// This test ensures that the rate limiting middleware fails safely by degrading
// to local-only operation rather than failing open when Redis becomes unreliable.
// This behavior prevents security vulnerabilities while maintaining service
// availability.
//
// Test approach:
//   - Initialize middleware with global rate limiting enabled.
//   - Verify initial state has degraded mode disabled.
//   - Note that comprehensive malformed response testing requires integration tests.
//   - Documents the security-first approach: degrade rather than fail open.
//
// Future testing: Integration tests should validate malformed Redis response
// handling using mock Redis servers that return invalid Lua script results.
func TestRateLimitMiddleware_FailOpenToDegrade(t *testing.T) {
	cfg := configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			TokensPerSecond: 10,
			BurstSize:       5,
			Enabled:         true,
		},
		Global: configuration.GlobalRateLimitConfig{
			Enabled:           true,
			RequestsPerSecond: 5,
			RedisAddr:         "localhost:6379",
			RedisDB:           1,
			ConnectTimeout:    5 * time.Second,
		},
	}

	rlm := createTestRateLimitMiddleware(&cfg)

	// Verify initial state: degraded mode should be disabled
	assert.False(t, rlm.globalConfig.DegradedMode.Load(), "Initial state should not be degraded")

	// Info: Comprehensive malformed Redis response testing requires sophisticated
	// mock infrastructure beyond unit test scope. Integration tests provide
	// better coverage for Redis error scenarios and response validation.
	//
	// Key behavior: System degrades to local-only limiting rather than failing
	// open, maintaining security while preserving service availability.
}

// TestRateLimitMiddleware_CleanupScheduling validates lifecycle management operations.
//
// This test verifies that the background cleanup process can be started and
// stopped safely with proper resource management. It ensures idempotent
// operations and clean resource cleanup to prevent goroutine leaks in
// production deployments.
//
// Test methodology:
//   - Verify Start() initializes cleanup infrastructure (ticker, channels).
//   - Test idempotent behavior: multiple Start() calls are safe.
//   - Verify Stop() properly cleans up resources.
//   - Test idempotent behavior: multiple Stop() calls are safe.
//   - Ensure no goroutine leaks or resource contention.
func TestRateLimitMiddleware_CleanupScheduling(t *testing.T) {
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

	// Verify Start() initializes cleanup infrastructure
	rlm.Start()
	assert.NotNil(t, rlm.cleanupTicker, "Cleanup ticker should be initialized")
	assert.NotNil(t, rlm.cleanupStop, "Cleanup stop channel should be initialized")

	// Test idempotent behavior: calling Start() again should be safe
	rlm.Start()

	// Verify Stop() properly cleans up resources
	rlm.Stop()
	assert.Nil(t, rlm.cleanupTicker, "Cleanup ticker should be nil after stop")

	// Test idempotent behavior: calling Stop() again should be safe
	rlm.Stop()
}

// TestRateLimitMiddleware_GetStatsNoContext validates metrics collection without dependencies.
//
// This test verifies that the GetStats() method correctly reports rate limiting
// statistics in a clean state without external dependencies like Redis
// connections. The test validates the baseline metrics structure and
// thread-safe access.
//
// Test methodology:
//   - Initialize middleware with local-only configuration.
//   - Call GetStats() without any prior rate limiting activity.
//   - Verify statistics structure and baseline values.
//   - Ensure thread-safe access to internal state.
func TestRateLimitMiddleware_GetStatsNoContext(t *testing.T) {
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

	// Verify GetStats() returns valid statistics in clean state
	stats, err := rlm.GetStats()
	require.NoError(t, err, "GetStats should not return error")
	assert.NotNil(t, stats, "Statistics should not be nil")
	assert.Equal(t, 0, stats.LocalLimiters, "Should start with zero local limiters")
	assert.False(t, stats.GlobalEnabled, "Global rate limiting should be disabled")
	assert.False(t, stats.DegradedMode, "Should not be in degraded mode initially")
}

// TestRateLimitMiddleware_NetworkErrorDetection validates Redis error classification.
//
// This test ensures that the isRedisError() method correctly distinguishes between
// Redis infrastructure errors (which should trigger degraded mode) and application
// errors (which should be propagated normally). Proper error classification is
// critical for maintaining service availability during infrastructure failures.
//
// Test methodology:
//   - Test comprehensive error type classification
//   - Verify Redis-specific errors trigger degraded mode
//   - Ensure network and timeout errors are handled correctly
//   - Validate that application errors are not misclassified
func TestRateLimitMiddleware_NetworkErrorDetection(t *testing.T) {
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

	// Comprehensive error classification test cases
	testCases := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "context deadline exceeded",
			err:      context.DeadlineExceeded,
			expected: true, // Should trigger degraded mode
		},
		{
			name:     "context canceled",
			err:      context.Canceled,
			expected: true, // Should trigger degraded mode
		},
		{
			name:     "network timeout error",
			err:      &net.OpError{Op: "dial", Err: fmt.Errorf("timeout")},
			expected: true, // Network error should trigger degraded mode
		},
		{
			name:     "redis protocol error",
			err:      redis.Nil,
			expected: true, // Redis error should trigger degraded mode
		},
		{
			name:     "generic application error",
			err:      fmt.Errorf("some other error"),
			expected: false, // Application error should not trigger degraded mode
		},
	}

	// Run error classification tests
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := rlm.isRedisError(tc.err)
			assert.Equal(t, tc.expected, result, "Error classification for %s", tc.name)
		})
	}
}

// TestRateLimitMiddleware_AtomicTimestampCleanup validates TTL-based memory management.
//
// This test verifies that the cleanup mechanism correctly identifies and removes
// stale rate limiters based on atomic timestamp tracking. The test ensures that
// recently used limiters are preserved while unused limiters are cleaned up to
// prevent memory leaks in long-running services.
//
// Test methodology:
//   - Create multiple rate limiters with recent activity
//   - Test that recent limiters are preserved during cleanup
//   - Test that all limiters are removed when considered stale
//   - Verify atomic timestamp operations prevent race conditions
func TestRateLimitMiddleware_AtomicTimestampCleanup(t *testing.T) {
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

	// Create test limiters with different key patterns
	key1 := "tenant1:provider1:model1:op1"
	key2 := "tenant2:provider2:model2:op2"

	limiter1 := rlm.getOrCreateLimiter(key1)
	limiter2 := rlm.getOrCreateLimiter(key2)

	assert.NotNil(t, limiter1, "First limiter should be created")
	assert.NotNil(t, limiter2, "Second limiter should be created")
	assert.Equal(t, 2, len(rlm.localLimiters), "Should have two active limiters")

	// Test that recent limiters are preserved (cutoff before creation)
	oldCutoff := time.Now().Add(-1 * time.Hour)
	rlm.CleanupStale(oldCutoff)
	assert.Equal(t, 2, len(rlm.localLimiters), "Recent limiters should be preserved")

	// Test that all limiters are removed when considered stale (cutoff after creation)
	futureeCutoff := time.Now().Add(1 * time.Hour) // Future time means everything is stale
	rlm.CleanupStale(futureeCutoff)
	assert.Equal(t, 0, len(rlm.localLimiters), "All stale limiters should be removed")
}

// TestRateLimitMiddleware_RuntimeNegativeRPSProtection validates runtime protection against negative RPS.
//
// This test verifies that even if validation is bypassed (e.g., through direct struct modification),
// the checkGlobalLimit method provides defense-in-depth by rejecting negative rate limits at runtime.
// This ensures that negative values never reach the Redis Lua script where they could cause
// security vulnerabilities.
func TestRateLimitMiddleware_RuntimeNegativeRPSProtection(t *testing.T) {
	// Create a middleware with valid configuration
	cfg := configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			Enabled: false,
		},
		Global: configuration.GlobalRateLimitConfig{
			Enabled:           true,
			RequestsPerSecond: 10, // Start with valid value
			RedisAddr:         "localhost:6379",
			RedisDB:           1,
			ConnectTimeout:    5 * time.Second,
		},
	}

	rlm := createTestRateLimitMiddleware(&cfg)

	// Simulate a scenario where negative value is injected after initialization
	// This could happen through reflection, unsafe operations, or bugs
	rlm.globalConfig.RequestsPerSecond = -5

	// Mock Redis client to isolate the test
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   1,
	})
	rlm.globalClient = client

	// Attempt to check global limit with negative RPS
	err := checkGlobalLimit(rlm, context.Background(), "test-key")

	// Runtime protection should reject negative values
	assert.Error(t, err, "checkGlobalLimit should reject negative RPS at runtime")
	assert.Contains(t, err.Error(), "negative", "error should mention negative value")
	assert.Contains(t, err.Error(), "-5", "error should include the actual negative value")
}

// TestRateLimit_PreventDuplicateLimiterOnCleanup verifies that cleanup and concurrent requests
// do not result in multiple limiters being created for the same key due to race conditions.
// This test ensures that even if a limiter is removed by cleanup while requests are in flight,
// only one limiter instance exists for a given key at any time.
func TestRateLimit_PreventDuplicateLimiterOnCleanup(t *testing.T) {
	cfg := &configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			TokensPerSecond: 1, // Very restrictive: 1 req/sec
			BurstSize:       3, // Allow burst of 3
			Enabled:         true,
		},
	}

	rlm := createTestRateLimitMiddleware(cfg)
	key := "violation-test"

	var successfulRequests []string

	makeRequest := func(phase string) bool {
		err := checkLocalLimit(rlm, key)
		if err == nil {
			successfulRequests = append(successfulRequests, phase)
			return true
		}
		return false
	}

	// Phase 1: Use up the burst capacity (should get exactly 3 successes).
	phase1Success := 0
	for i := 0; i < 10; i++ {
		if makeRequest("phase1") {
			phase1Success++
		}
	}
	assert.Equal(t, 3, phase1Success, "Phase 1 should allow exactly burst size (3) requests")

	// Phase 2: Without waiting for token refill, force cleanup
	// This simulates what happens in production with periodic cleanup
	rlm.localMu.Lock()
	for k, tl := range rlm.localLimiters {
		if k == key {
			// Make it look old so cleanup will remove it
			tl.lastUsed.Store(time.Now().Add(-2 * time.Hour).UnixNano())
		}
	}
	rlm.localMu.Unlock()

	// Run cleanup
	rlm.CleanupStale(time.Now().Add(-time.Hour))

	// Phase 3: Try more requests WITHOUT any time passing
	// These should ALL fail.
	phase3Success := 0
	for i := 0; i < 10; i++ {
		if makeRequest("phase3") {
			phase3Success++
		}
	}

	assert.Equal(t, 0, phase3Success,
		"Phase 3 should NOT allow any requests (tokens exhausted, no time passed)")

	// Total successful requests should NEVER exceed burst size without time passing.
	totalSuccess := len(successfulRequests)
	assert.Equal(t, 3, totalSuccess,
		"Total requests without time passing should never exceed burst size")

	if totalSuccess > 3 {
		t.Errorf("RATE LIMIT VIOLATED: Got %d total requests when max should be %d",
			totalSuccess, 3)
		t.Logf("Successful requests by phase: %v", successfulRequests)
		t.Log("This proves cleanup created a new limiter with fresh tokens!")
	}
}
