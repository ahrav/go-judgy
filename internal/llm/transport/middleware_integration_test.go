package transport_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/configuration"
	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/retry"
	"github.com/ahrav/go-judgy/internal/llm/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMiddlewareOrder_RetryRespectsRateLimit verifies that each retry attempt
// goes through rate limiting, fixing the issue where retries bypass RL/CB.
func TestMiddlewareOrder_RetryRespectsRateLimit(t *testing.T) {
	ctx := context.Background()

	// Create a failing handler that will trigger retries
	var attemptCount int32
	failingHandler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		count := atomic.AddInt32(&attemptCount, 1)
		if count <= 2 {
			// First two attempts fail with retryable error
			return nil, &llmerrors.RateLimitError{
				Provider:   "test",
				Limit:      10,
				RetryAfter: 1,
			}
		}
		// Third attempt succeeds
		return &transport.Response{
			Content:      "success",
			FinishReason: domain.FinishStop,
			Usage:        transport.NormalizedUsage{TotalTokens: 10},
		}, nil
	})

	// Create rate limit middleware that tracks calls
	var rlCalls int32
	rlMiddleware := func(next transport.Handler) transport.Handler {
		return transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
			atomic.AddInt32(&rlCalls, 1)
			return next.Handle(ctx, req)
		})
	}

	// Build attempt-level stack: RL → Core
	attemptHandler := transport.Chain(failingHandler, rlMiddleware)

	// Wrap with retry
	retryConfig := configuration.RetryConfig{
		MaxAttempts:     3,
		InitialInterval: 10 * time.Millisecond,
		Multiplier:      2.0,
		MaxInterval:     100 * time.Millisecond,
		MaxElapsedTime:  1 * time.Second,
		UseJitter:       false,
	}
	retryMiddleware, _ := retry.NewRetryMiddlewareWithConfig(retryConfig)
	handler := retryMiddleware(attemptHandler)

	req := &transport.Request{
		Operation: transport.OpGeneration,
		Provider:  "test",
		Model:     "test-model",
		Question:  "test question",
	}

	// Execute request
	resp, err := handler.Handle(ctx, req)
	require.NoError(t, err, "expected success after retries")
	assert.Equal(t, "success", resp.Content)

	// Verify rate limiting was applied to EACH attempt (not just the call)
	expectedRLCalls := int32(3) // 3 attempts, each should go through RL
	assert.Equal(t, expectedRLCalls, atomic.LoadInt32(&rlCalls), "RL should be called once per attempt")

	// Verify core handler was called 3 times
	assert.Equal(t, int32(3), atomic.LoadInt32(&attemptCount), "expected 3 attempts")
}

// TestMiddlewareOrder_ObservabilitySeesAllEvents verifies that observability
// middleware captures events from all outer middlewares (cache, CB, RL).
func TestMiddlewareOrder_ObservabilitySeesAllEvents(t *testing.T) {
	ctx := context.Background()

	// Track observability calls
	var obsEvents []string
	obsMiddleware := func(next transport.Handler) transport.Handler {
		return transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
			resp, err := next.Handle(ctx, req)
			if err != nil {
				obsEvents = append(obsEvents, "error: "+err.Error())
			} else {
				obsEvents = append(obsEvents, "success")
			}
			return resp, err
		})
	}

	// Mock cache that returns cache hit
	cacheMiddleware := func(_ transport.Handler) transport.Handler {
		return transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
			// Simulate cache hit - return without calling next
			obsEvents = append(obsEvents, "cache_hit")
			return &transport.Response{
				Content:      "cached response",
				FinishReason: domain.FinishStop,
				Usage:        transport.NormalizedUsage{TotalTokens: 5},
			}, nil
		})
	}

	// Mock circuit breaker that denies request
	cbMiddleware := func(_ transport.Handler) transport.Handler {
		return transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
			obsEvents = append(obsEvents, "cb_open")
			return nil, &llmerrors.WorkflowError{
				Type:    llmerrors.ErrorTypeRateLimit,
				Message: "circuit breaker open",
			}
		})
	}

	// Mock rate limit that denies request
	rlMiddleware := func(_ transport.Handler) transport.Handler {
		return transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
			obsEvents = append(obsEvents, "rl_denied")
			return nil, &llmerrors.RateLimitError{
				Provider:   "test",
				Limit:      1,
				RetryAfter: 60,
			}
		})
	}

	baseHandler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		return &transport.Response{Content: "base"}, nil
	})

	req := &transport.Request{
		Operation: transport.OpGeneration,
		Provider:  "test",
		Model:     "test-model",
	}

	// Test 1: Cache hit should be visible to observability
	t.Run("observability_sees_cache_hit", func(t *testing.T) {
		obsEvents = nil
		// Order: Observability → Cache → Core
		handler := transport.Chain(baseHandler, cacheMiddleware, obsMiddleware)

		_, err := handler.Handle(ctx, req)
		require.NoError(t, err, "unexpected error")

		// Observability should see only the cache hit event (cache returns early)
		expected := []string{"cache_hit"}
		assert.Equal(t, expected, obsEvents, "observability should see cache hit event")
	})

	// Test 2: Circuit breaker denial should be visible to observability
	t.Run("observability_sees_circuit_breaker", func(t *testing.T) {
		obsEvents = nil
		// Order: Observability → CB → Core
		handler := transport.Chain(baseHandler, cbMiddleware, obsMiddleware)

		_, err := handler.Handle(ctx, req)
		require.Error(t, err, "expected circuit breaker error")

		// Observability should see only the CB denial (CB returns early)
		expected := []string{"cb_open"}
		assert.Equal(t, expected, obsEvents, "observability should see circuit breaker event")
	})

	// Test 3: Rate limit denial should be visible to observability
	t.Run("observability_sees_rate_limit", func(t *testing.T) {
		obsEvents = nil
		// Order: Observability → RL → Core
		handler := transport.Chain(baseHandler, rlMiddleware, obsMiddleware)

		_, err := handler.Handle(ctx, req)
		require.Error(t, err, "expected rate limit error")

		// Observability should see only RL denial (RL returns early)
		expected := []string{"rl_denied"}
		assert.Equal(t, expected, obsEvents, "observability should see rate limit event")
	})
}

// TestMiddlewareOrder_CircuitBreakerPreventsRetryStorms verifies that
// circuit breaker at call-level prevents retry storms to failing providers.
func TestMiddlewareOrder_CircuitBreakerPreventsRetryStorms(t *testing.T) {
	ctx := context.Background()

	// Track attempt count
	var attemptCount int32
	failingHandler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		atomic.AddInt32(&attemptCount, 1)
		// Return retryable provider error so retry middleware will retry
		return nil, &llmerrors.ProviderError{
			Provider:   "test",
			StatusCode: 503,
			Message:    "provider failure",
			Type:       llmerrors.ErrorTypeProvider,
		}
	})

	// Mock circuit breaker that opens after first call
	var cbState int32 // 0 = closed, 1 = open
	cbMiddleware := func(next transport.Handler) transport.Handler {
		return transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
			if atomic.LoadInt32(&cbState) == 1 {
				// Circuit breaker is open, deny immediately
				return nil, &llmerrors.CircuitBreakerError{
					Provider: "test",
					Model:    "test-model",
					State:    "open",
				}
			}

			// First call - let it through but open CB after failure
			resp, err := next.Handle(ctx, req)
			if err != nil {
				atomic.StoreInt32(&cbState, 1) // Open circuit breaker
			}
			return resp, err
		})
	}

	// Build call-level stack: CB → [retry wrapper around attempt handler]
	retryConfig := configuration.RetryConfig{
		MaxAttempts:     3,
		InitialInterval: 10 * time.Millisecond,
		Multiplier:      2.0,
		MaxInterval:     100 * time.Millisecond,
		MaxElapsedTime:  1 * time.Second,
		UseJitter:       false,
	}
	retryMiddleware, _ := retry.NewRetryMiddlewareWithConfig(retryConfig)
	retryHandler := retryMiddleware(failingHandler)

	// Circuit breaker at call level
	handler := transport.Chain(retryHandler, cbMiddleware)

	req := &transport.Request{
		Operation: transport.OpGeneration,
		Provider:  "test",
		Model:     "test-model",
		Question:  "test question",
	}

	// First call - should fail after retries and open CB
	_, err := handler.Handle(ctx, req)
	require.Error(t, err, "expected error from failing provider")

	// Reset attempt count for second call
	firstCallAttempts := atomic.LoadInt32(&attemptCount)
	atomic.StoreInt32(&attemptCount, 0)

	// Second call - should be blocked by CB immediately
	_, err = handler.Handle(ctx, req)
	require.Error(t, err, "expected circuit breaker to block second call")

	// Verify CB prevented retry storm - second call should make 0 attempts
	secondCallAttempts := atomic.LoadInt32(&attemptCount)
	assert.Equal(t, int32(0), secondCallAttempts, "CB should prevent retries on second call")

	// Verify first call made multiple attempts (retry worked)
	assert.Equal(t, int32(3), firstCallAttempts, "expected 3 attempts on first call")
}

// TestMiddlewareOrder_FullStackIntegration verifies the complete middleware
// stack behavior with proper ordering and interaction.
func TestMiddlewareOrder_FullStackIntegration(t *testing.T) {
	ctx := context.Background()

	// Track middleware execution order
	var executionOrder []string

	// Mock middlewares that record execution
	obsMiddleware := func(next transport.Handler) transport.Handler {
		return transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
			executionOrder = append(executionOrder, "obs_start")
			resp, err := next.Handle(ctx, req)
			executionOrder = append(executionOrder, "obs_end")
			return resp, err
		})
	}

	cacheMiddleware := func(next transport.Handler) transport.Handler {
		return transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
			executionOrder = append(executionOrder, "cache_start")
			resp, err := next.Handle(ctx, req)
			executionOrder = append(executionOrder, "cache_end")
			return resp, err
		})
	}

	cbMiddleware := func(next transport.Handler) transport.Handler {
		return transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
			executionOrder = append(executionOrder, "cb_start")
			resp, err := next.Handle(ctx, req)
			executionOrder = append(executionOrder, "cb_end")
			return resp, err
		})
	}

	rlMiddleware := func(next transport.Handler) transport.Handler {
		return transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
			executionOrder = append(executionOrder, "rl_check")
			return next.Handle(ctx, req)
		})
	}

	pricingMiddleware := func(next transport.Handler) transport.Handler {
		return transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
			executionOrder = append(executionOrder, "pricing_check")
			return next.Handle(ctx, req)
		})
	}

	coreHandler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		executionOrder = append(executionOrder, "core_execution")
		return &transport.Response{
			Content:      "success",
			FinishReason: domain.FinishStop,
			Usage:        transport.NormalizedUsage{TotalTokens: 10},
		}, nil
	})

	// Build the two-stack architecture
	// Attempt level: RL → Pricing → Core
	attemptHandler := transport.Chain(coreHandler, rlMiddleware, pricingMiddleware)

	// Call level: Observability → Cache → CB → Retry(attemptHandler)
	retryConfig := configuration.RetryConfig{
		MaxAttempts:     1, // No retries for this test
		InitialInterval: 10 * time.Millisecond,
		Multiplier:      2.0,
		MaxInterval:     100 * time.Millisecond,
		MaxElapsedTime:  1 * time.Second,
		UseJitter:       false,
	}
	retryMiddleware, _ := retry.NewRetryMiddlewareWithConfig(retryConfig)
	retryHandler := retryMiddleware(attemptHandler)

	finalHandler := transport.Chain(retryHandler, obsMiddleware, cacheMiddleware, cbMiddleware)

	req := &transport.Request{
		Operation: transport.OpGeneration,
		Provider:  "test",
		Model:     "test-model",
		Question:  "test question",
	}

	// Execute request
	_, err := finalHandler.Handle(ctx, req)
	require.NoError(t, err, "unexpected error")

	// Verify execution order follows expected pattern:
	// obs_start → cache_start → cb_start → rl_check → pricing_check → core_execution → cb_end → cache_end → obs_end
	expectedOrder := []string{
		"obs_start", "cache_start", "cb_start",
		"rl_check", "pricing_check", "core_execution",
		"cb_end", "cache_end", "obs_end",
	}

	assert.Equal(t, expectedOrder, executionOrder, "middleware execution order should match expected pattern")
}

// TestMiddlewareOrder_CacheBypassesRetry verifies that cache hits
// bypass the retry mechanism entirely, which is the desired behavior.
func TestMiddlewareOrder_CacheBypassesRetry(t *testing.T) {
	ctx := context.Background()

	// Track if core handler is called
	var coreHandlerCalled bool
	coreHandler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		coreHandlerCalled = true
		return &transport.Response{
			Content:      "from core",
			FinishReason: domain.FinishStop,
			Usage:        transport.NormalizedUsage{TotalTokens: 10},
		}, nil
	})

	// Cache middleware that always returns cache hit
	cacheMiddleware := func(next transport.Handler) transport.Handler {
		return transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
			// Always return cache hit, never call next
			return &transport.Response{
				Content:      "from cache",
				FinishReason: domain.FinishStop,
				Usage:        transport.NormalizedUsage{TotalTokens: 0}, // Cache hits have no token cost
			}, nil
		})
	}

	// Build stack: Cache → [retry wrapping core]
	retryConfig := configuration.RetryConfig{
		MaxAttempts:     3,
		InitialInterval: 10 * time.Millisecond,
		Multiplier:      2.0,
		MaxInterval:     100 * time.Millisecond,
		MaxElapsedTime:  1 * time.Second,
		UseJitter:       false,
	}
	retryMiddleware, _ := retry.NewRetryMiddlewareWithConfig(retryConfig)
	retryHandler := retryMiddleware(coreHandler)

	handler := transport.Chain(retryHandler, cacheMiddleware)

	req := &transport.Request{
		Operation: transport.OpGeneration,
		Provider:  "test",
		Model:     "test-model",
		Question:  "test question",
	}

	// Execute request
	resp, err := handler.Handle(ctx, req)
	require.NoError(t, err, "unexpected error")

	// Verify cache hit response
	assert.Equal(t, "from cache", resp.Content, "expected cached response")

	// Verify core handler was never called (cache bypassed retry entirely)
	assert.False(t, coreHandlerCalled, "expected cache to bypass core handler")

	// Verify no token usage for cache hit
	assert.Equal(t, int64(0), resp.Usage.TotalTokens, "expected 0 tokens for cache hit")
}
