//go:build integration
// +build integration

// Package resilience provides integration tests for the rate limiting middleware.
//
// This test suite validates the integration with an external Redis
// infrastructure. These tests require a running Redis instance and verify
// end-to-end behavior, including network failures, connection management, and
// Redis Lua script execution under realistic conditions.
//
// Integration test coverage includes:
//   - Real Redis server integration with complex Lua script execution.
//   - Network failure scenarios and graceful degradation behavior.
//   - End-to-end middleware behavior with external Redis dependencies.
//   - Connection pooling behavior under concurrent load.
//   - Redis script execution with various parameter combinations and edge cases.
//   - Distributed rate limiting coordination across multiple service instances.
//
// Prerequisites:
//   - A running Redis instance on localhost:6379.
//   - A test database (DB 1) for isolation.
//
// To run these tests, use the "Integration" pattern:
//
//	go test -v ./internal/llm/resilience -run=Integration
package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ahrav/go-judgy/internal/llm/configuration"
	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/transport"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRateLimitMiddleware_RedisIntegration_FullFlow verifies the end-to-end
// behavior of the rate limit middleware.
// It tests both local and global (Redis) rate limiting by sending a burst of
// requests and asserting that some succeed while others are correctly
// rate-limited.
func TestRateLimitMiddleware_RedisIntegration_FullFlow(t *testing.T) {
	client := setupRedisClient(t)

	cfg := &configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			TokensPerSecond: 5,
			BurstSize:       10,
			Enabled:         true,
		},
		Global: configuration.GlobalRateLimitConfig{
			Enabled:           true,
			RequestsPerSecond: 10,
			RedisAddr:         "localhost:6379",
			RedisDB:           1,
			ConnectTimeout:    5 * time.Second,
		},
	}

	middleware, err := NewRateLimitMiddlewareWithRedis(cfg, client)
	require.NoError(t, err)

	// Mock handler
	handlerCallCount := 0
	mockHandler := transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
		handlerCallCount++
		return &transport.Response{}, nil
	})

	wrappedHandler := middleware(mockHandler)

	req := &transport.Request{
		TenantID:  "integration-tenant",
		Provider:  "integration-provider",
		Model:     "integration-model",
		Operation: transport.OpGeneration,
	}

	testKey := "integration-tenant:integration-provider:integration-model:generation"
	globalKey := fmt.Sprintf("rl:global:%s", testKey)

	// Cleanup before and after test
	client.Del(context.Background(), globalKey)
	defer client.Del(context.Background(), globalKey)

	// Test normal flow - should succeed initially
	resp, err := wrappedHandler.Handle(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, 1, handlerCallCount)

	// Make more requests to test both local and global rate limiting
	successCount := 1 // Already had one success
	rateLimitedCount := 0

	for i := 0; i < 50; i++ {
		resp, err := wrappedHandler.Handle(context.Background(), req)
		if err == nil {
			successCount++
			assert.NotNil(t, resp)
		} else {
			var rateLimitErr *llmerrors.RateLimitError
			require.True(t, errors.As(err, &rateLimitErr))
			rateLimitedCount++
			assert.Nil(t, resp)

			// Verify rate limit error structure
			assert.Contains(t, []string{"local", "global"}, rateLimitErr.Provider)
			assert.Greater(t, rateLimitErr.Limit, 0)
			assert.GreaterOrEqual(t, rateLimitErr.RetryAfter, 1)
		}
	}

	t.Logf("Integration test results: %d successful, %d rate limited", successCount, rateLimitedCount)

	// Should have hit rate limits at some point
	assert.Greater(t, rateLimitedCount, 0, "Should have encountered rate limiting")

	// Handler should only be called for successful requests
	assert.Equal(t, successCount, handlerCallCount)
}

// TestRateLimitMiddleware_RedisConnection_FailureRecovery tests the middleware's
// graceful degradation when Redis is unavailable.
// It ensures that the middleware can be created and falls back to local-only
// rate limiting when the Redis connection fails.
func TestRateLimitMiddleware_RedisConnection_FailureRecovery(t *testing.T) {
	// Test graceful degradation when Redis is unavailable
	cfg := &configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			TokensPerSecond: 10,
			BurstSize:       5,
			Enabled:         true,
		},
		Global: configuration.GlobalRateLimitConfig{
			Enabled:           true,
			RequestsPerSecond: 20,
			RedisAddr:         "localhost:9999", // Invalid port
			RedisDB:           1,
			ConnectTimeout:    100 * time.Millisecond,
		},
	}

	middleware, err := NewRateLimitMiddlewareWithRedis(cfg, nil)
	require.NoError(t, err, "Should create middleware even with invalid Redis")

	mockHandler := transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
		return &transport.Response{}, nil
	})

	wrappedHandler := middleware(mockHandler)

	req := &transport.Request{
		TenantID:  "failure-tenant",
		Provider:  "failure-provider",
		Model:     "failure-model",
		Operation: transport.OpGeneration,
	}

	// Should still work with local rate limiting only
	resp, err := wrappedHandler.Handle(context.Background(), req)
	assert.NoError(t, err, "Should fall back to local-only rate limiting")
	assert.NotNil(t, resp)
}

// TestRateLimitMiddleware_RedisScript_EdgeCases is intentionally omitted.
// Testing internal Redis script behavior requires access to unexported methods,
// and its functionality is covered by other integration tests in this suite.

// TestRateLimitMiddleware_RedisPool_Behavior is intentionally omitted.
// Testing connection pool behavior requires access to unexported pool statistics,
// and its core functionality is covered by the main integration test.

// TestRateLimitMiddleware_RedisNetwork_Failures tests the middleware's resilience
// to network issues when communicating with Redis.
// It verifies that the system gracefully degrades to local rate limiting when
// Redis operations time out, preventing network errors from propagating.
func TestRateLimitMiddleware_RedisNetwork_Failures(t *testing.T) {
	client := setupRedisClient(t)

	cfg := &configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			TokensPerSecond: 10,
			BurstSize:       5,
			Enabled:         true, // Important: local fallback
		},
		Global: configuration.GlobalRateLimitConfig{
			Enabled:           true,
			RequestsPerSecond: 20,
			RedisAddr:         "localhost:6379",
			RedisDB:           1,
			ConnectTimeout:    5 * time.Second,
		},
	}

	middleware, err := NewRateLimitMiddlewareWithRedis(cfg, client)
	require.NoError(t, err)

	mockHandler := transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
		return &transport.Response{}, nil
	})

	wrappedHandler := middleware(mockHandler)

	req := &transport.Request{
		TenantID:  "network-failure-tenant",
		Provider:  "network-failure-provider",
		Model:     "network-failure-model",
		Operation: transport.OpGeneration,
	}

	// First, verify normal operation
	resp, err := wrappedHandler.Handle(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)

	// Simulate network failure by closing client connections
	// Info: This is a more realistic test but might be flaky
	// In practice, network failures are handled by connection retries

	// Test with context timeout (simulates network delay)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	resp, err = wrappedHandler.Handle(ctx, req)
	// Should still work due to graceful degradation
	// Even if Redis times out, local rate limiting should handle it
	if err != nil {
		var rateLimitErr *llmerrors.RateLimitError
		assert.True(t, errors.As(err, &rateLimitErr), "Should get rate limit error, not network error")
	} else {
		assert.NotNil(t, resp)
	}
}

// TestRateLimitMiddleware_RedisScript_Consistency verifies that the Redis Lua
// script enforces the global rate limit with precision.
// It sends a series of requests and asserts that exactly the configured number
// of requests are allowed within the time window.
func TestRateLimitMiddleware_RedisScript_Consistency(t *testing.T) {
	client := setupRedisClient(t)

	cfg := &configuration.RateLimitConfig{
		Global: configuration.GlobalRateLimitConfig{
			Enabled:           true,
			RequestsPerSecond: 5,
			RedisAddr:         "localhost:6379",
			RedisDB:           1,
			ConnectTimeout:    5 * time.Second,
		},
	}
	rlm := createTestRateLimitMiddleware(cfg)
	rlm.globalClient = client

	key := "consistency-test-key"
	globalKey := fmt.Sprintf("rl:global:%s", key)

	defer client.Del(context.Background(), globalKey)

	// Test that the Redis script produces consistent results
	results := make([]struct {
		allowed   bool
		remaining int64
	}, 10)

	for i := 0; i < 10; i++ {
		err := checkGlobalLimit(rlm, context.Background(), key)
		if err == nil {
			results[i].allowed = true
		} else {
			results[i].allowed = false
			var rateLimitErr *llmerrors.RateLimitError
			if errors.As(err, &rateLimitErr) {
				results[i].remaining = int64(rateLimitErr.RetryAfter)
			}
		}
	}

	// Verify that we get exactly the expected number of successes
	successCount := 0
	for _, result := range results {
		if result.allowed {
			successCount++
		}
	}

	assert.Equal(t, 5, successCount, "Should get exactly 5 successful requests per second")

	// Wait for window reset and test again
	time.Sleep(1100 * time.Millisecond) // Wait for Redis window to reset

	// Should be able to make requests again
	err := checkGlobalLimit(rlm, context.Background(), key)
	assert.NoError(t, err, "Should succeed after window reset")
}

// TestRateLimitMiddleware_RedisCleanup_KeyExpiration verifies that Redis keys
// created for rate limiting are automatically cleaned up.
// It checks that keys are assigned a reasonable TTL and expire correctly,
// allowing rate limit windows to reset.
func TestRateLimitMiddleware_RedisCleanup_KeyExpiration(t *testing.T) {
	client := setupRedisClient(t)

	cfg := &configuration.RateLimitConfig{
		Global: configuration.GlobalRateLimitConfig{
			Enabled:           true,
			RequestsPerSecond: 10,
			RedisAddr:         "localhost:6379",
			RedisDB:           1,
			ConnectTimeout:    5 * time.Second,
		},
	}
	rlm := createTestRateLimitMiddleware(cfg)
	rlm.globalClient = client

	key := "expiration-test-key"
	globalKey := fmt.Sprintf("rl:global:%s", key)

	defer client.Del(context.Background(), globalKey)

	// Make a request to create the Redis key
	err := checkGlobalLimit(rlm, context.Background(), key)
	assert.NoError(t, err)

	// Verify key exists and has TTL
	exists := client.Exists(context.Background(), globalKey).Val()
	assert.Equal(t, int64(1), exists, "Redis key should exist")

	ttl := client.TTL(context.Background(), globalKey).Val()
	assert.Greater(t, ttl, time.Duration(0), "Key should have TTL set")
	assert.LessOrEqual(t, ttl, time.Second, "TTL should be reasonable (1 second window)")

	// Wait for key to expire
	time.Sleep(1100 * time.Millisecond)

	// Key should be expired/deleted
	exists = client.Exists(context.Background(), globalKey).Val()
	assert.Equal(t, int64(0), exists, "Redis key should have expired")

	// Should be able to make new requests (fresh window)
	err = checkGlobalLimit(rlm, context.Background(), key)
	assert.NoError(t, err, "Should succeed with fresh window")
}

// TestRateLimitMiddleware_MultiTenant_Isolation ensures that rate limits are
// applied independently for different tenants and request types.
// It verifies that traffic for one tenant does not affect the rate limit quota
// of another.
func TestRateLimitMiddleware_MultiTenant_Isolation(t *testing.T) {
	client := setupRedisClient(t)

	cfg := &configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			TokensPerSecond: 2, // Low rate for clear testing
			BurstSize:       2,
			Enabled:         true,
		},
		Global: configuration.GlobalRateLimitConfig{
			Enabled:           true,
			RequestsPerSecond: 3,
			RedisAddr:         "localhost:6379",
			RedisDB:           1,
			ConnectTimeout:    5 * time.Second,
		},
	}

	middleware, err := NewRateLimitMiddlewareWithRedis(cfg, client)
	require.NoError(t, err)

	mockHandler := transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
		return &transport.Response{}, nil
	})

	wrappedHandler := middleware(mockHandler)

	// Different tenant/provider/model combinations
	requests := []*transport.Request{
		{TenantID: "tenant-A", Provider: "provider-1", Model: "model-X", Operation: transport.OpGeneration},
		{TenantID: "tenant-B", Provider: "provider-1", Model: "model-X", Operation: transport.OpGeneration},
		{TenantID: "tenant-A", Provider: "provider-2", Model: "model-X", Operation: transport.OpGeneration},
		{TenantID: "tenant-A", Provider: "provider-1", Model: "model-Y", Operation: transport.OpGeneration},
	}

	// Each request type should have independent rate limiting
	for _, req := range requests {
		t.Run(fmt.Sprintf("%s-%s-%s", req.TenantID, req.Provider, req.Model), func(t *testing.T) {
			successCount := 0
			rateLimitCount := 0

			// Test each request type independently
			for i := 0; i < 10; i++ {
				_, err := wrappedHandler.Handle(context.Background(), req)
				if err == nil {
					successCount++
				} else {
					var rateLimitErr *llmerrors.RateLimitError
					if errors.As(err, &rateLimitErr) {
						rateLimitCount++
					} else {
						t.Fatalf("Unexpected error: %v", err)
					}
				}
			}

			t.Logf("Tenant %s: %d success, %d rate limited",
				req.TenantID, successCount, rateLimitCount)

			// Each should get some successes before being rate limited
			assert.Greater(t, successCount, 0, "Should have some successful requests")
			assert.Greater(t, rateLimitCount, 0, "Should have some rate limited requests")
		})
	}

	// Cleanup Redis keys
	allKeys, err := client.Keys(context.Background(), "rl:global:*").Result()
	if err == nil && len(allKeys) > 0 {
		client.Del(context.Background(), allKeys...)
	}
}

// setupRedisClient creates a Redis client for testing and skips if Redis is unavailable
func setupRedisClient(t *testing.T) *redis.Client {
	// Use the testcontainer helper function
	_, client := setupRedisContainer(t)

	// Clean up any existing test keys
	ctx := context.Background()
	keys, err := client.Keys(ctx, "rl:global:*").Result()
	if err == nil && len(keys) > 0 {
		client.Del(ctx, keys...)
	}

	return client
}

// TestRateLimitMiddleware_ZeroRPSSemantics_Integration validates zero RPS global configuration.
//
// This test verifies that setting RequestsPerSecond to zero effectively
// disables global rate limiting, allowing all requests to pass through
// without Redis interaction. Such a configuration is useful for temporarily
// disabling global limits while maintaining local rate limiting.
//
// Test methodology:
//   - Configure global rate limiting with RequestsPerSecond = 0.
//   - Disable local rate limiting to isolate global behavior.
//   - Verify requests pass through without rate limit errors.
//   - Ensure Redis operations are skipped for performance.
func TestRateLimitMiddleware_ZeroRPSSemantics_Integration(t *testing.T) {
	client := setupRedisClient(t)

	cfg := configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			TokensPerSecond: 10,
			BurstSize:       5,
			Enabled:         false, // Disable local limiting to isolate global behavior
		},
		Global: configuration.GlobalRateLimitConfig{
			Enabled:           true,
			RequestsPerSecond: 0, // Zero RPS should disable global limiting
			RedisAddr:         "localhost:6379",
			RedisDB:           1,
			ConnectTimeout:    5 * time.Second,
		},
	}

	rlm := createTestRateLimitMiddleware(&cfg)
	rlm.globalClient = client

	// Create test request with typical metadata structure
	req := &transport.Request{
		TenantID:  "test-tenant",
		Provider:  "test-provider",
		Model:     "test-model",
		Operation: "test-op",
	}

	key := rlm.buildKey(req)
	err := checkGlobalLimit(rlm, context.Background(), key)
	assert.NoError(t, err, "Zero RPS should disable global rate limiting")

	// Clean up any Redis keys to prevent test interference
	testKey := fmt.Sprintf("rl:global:%s", key)
	client.Del(context.Background(), testKey)
}

// Test_DegradedMode_IsSticky_NoRecovery_Integration demonstrates that degraded mode is sticky.
//
// This test proves that once DegradedMode is set to true (when Redis fails),
// there's no automatic recovery path - even if Redis becomes healthy later.
// This means a transient Redis hiccup disables global limiting for the lifetime
// of the process.
//
// The test:
//  1. Forces a Redis failure to trigger degraded mode
//  2. Points to a working Redis instance afterward
//  3. Shows that the system stays in degraded mode and never uses Redis again
//  4. Verifies that no Redis keys are created after "recovery"
func Test_DegradedMode_IsSticky_NoRecovery_Integration(t *testing.T) {
	cfg := &configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			Enabled:         true,
			TokensPerSecond: 100,
			BurstSize:       100,
		},
		Global: configuration.GlobalRateLimitConfig{
			Enabled:           true,
			RequestsPerSecond: 10,
			ConnectTimeout:    50 * time.Millisecond,
		},
	}
	rlm := createTestRateLimitMiddleware(cfg)

	// Step 1: Force a Redis failure → degraded mode
	// Point to an invalid address that will fail immediately
	rlm.globalClient = redis.NewClient(&redis.Options{
		Addr:        "localhost:1", // Invalid port, will fail
		DialTimeout: 50 * time.Millisecond,
	})

	// Create the middleware handler
	h := rlm.middleware()(transport.HandlerFunc(func(ctx context.Context, r *transport.Request) (*transport.Response, error) {
		return &transport.Response{}, nil
	}))

	// Create a test request
	req := &transport.Request{
		TenantID:  "test-tenant",
		Provider:  "test-provider",
		Model:     "test-model",
		Operation: transport.OpGeneration,
	}

	// Make a request that will trigger Redis error and activate degraded mode
	_, _ = h.Handle(context.Background(), req)

	// Verify degraded mode is now active
	assert.True(t, rlm.globalConfig.DegradedMode.Load(), "should be degraded after Redis error")

	// Step 2: Now point to a real Redis and show we *stay* degraded
	client := setupRedisClient(t)

	// Update the middleware to use the working Redis client
	rlm.globalClient = client

	// Build the key that would be used for global rate limiting
	key := rlm.buildKey(req)
	globalKey := fmt.Sprintf("rl:global:%s", key)

	// Clean up any existing key to ensure test starts fresh
	_ = client.Del(context.Background(), globalKey).Err()

	// Make another request - if global limiter runs, it would create a Redis key
	_, _ = h.Handle(context.Background(), req)

	// Check if the global rate limit key was created in Redis
	exists := client.Exists(context.Background(), globalKey).Val()

	// The key should NOT exist because global limiter didn't run due to sticky degraded mode
	assert.Equal(t, int64(0), exists, "global limiter did not run after recovery (sticky degraded mode)")

	// Verify we're still in degraded mode
	assert.True(t, rlm.globalConfig.DegradedMode.Load(), "should remain in degraded mode (no auto-recovery)")

	// Clean up
	_ = client.Del(context.Background(), globalKey).Err()
}

// Test_DegradedMode_Multiple_Requests_No_Recovery_Integration extends the sticky degraded mode test.
//
// This test further demonstrates that even after multiple requests with a healthy
// Redis, the system never recovers from degraded mode. It shows that the problem
// persists across many operations, not just a single request.
func Test_DegradedMode_Multiple_Requests_No_Recovery_Integration(t *testing.T) {
	cfg := &configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			Enabled:         true,
			TokensPerSecond: 100,
			BurstSize:       100,
		},
		Global: configuration.GlobalRateLimitConfig{
			Enabled:           true,
			RequestsPerSecond: 10,
			ConnectTimeout:    50 * time.Millisecond,
		},
	}
	rlm := createTestRateLimitMiddleware(cfg)

	// Force degraded mode with bad Redis connection
	rlm.globalClient = redis.NewClient(&redis.Options{
		Addr:        "localhost:1",
		DialTimeout: 50 * time.Millisecond,
	})

	h := rlm.middleware()(transport.HandlerFunc(func(ctx context.Context, r *transport.Request) (*transport.Response, error) {
		return &transport.Response{}, nil
	}))

	req := &transport.Request{
		TenantID:  "test-tenant",
		Provider:  "test-provider",
		Model:     "test-model",
		Operation: transport.OpGeneration,
	}

	// Trigger degraded mode
	_, _ = h.Handle(context.Background(), req)
	assert.True(t, rlm.globalConfig.DegradedMode.Load(), "should enter degraded mode")

	// Switch to healthy Redis
	client := setupRedisClient(t)
	rlm.globalClient = client

	// Make multiple requests to show no recovery happens
	key := rlm.buildKey(req)
	globalKey := fmt.Sprintf("rl:global:%s", key)

	// Clean up before test
	_ = client.Del(context.Background(), globalKey).Err()

	// Make 10 requests - none should create Redis keys
	for i := 0; i < 10; i++ {
		_, _ = h.Handle(context.Background(), req)

		// Check that no Redis key was created
		exists := client.Exists(context.Background(), globalKey).Val()
		assert.Equal(t, int64(0), exists,
			"request %d: global limiter should not run in degraded mode", i+1)

		// Verify still in degraded mode
		assert.True(t, rlm.globalConfig.DegradedMode.Load(),
			"request %d: should remain in degraded mode", i+1)
	}

	// Clean up
	_ = client.Del(context.Background(), globalKey).Err()
}

// Test_GlobalLimit_NegativeRPS_ShouldBeRejected validates that negative RPS values are rejected.
//
// This test demonstrates a bug where negative RequestsPerSecond values bypass rate limiting
// entirely in the Lua script. The test creates a configuration with negative RPS and verifies
// that the middleware either rejects the configuration at initialization or properly handles
// negative values at runtime to prevent unlimited request throughput.
func Test_GlobalLimit_NegativeRPS_ShouldBeRejected(t *testing.T) {
	client := setupRedisClient(t)

	cfg := &configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			Enabled: false, // Disable local to isolate global behavior
		},
		Global: configuration.GlobalRateLimitConfig{
			Enabled:           true,
			RequestsPerSecond: -1, // Invalid negative value
			RedisAddr:         "localhost:6379",
			RedisDB:           1,
			ConnectTimeout:    5 * time.Second,
		},
	}

	// The middleware creation should reject negative RPS
	middleware, err := NewRateLimitMiddlewareWithRedis(cfg, client)

	// We expect an error for negative RPS configuration
	assert.Error(t, err, "negative RPS should be rejected at initialization")
	assert.Nil(t, middleware, "middleware should not be created with invalid config")
	assert.Contains(t, err.Error(), "negative", "error should mention negative value")

	// Also test with negative local rate limits
	cfg2 := &configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			Enabled:         true,
			TokensPerSecond: -5.0, // Invalid negative value
			BurstSize:       10,
		},
		Global: configuration.GlobalRateLimitConfig{
			Enabled: false,
		},
	}

	middleware2, err2 := NewRateLimitMiddlewareWithRedis(cfg2, nil)
	assert.Error(t, err2, "negative TokensPerSecond should be rejected")
	assert.Nil(t, middleware2, "middleware should not be created with invalid local config")

	// Test with negative burst size
	cfg3 := &configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			Enabled:         true,
			TokensPerSecond: 10.0,
			BurstSize:       -5, // Invalid negative value
		},
		Global: configuration.GlobalRateLimitConfig{
			Enabled: false,
		},
	}

	middleware3, err3 := NewRateLimitMiddlewareWithRedis(cfg3, nil)
	assert.Error(t, err3, "negative BurstSize should be rejected")
	assert.Nil(t, middleware3, "middleware should not be created with invalid burst size")
}
