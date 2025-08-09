//go:build integration
// +build integration

// Package resilience_test provides comprehensive integration and edge case tests
// for the rate limiting middleware.
//
// This test suite focuses on validating the behavior of the rate limiter
// under various configurations and conditions, including local and global
// (Redis-backed) limiting, using only the public API of the resilience package.
package ratelimit_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/configuration"
	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/ratelimit"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// TestNewRateLimitMiddlewareWithRedis_Comprehensive validates the creation of the
// rate limit middleware with a variety of configurations.
// It covers edge cases such as local-only setups, global configurations with
// an external Redis client, and boundary conditions like zero tokens per second.
func TestNewRateLimitMiddlewareWithRedis_Comprehensive(t *testing.T) {
	tests := []struct {
		name        string
		cfg         *configuration.RateLimitConfig
		client      *redis.Client
		wantErr     bool
		errContains string
	}{
		{
			name: "valid local-only configuration",
			cfg: &configuration.RateLimitConfig{
				Local: configuration.LocalRateLimitConfig{
					TokensPerSecond: 10.5, // Test fractional token rates
					BurstSize:       20,   // Test burst capacity handling
					Enabled:         true,
				},
				Global: configuration.GlobalRateLimitConfig{
					Enabled: false, // Test local-only middleware creation
				},
			},
			wantErr: false,
		},
		{
			name: "valid global configuration with provided client",
			cfg: &configuration.RateLimitConfig{
				Local: configuration.LocalRateLimitConfig{
					TokensPerSecond: 5.0,
					BurstSize:       10,
					Enabled:         true,
				},
				Global: configuration.GlobalRateLimitConfig{
					Enabled:           true,
					RequestsPerSecond: 100,
					RedisAddr:         "localhost:6379",
					RedisDB:           0,
					ConnectTimeout:    5 * time.Second,
				},
			},
			client: redis.NewClient(&redis.Options{
				Addr: "localhost:6379",
			}),
			wantErr: false,
		},
		{
			name: "zero tokens per second (boundary condition)",
			cfg: &configuration.RateLimitConfig{
				Local: configuration.LocalRateLimitConfig{
					TokensPerSecond: 0,
					BurstSize:       1,
					Enabled:         true,
				},
				Global: configuration.GlobalRateLimitConfig{
					Enabled: false,
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			middleware, err := ratelimit.NewRateLimitMiddlewareWithRedis(tt.cfg, tt.client)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, middleware, "middleware should not be nil")
		})
	}
}

// TestRateLimitMiddleware_Integration_Comprehensive verifies the core behavior of
// the local rate limiter in an integration-style test.
// It ensures that the middleware correctly allows an initial request and then
// blocks a subsequent request that exceeds the configured rate, returning the
// appropriate error type.
func TestRateLimitMiddleware_Integration_Comprehensive(t *testing.T) {
	cfg := &configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			TokensPerSecond: 2, // Low rate for testing
			BurstSize:       1,
			Enabled:         true,
		},
		Global: configuration.GlobalRateLimitConfig{
			Enabled: false,
		},
	}

	middleware, err := ratelimit.NewRateLimitMiddlewareWithRedis(cfg, nil)
	require.NoError(t, err)

	// Mock handler that always succeeds
	mockHandler := transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
		return &transport.Response{
			Content:      "success",
			FinishReason: domain.FinishStop,
		}, nil
	})

	wrappedHandler := middleware(mockHandler)

	req := &transport.Request{
		TenantID:  "test-tenant",
		Provider:  "test-provider",
		Model:     "test-model",
		Operation: transport.OpGeneration,
	}

	// First request should succeed
	resp, err := wrappedHandler.Handle(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)

	// Second request should be rate limited
	resp, err = wrappedHandler.Handle(context.Background(), req)
	assert.Error(t, err)
	assert.Nil(t, resp)

	var rateLimitErr *llmerrors.RateLimitError
	assert.True(t, errors.As(err, &rateLimitErr))
	assert.Equal(t, "local", rateLimitErr.Provider)
}

// TestRateLimitMiddleware_DifferentOperations_Comprehensive ensures that the rate
// limiter correctly handles requests for different operation types.
// This test confirms that the limiter's logic is applied independently of the
// specific operation (e.g., generation vs. scoring) being performed.
func TestRateLimitMiddleware_DifferentOperations_Comprehensive(t *testing.T) {
	cfg := &configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			TokensPerSecond: 10,
			BurstSize:       5,
			Enabled:         true,
		},
	}

	middleware, err := ratelimit.NewRateLimitMiddlewareWithRedis(cfg, nil)
	require.NoError(t, err)

	mockHandler := transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
		return &transport.Response{
			Content: "response",
		}, nil
	})

	wrappedHandler := middleware(mockHandler)

	operations := []transport.OperationType{
		transport.OpGeneration,
		transport.OpScoring,
	}

	for _, op := range operations {
		t.Run(string(op), func(t *testing.T) {
			req := &transport.Request{
				TenantID:  "test-tenant",
				Provider:  "test-provider",
				Model:     "test-model",
				Operation: op,
			}

			// Should work fine with different operations
			resp, err := wrappedHandler.Handle(context.Background(), req)
			assert.NoError(t, err)
			assert.NotNil(t, resp)
		})
	}
}

// TestRateLimitMiddleware_GlobalWithRedis_Comprehensive tests the behavior of the
// global rate limiter when integrated with a Redis instance.
// It verifies that after a certain number of requests, the global rate limit
// is triggered, and subsequent requests are correctly blocked. The test skips
// if Redis is not available.
func TestRateLimitMiddleware_GlobalWithRedis_Comprehensive(t *testing.T) {
	// Skip if Redis is not available
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   1, // Use test database
	})
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skip("Redis not available for testing")
	}

	cfg := &configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			TokensPerSecond: 100,
			BurstSize:       10,
			Enabled:         true,
		},
		Global: configuration.GlobalRateLimitConfig{
			Enabled:           true,
			RequestsPerSecond: 5, // Low rate for testing
			RedisAddr:         "localhost:6379",
			RedisDB:           1,
		},
	}

	middleware, err := ratelimit.NewRateLimitMiddlewareWithRedis(cfg, client)
	require.NoError(t, err)

	mockHandler := transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
		return &transport.Response{
			Content: "response",
		}, nil
	})

	wrappedHandler := middleware(mockHandler)

	req := &transport.Request{
		TenantID:  "test-tenant",
		Provider:  "test-provider",
		Model:     "test-model",
		Operation: transport.OpGeneration,
	}

	// Execute several requests - should eventually hit global limit
	successCount := 0
	errorCount := 0

	for i := 0; i < 10; i++ {
		_, err := wrappedHandler.Handle(context.Background(), req)
		if err != nil {
			errorCount++
			var rateLimitErr *llmerrors.RateLimitError
			assert.True(t, errors.As(err, &rateLimitErr))
		} else {
			successCount++
		}
	}

	// Should have some rate limiting
	assert.Greater(t, errorCount, 0, "Should encounter some rate limiting")
	assert.Greater(t, successCount, 0, "Should have some successful requests")
}

// Test_GlobalFailure_WithLocalDisabled_FailsOpen validates that the middleware
// fails closed (applies fallback rate limiting) when Redis is unavailable and
// local limiting is disabled, preventing the security vulnerability of unlimited
// throughput during degraded operation.
//
// This test verifies the fix for the fail-open security bug where the middleware
// would allow unlimited requests when both Redis failed and local limiting was disabled.
func Test_GlobalFailure_WithLocalDisabled_FailsOpen(t *testing.T) {
	cfg := &configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			Enabled:         false, // <- critical: local disabled
			TokensPerSecond: 0,
			BurstSize:       0,
		},
		Global: configuration.GlobalRateLimitConfig{
			Enabled:           true,
			RequestsPerSecond: 5,
			RedisAddr:         "localhost:1",         // invalid address
			ConnectTimeout:    50 * time.Millisecond, // fast fail
		},
	}

	mw, err := ratelimit.NewRateLimitMiddlewareWithRedis(cfg, nil)
	require.NoError(t, err)

	// Handler always succeeds; we want the middleware to stop us if Redis is down
	mock := transport.HandlerFunc(func(ctx context.Context, r *transport.Request) (*transport.Response, error) {
		return &transport.Response{}, nil
	})
	h := mw(mock)

	req := &transport.Request{TenantID: "t", Provider: "p", Model: "m", Operation: transport.OpGeneration}

	// With Redis broken and local disabled, this should now apply fallback limiting
	// and eventually start rate limiting instead of allowing unlimited requests
	successCount := 0
	errorCount := 0

	for i := 0; i < 20; i++ { // Try more requests to trigger fallback rate limiting
		resp, err := h.Handle(context.Background(), req)
		if err != nil {
			errorCount++
			// Verify it's a rate limit error from the fallback limiter
			var rateLimitErr *llmerrors.RateLimitError
			assert.True(t, errors.As(err, &rateLimitErr))
			assert.Equal(t, "fallback", rateLimitErr.Provider)
			assert.Equal(t, 10, rateLimitErr.Limit) // DefaultRateLimit = 10
			continue
		}
		assert.NotNil(t, resp, "unexpected nil response")
		successCount++
	}

	// The fix should prevent fail-open: we should see both successes and rate limiting
	assert.Greater(t, successCount, 0, "Should allow some requests initially")
	assert.Greater(t, errorCount, 0, "Should start rate limiting with fallback limiter")

	// Most importantly: we should NOT have 20 successes (which would indicate fail-open)
	assert.Less(t, successCount, 20, "Should not allow unlimited requests (fail-open)")
}
