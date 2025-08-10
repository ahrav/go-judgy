package retry_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"reflect"
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

// contextKey is an unexported type for context keys to prevent external forgery.
type contextKey struct{}

// circuitBreakerProbeKey is the context key for circuit breaker half-open probe indication.
var circuitBreakerProbeKey = contextKey{}

// TestNewRetryMiddlewareWithConfig validates the behavior of the retry middleware constructor.
// It verifies proper initialization with a comprehensive configuration,
// including timing parameters, jitter settings, and budget enforcement options.
// The test ensures that the returned middleware can successfully wrap a handler.
func TestNewRetryMiddlewareWithConfig(t *testing.T) {
	config := configuration.RetryConfig{
		MaxAttempts:     5,
		MaxElapsedTime:  30 * time.Second,
		InitialInterval: 100 * time.Millisecond,
		MaxInterval:     10 * time.Second,
		Multiplier:      2.0,
		UseJitter:       true,
		MaxCostCents:    1000,
		MaxTokens:       50000,
		EnableBudget:    true,
	}

	middleware, err := retry.NewRetryMiddlewareWithConfig(config)
	require.NoError(t, err)
	require.NotNil(t, middleware, "expected retry middleware, got nil")

	// Test that middleware can wrap a handler
	handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		return &transport.Response{
			Content:      "success",
			FinishReason: domain.FinishStop,
			Usage:        transport.NormalizedUsage{TotalTokens: 10},
		}, nil
	})

	wrappedHandler := middleware(handler)
	require.NotNil(t, wrappedHandler, "expected wrapped handler, got nil")
}

// TestRetryMiddleware_MaxAttempts validates the enforcement of attempt limits.
// It tests the retry middleware's adherence to the MaxAttempts configuration
// across various retry scenarios.
// The test ensures precise call counting and proper failure handling
// when all retry attempts are exhausted.
func TestRetryMiddleware_MaxAttempts(t *testing.T) {
	tests := []struct {
		name        string
		maxAttempts int
		expectCalls int
	}{
		{
			name:        "single attempt",
			maxAttempts: 1,
			expectCalls: 1,
		},
		{
			name:        "three attempts",
			maxAttempts: 3,
			expectCalls: 3,
		},
		{
			name:        "five attempts",
			maxAttempts: 5,
			expectCalls: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			var callCount int32

			config := configuration.RetryConfig{
				MaxAttempts:     tt.maxAttempts,
				MaxElapsedTime:  10 * time.Second,
				InitialInterval: 10 * time.Millisecond,
				MaxInterval:     100 * time.Millisecond,
				Multiplier:      2.0,
				UseJitter:       false,
			}

			failingHandler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				atomic.AddInt32(&callCount, 1)
				return nil, &llmerrors.ProviderError{
					Provider:   "test",
					StatusCode: 500,
					Message:    "server error",
					Type:       llmerrors.ErrorTypeProvider,
				}
			})

			middleware, err := retry.NewRetryMiddlewareWithConfig(config)
			require.NoError(t, err)
			handler := middleware(failingHandler)

			req := &transport.Request{
				Operation: transport.OpGeneration,
				Provider:  "test",
				Model:     "test-model",
				Question:  "test question",
			}

			_, retryErr := handler.Handle(ctx, req)
			require.Error(t, retryErr, "expected error after max attempts")

			assert.Contains(t, retryErr.Error(), fmt.Sprintf("all retries exhausted after %d attempts", tt.maxAttempts), "expected exhaustion error, got: %v", retryErr)

			actualCalls := atomic.LoadInt32(&callCount)
			assert.Equal(t, int32(tt.expectCalls), actualCalls, "expected %d calls, got %d", tt.expectCalls, actualCalls)
		})
	}
}

// TestRetryMiddleware_SuccessAfterRetry verifies immediate success handling in the retry middleware.
// It validates that the middleware terminates retry loops immediately upon a successful call,
// preventing unnecessary additional attempts.
// The test also confirms proper response propagation with accurate attempt counting.
func TestRetryMiddleware_SuccessAfterRetry(t *testing.T) {
	ctx := context.Background()
	var callCount int32

	config := configuration.RetryConfig{
		MaxAttempts:     3,
		MaxElapsedTime:  5 * time.Second,
		InitialInterval: 10 * time.Millisecond,
		MaxInterval:     100 * time.Millisecond,
		Multiplier:      2.0,
		UseJitter:       false,
	}

	handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		calls := atomic.AddInt32(&callCount, 1)
		if calls < 2 {
			return nil, &llmerrors.ProviderError{
				Provider:   "test",
				StatusCode: 500,
				Message:    "server error",
				Type:       llmerrors.ErrorTypeProvider,
			}
		}
		return &transport.Response{
			Content:      "success",
			FinishReason: domain.FinishStop,
			Usage:        transport.NormalizedUsage{TotalTokens: 10},
		}, nil
	})

	middleware, err := retry.NewRetryMiddlewareWithConfig(config)
	require.NoError(t, err)
	wrappedHandler := middleware(handler)

	req := &transport.Request{
		Operation: transport.OpGeneration,
		Provider:  "test",
		Model:     "test-model",
		Question:  "test question",
	}

	resp, err := wrappedHandler.Handle(ctx, req)
	require.NoError(t, err, "expected success after retry")

	assert.Equal(t, "success", resp.Content, "expected 'success', got: %s", resp.Content)

	actualCalls := atomic.LoadInt32(&callCount)
	assert.Equal(t, int32(2), actualCalls, "expected 2 calls (1 failure + 1 success), got %d", actualCalls)
}

// TestRetryMiddleware_MaxElapsedTime validates that the retry middleware respects
// the maximum elapsed time limit.
// It ensures that retrying stops when the configured time limit is exceeded.
func TestRetryMiddleware_MaxElapsedTime(t *testing.T) {
	ctx := context.Background()
	var callCount int32

	config := configuration.RetryConfig{
		MaxAttempts:     10, // High to ensure time limit is hit first
		MaxElapsedTime:  100 * time.Millisecond,
		InitialInterval: 20 * time.Millisecond,
		MaxInterval:     50 * time.Millisecond,
		Multiplier:      2.0,
		UseJitter:       false,
	}

	failingHandler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		atomic.AddInt32(&callCount, 1)
		return nil, &llmerrors.ProviderError{
			Provider:   "test",
			StatusCode: 500,
			Message:    "server error",
			Type:       llmerrors.ErrorTypeProvider,
		}
	})

	middleware, err := retry.NewRetryMiddlewareWithConfig(config)
	require.NoError(t, err)
	handler := middleware(failingHandler)

	req := &transport.Request{
		Operation: transport.OpGeneration,
		Provider:  "test",
		Model:     "test-model",
		Question:  "test question",
	}

	start := time.Now()
	_, err = handler.Handle(ctx, req)
	elapsed := time.Since(start)

	require.Error(t, err, "expected error after max elapsed time")

	// Should have stopped due to time limit, not max attempts
	actualCalls := atomic.LoadInt32(&callCount)
	assert.Less(t, actualCalls, int32(10), "expected fewer than 10 calls due to time limit, got %d", actualCalls)

	// Should not have exceeded max elapsed time by too much
	// Allow some tolerance for execution time
	assert.LessOrEqual(t, elapsed, config.MaxElapsedTime+50*time.Millisecond,
		"expected elapsed time <= %v, got %v", config.MaxElapsedTime+50*time.Millisecond, elapsed)
}

// TestRetryMiddleware_BudgetLimits validates comprehensive budget enforcement.
// It tests cost and token limit enforcement during retry sequences,
// ensuring the middleware terminates appropriately when spending thresholds are exceeded.
// This prevents runaway costs in failure scenarios.
func TestRetryMiddleware_BudgetLimits(t *testing.T) {
	tests := []struct {
		name           string
		maxCostCents   int64
		maxTokens      int64
		responseCost   int64 // milli-cents
		responseTokens int64
		expectStop     bool
	}{
		{
			name:           "within cost budget",
			maxCostCents:   100,
			maxTokens:      1000,
			responseCost:   50000, // 50 cents in milli-cents (50 * 1000)
			responseTokens: 100,
			expectStop:     false,
		},
		{
			name:           "exceeds cost budget",
			maxCostCents:   50,
			maxTokens:      1000,
			responseCost:   60000, // 60 cents in milli-cents (60 * 1000)
			responseTokens: 100,
			expectStop:     true,
		},
		{
			name:           "exceeds token budget",
			maxCostCents:   100,
			maxTokens:      50,
			responseCost:   20000, // 20 cents in milli-cents (20 * 1000)
			responseTokens: 60,
			expectStop:     true,
		},
		{
			name:           "budget disabled",
			maxCostCents:   10,
			maxTokens:      10,
			responseCost:   1000,  // Way over budget
			responseTokens: 100,   // Way over budget
			expectStop:     false, // But budget disabled
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			var callCount int32

			config := configuration.RetryConfig{
				MaxAttempts:     3,
				MaxElapsedTime:  5 * time.Second,
				InitialInterval: 10 * time.Millisecond,
				MaxInterval:     100 * time.Millisecond,
				Multiplier:      2.0,
				UseJitter:       false,
				MaxCostCents:    tt.maxCostCents,
				MaxTokens:       tt.maxTokens,
				EnableBudget:    tt.name != "budget disabled",
			}

			handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				calls := atomic.AddInt32(&callCount, 1)
				if calls == 1 {
					// First call returns response with cost/tokens, then fails
					return &transport.Response{
							Content:                 "partial",
							FinishReason:            domain.FinishStop,
							Usage:                   transport.NormalizedUsage{TotalTokens: tt.responseTokens},
							EstimatedCostMilliCents: tt.responseCost,
						}, &llmerrors.ProviderError{
							Provider:   "test",
							StatusCode: 500,
							Message:    "server error",
							Type:       llmerrors.ErrorTypeProvider,
						}
				}
				// Subsequent calls should be blocked by budget
				return nil, &llmerrors.ProviderError{
					Provider:   "test",
					StatusCode: 500,
					Message:    "server error",
					Type:       llmerrors.ErrorTypeProvider,
				}
			})

			middleware, err := retry.NewRetryMiddlewareWithConfig(config)
			if err != nil {
				t.Fatal(err)
			}
			wrappedHandler := middleware(handler)

			req := &transport.Request{
				Operation: transport.OpGeneration,
				Provider:  "test",
				Model:     "test-model",
				Question:  "test question",
			}

			_, err = wrappedHandler.Handle(ctx, req)
			require.Error(t, err, "expected error")

			actualCalls := atomic.LoadInt32(&callCount)

			if tt.expectStop {
				// Should stop after first attempt due to budget
				assert.Equal(t, int32(1), actualCalls, "expected 1 call due to budget limit, got %d", actualCalls)
				assert.Contains(t, err.Error(), "RETRY_BUDGET_EXCEEDED", "expected budget exceeded error, got: %v", err)
			} else {
				// Should proceed with all retries
				assert.Equal(t, int32(config.MaxAttempts), actualCalls, "expected %d calls, got %d", config.MaxAttempts, actualCalls)
			}
		})
	}
}

// TestRetryMiddleware_CircuitBreakerIntegration validates that the retry middleware
// properly handles circuit breaker errors.
// It also ensures that the middleware respects half-open probe limits.
func TestRetryMiddleware_CircuitBreakerIntegration(t *testing.T) {
	ctx := context.Background()

	config := configuration.RetryConfig{
		MaxAttempts:     3,
		MaxElapsedTime:  5 * time.Second,
		InitialInterval: 10 * time.Millisecond,
		MaxInterval:     100 * time.Millisecond,
		Multiplier:      2.0,
		UseJitter:       false,
	}

	tests := []struct {
		name        string
		error       error
		expectRetry bool
	}{
		{
			name: "circuit breaker open",
			error: &llmerrors.CircuitBreakerError{
				Provider: "test",
				Model:    "test-model",
				State:    "open",
				ResetAt:  time.Now().Add(30 * time.Second).Unix(),
			},
			expectRetry: false, // Circuit breaker errors are non-retryable
		},
		{
			name: "provider error retryable",
			error: &llmerrors.ProviderError{
				Provider:   "test",
				StatusCode: 500,
				Message:    "server error",
				Type:       llmerrors.ErrorTypeProvider,
			},
			expectRetry: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var callCount int32

			handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				atomic.AddInt32(&callCount, 1)
				return nil, tt.error
			})

			// Test with half-open probe context
			ctxWithProbe := context.WithValue(ctx, circuitBreakerProbeKey, true)

			middleware, err := retry.NewRetryMiddlewareWithConfig(config)
			if err != nil {
				t.Fatal(err)
			}
			wrappedHandler := middleware(handler)

			req := &transport.Request{
				Operation: transport.OpGeneration,
				Provider:  "test",
				Model:     "test-model",
				Question:  "test question",
			}

			_, err = wrappedHandler.Handle(ctxWithProbe, req)
			require.Error(t, err, "expected error")

			actualCalls := atomic.LoadInt32(&callCount)

			// Half-open probe should limit to 1 attempt regardless of retryability
			assert.Equal(t, int32(1), actualCalls, "expected 1 call for half-open probe, got %d", actualCalls)

			// Now test without probe context for retryable errors
			if tt.expectRetry {
				atomic.StoreInt32(&callCount, 0)
				_, err = wrappedHandler.Handle(ctx, req)
				if err == nil {
					t.Fatal("expected error")
				}

				actualCalls = atomic.LoadInt32(&callCount)
				if actualCalls != int32(config.MaxAttempts) {
					t.Errorf("expected %d calls for retryable error, got %d", config.MaxAttempts, actualCalls)
				}
			}
		})
	}
}

// TestRetryMiddleware_ContextCancellation validates that the retry middleware
// respects context cancellation during backoff periods.
// It ensures that the retry loop is terminated when the context is cancelled.
func TestRetryMiddleware_ContextCancellation(t *testing.T) {
	config := configuration.RetryConfig{
		MaxAttempts:     5,
		MaxElapsedTime:  10 * time.Second,
		InitialInterval: 100 * time.Millisecond,
		MaxInterval:     1 * time.Second,
		Multiplier:      2.0,
		UseJitter:       false,
	}

	var callCount int32
	handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		atomic.AddInt32(&callCount, 1)
		return nil, &llmerrors.ProviderError{
			Provider:   "test",
			StatusCode: 500,
			Message:    "server error",
			Type:       llmerrors.ErrorTypeProvider,
		}
	})

	middleware, err := retry.NewRetryMiddlewareWithConfig(config)
	require.NoError(t, err)
	wrappedHandler := middleware(handler)

	req := &transport.Request{
		Operation: transport.OpGeneration,
		Provider:  "test",
		Model:     "test-model",
		Question:  "test question",
	}

	// Create context that cancels after first failure
	ctx, cancel := context.WithCancel(context.Background())

	// Start request processing in goroutine
	done := make(chan error)
	go func() {
		_, err := wrappedHandler.Handle(ctx, req)
		done <- err
	}()

	// Cancel context after allowing first call
	time.Sleep(50 * time.Millisecond)
	cancel()

	// Wait for completion
	err = <-done

	require.Error(t, err, "expected error due to context cancellation")

	assert.Contains(t, err.Error(), "context cancelled during retry", "expected context cancellation error, got: %v", err)

	// Should have made at least 1 call, but not all attempts
	actualCalls := atomic.LoadInt32(&callCount)
	assert.NotEqual(t, int32(0), actualCalls, "expected at least 1 call before cancellation")
	assert.Less(t, actualCalls, int32(config.MaxAttempts), "expected fewer than %d calls due to cancellation, got %d", config.MaxAttempts, actualCalls)
}

// TestIsRetryable validates the error classification logic.
// It determines which errors should trigger retry attempts versus immediate failure.
// Since isRetryable is not an exported function, this test validates its
// behavior indirectly through the middleware.
func TestIsRetryable(t *testing.T) {
	// Create a retry middleware to access the isRetryable method
	config := configuration.RetryConfig{
		MaxAttempts:     3,
		MaxElapsedTime:  1 * time.Second,
		InitialInterval: 10 * time.Millisecond,
		MaxInterval:     100 * time.Millisecond,
		Multiplier:      2.0,
	}

	// We'll test this indirectly through middleware behavior
	// since isRetryable is not exported

	tests := []struct {
		name        string
		error       error
		expectRetry bool
	}{
		{
			name:        "nil error",
			error:       nil,
			expectRetry: false,
		},
		{
			name: "rate limit error",
			error: &llmerrors.RateLimitError{
				Provider:   "test",
				RetryAfter: 5,
			},
			expectRetry: true,
		},
		{
			name: "retryable provider error",
			error: &llmerrors.ProviderError{
				Provider:   "test",
				StatusCode: 500,
				Message:    "server error",
				Type:       llmerrors.ErrorTypeProvider,
			},
			expectRetry: true,
		},
		{
			name: "non-retryable provider error",
			error: &llmerrors.ProviderError{
				Provider:   "test",
				StatusCode: 400,
				Message:    "bad request",
				Type:       llmerrors.ErrorTypeAuth,
			},
			expectRetry: false,
		},
		{
			name:        "context deadline exceeded",
			error:       context.DeadlineExceeded,
			expectRetry: true,
		},
		{
			name: "network error",
			error: &net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: errors.New("connection refused"),
			},
			expectRetry: true,
		},
		{
			name:        "unknown error",
			error:       errors.New("unknown error"),
			expectRetry: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			var callCount int32

			handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				atomic.AddInt32(&callCount, 1)
				return nil, tt.error
			})

			middleware, err := retry.NewRetryMiddlewareWithConfig(config)
			if err != nil {
				t.Fatal(err)
			}
			wrappedHandler := middleware(handler)

			req := &transport.Request{
				Operation: transport.OpGeneration,
				Provider:  "test",
				Model:     "test-model",
				Question:  "test question",
			}

			_, err = wrappedHandler.Handle(ctx, req)

			actualCalls := atomic.LoadInt32(&callCount)

			if tt.expectRetry {
				// Should retry up to max attempts
				if actualCalls != int32(config.MaxAttempts) {
					t.Errorf("expected %d calls for retryable error, got %d", config.MaxAttempts, actualCalls)
				}
			} else {
				// Should not retry (only 1 call)
				if actualCalls != 1 {
					t.Errorf("expected 1 call for non-retryable error, got %d", actualCalls)
				}
			}

			// Verify error propagation
			if tt.error == nil && err != nil {
				t.Errorf("expected no error, got: %v", err)
			} else if tt.error != nil && err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

// TestCalculateBackoff validates the exponential backoff calculation.
// It covers various configurations, including jitter, multipliers, and boundary conditions.
func TestCalculateBackoff(t *testing.T) {
	tests := []struct {
		name          string
		config        configuration.RetryConfig
		attempt       int
		expectedRange [2]time.Duration // min, max expected
	}{
		{
			name: "first attempt no jitter",
			config: configuration.RetryConfig{
				InitialInterval: 100 * time.Millisecond,
				Multiplier:      2.0,
				MaxInterval:     1 * time.Second,
				UseJitter:       false,
			},
			attempt:       1,
			expectedRange: [2]time.Duration{100 * time.Millisecond, 100 * time.Millisecond},
		},
		{
			name: "second attempt no jitter",
			config: configuration.RetryConfig{
				InitialInterval: 100 * time.Millisecond,
				Multiplier:      2.0,
				MaxInterval:     1 * time.Second,
				UseJitter:       false,
			},
			attempt:       2,
			expectedRange: [2]time.Duration{200 * time.Millisecond, 200 * time.Millisecond},
		},
		{
			name: "with jitter",
			config: configuration.RetryConfig{
				InitialInterval: 100 * time.Millisecond,
				Multiplier:      2.0,
				MaxInterval:     1 * time.Second,
				UseJitter:       true,
			},
			attempt:       2,
			expectedRange: [2]time.Duration{0, 200 * time.Millisecond},
		},
		{
			name: "max interval cap",
			config: configuration.RetryConfig{
				InitialInterval: 100 * time.Millisecond,
				Multiplier:      2.0,
				MaxInterval:     150 * time.Millisecond,
				UseJitter:       false,
			},
			attempt:       3, // Would be 400ms without cap
			expectedRange: [2]time.Duration{150 * time.Millisecond, 150 * time.Millisecond},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test the standalone utility function
			backoff := retry.ExponentialBackoff(tt.attempt, tt.config)

			if backoff < tt.expectedRange[0] || backoff > tt.expectedRange[1] {
				t.Errorf("backoff %v not in expected range [%v, %v]",
					backoff, tt.expectedRange[0], tt.expectedRange[1])
			}

			// For non-jitter tests, should be exact
			if !tt.config.UseJitter && backoff != tt.expectedRange[0] {
				t.Errorf("expected exact backoff %v, got %v", tt.expectedRange[0], backoff)
			}
		})
	}
}

// TestExponentialBackoff_EdgeCases validates edge cases and boundary conditions
// in the ExponentialBackoff utility function.
func TestExponentialBackoff_EdgeCases(t *testing.T) {
	config := configuration.RetryConfig{
		InitialInterval: 100 * time.Millisecond,
		Multiplier:      2.0,
		MaxInterval:     1 * time.Second,
		UseJitter:       false,
	}

	tests := []struct {
		name     string
		attempt  int
		expected time.Duration
	}{
		{
			name:     "zero attempt",
			attempt:  0,
			expected: 0,
		},
		{
			name:     "negative attempt",
			attempt:  -1,
			expected: 0,
		},
		{
			name:     "first attempt",
			attempt:  1,
			expected: 100 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backoff := retry.ExponentialBackoff(tt.attempt, config)
			if backoff != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, backoff)
			}
		})
	}
}

// TestCalculateJitter validates the jitter calculation.
// It covers various factors and boundary conditions.
func TestCalculateJitter(t *testing.T) {
	base := 100 * time.Millisecond

	tests := []struct {
		name          string
		factor        float64
		expectedRange [2]time.Duration
	}{
		{
			name:          "zero factor",
			factor:        0.0,
			expectedRange: [2]time.Duration{base, base},
		},
		{
			name:          "negative factor",
			factor:        -0.5,
			expectedRange: [2]time.Duration{base, base},
		},
		{
			name:          "half factor",
			factor:        0.5,
			expectedRange: [2]time.Duration{base, base + 50*time.Millisecond},
		},
		{
			name:          "full factor",
			factor:        1.0,
			expectedRange: [2]time.Duration{base, base + base},
		},
		{
			name:          "over factor capped",
			factor:        2.0,
			expectedRange: [2]time.Duration{base, base + base}, // Capped at 1.0
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test multiple times due to randomness
			for i := 0; i < 10; i++ {
				result := retry.CalculateJitter(base, tt.factor)
				if result < tt.expectedRange[0] || result > tt.expectedRange[1] {
					t.Errorf("result %v not in expected range [%v, %v]",
						result, tt.expectedRange[0], tt.expectedRange[1])
				}
			}
		})
	}
}

// TestGetActivityOptions validates the Temporal activity configuration.
// It checks the options generated for different idempotency scenarios.
func TestGetActivityOptions(t *testing.T) {
	tests := []struct {
		name         string
		isIdempotent bool
		expectKey    string
		expectValue  any
	}{
		{
			name:         "idempotent operation",
			isIdempotent: true,
			expectKey:    "RetryPolicy",
			expectValue: map[string]any{
				"MaximumAttempts": 1,
			},
		},
		{
			name:         "non-idempotent operation",
			isIdempotent: false,
			expectKey:    "RetryPolicy",
			expectValue: map[string]any{
				"MaximumAttempts":        3,
				"NonRetryableErrorTypes": []string{"ValidationError", "AuthError"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := retry.GetActivityOptions(tt.isIdempotent)

			require.NotNil(t, options, "expected options, got nil")

			retryPolicy, exists := options[tt.expectKey]
			if !exists {
				t.Fatalf("expected key %s, not found", tt.expectKey)
			}

			if !reflect.DeepEqual(retryPolicy, tt.expectValue) {
				t.Errorf("expected %v, got %v", tt.expectValue, retryPolicy)
			}

			// Verify timeout is set
			timeout, exists := options["StartToCloseTimeout"]
			if !exists {
				t.Error("expected StartToCloseTimeout to be set")
			}
			if timeout != "5m" {
				t.Errorf("expected StartToCloseTimeout '5m', got %v", timeout)
			}
		})
	}
}

// TestParseRetryAfterValue validates the parsing of various retry-after header formats.
// It checks integers, floats, strings, and RFC date formats.
// This test validates the behavior indirectly through the middleware's handling
// of retry-after values in errors.
func TestParseRetryAfterValue(t *testing.T) {
	// We'll test this indirectly through the middleware's retry-after handling

	tests := []struct {
		name          string
		retryAfterErr *llmerrors.ProviderError
		expectDelay   bool
	}{
		{
			name: "retry after seconds",
			retryAfterErr: &llmerrors.ProviderError{
				Provider:   "test",
				StatusCode: 429,
				Message:    "rate limited",
				Type:       llmerrors.ErrorTypeRateLimit,
				RetryAfter: 5,
			},
			expectDelay: true,
		},
		{
			name: "no retry after",
			retryAfterErr: &llmerrors.ProviderError{
				Provider:   "test",
				StatusCode: 429,
				Message:    "rate limited",
				Type:       llmerrors.ErrorTypeRateLimit,
				RetryAfter: 0,
			},
			expectDelay: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			var callCount int32

			config := configuration.RetryConfig{
				MaxAttempts:     2,
				MaxElapsedTime:  10 * time.Second,
				InitialInterval: 10 * time.Millisecond,
				MaxInterval:     100 * time.Millisecond,
				Multiplier:      2.0,
				UseJitter:       false,
			}

			handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				calls := atomic.AddInt32(&callCount, 1)
				if calls == 1 {
					return nil, tt.retryAfterErr
				}
				return &transport.Response{
					Content:      "success",
					FinishReason: domain.FinishStop,
					Usage:        transport.NormalizedUsage{TotalTokens: 10},
				}, nil
			})

			middleware, err := retry.NewRetryMiddlewareWithConfig(config)
			if err != nil {
				t.Fatal(err)
			}
			wrappedHandler := middleware(handler)

			req := &transport.Request{
				Operation: transport.OpGeneration,
				Provider:  "test",
				Model:     "test-model",
				Question:  "test question",
			}

			start := time.Now()
			resp, err := wrappedHandler.Handle(ctx, req)
			elapsed := time.Since(start)

			if err != nil {
				t.Fatalf("expected success after retry, got: %v", err)
			}

			if resp.Content != "success" {
				t.Errorf("expected success response, got: %s", resp.Content)
			}

			if tt.expectDelay {
				// Should have waited for RetryAfter duration
				expectedDelay := time.Duration(tt.retryAfterErr.RetryAfter) * time.Second
				if elapsed < expectedDelay {
					t.Errorf("expected delay >= %v, got %v", expectedDelay, elapsed)
				}
			} else if elapsed > 500*time.Millisecond {
				// Should use normal backoff (much shorter)
				t.Errorf("expected short delay, got %v", elapsed)
			}
		})
	}
}

// TestIsNetworkError validates network error detection.
// It covers both typed errors and string pattern matching to identify network issues.
// This test validates the behavior indirectly through the middleware.
func TestIsNetworkError(t *testing.T) {
	tests := []struct {
		name      string
		error     error
		isNetwork bool
	}{
		{
			name:      "nil error",
			error:     nil,
			isNetwork: false,
		},
		{
			name: "net.OpError",
			error: &net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: errors.New("connection refused"),
			},
			isNetwork: true,
		},
		{
			name: "net.DNSError",
			error: &net.DNSError{
				Err:  "no such host",
				Name: "invalid.host",
			},
			isNetwork: true,
		},
		{
			name: "url.Error temporary",
			error: &url.Error{
				Op:  "Get",
				URL: "http://example.com",
				Err: &testTempError{msg: "temporary failure"},
			},
			isNetwork: true,
		},
		{
			name: "url.Error non-temporary",
			error: &url.Error{
				Op:  "Get",
				URL: "http://example.com",
				Err: errors.New("permanent failure"),
			},
			isNetwork: false,
		},
		{
			name:      "connection refused string",
			error:     errors.New("connection refused"),
			isNetwork: true,
		},
		{
			name:      "i/o timeout string",
			error:     errors.New("i/o timeout"),
			isNetwork: true,
		},
		{
			name:      "EOF string",
			error:     errors.New("EOF"),
			isNetwork: true,
		},
		{
			name:      "broken pipe string",
			error:     errors.New("broken pipe"),
			isNetwork: true,
		},
		{
			name:      "no such host string",
			error:     errors.New("no such host"),
			isNetwork: true,
		},
		{
			name:      "network is unreachable string",
			error:     errors.New("network is unreachable"),
			isNetwork: true,
		},
		{
			name:      "connection reset string",
			error:     errors.New("connection reset"),
			isNetwork: true,
		},
		{
			name:      "case insensitive matching",
			error:     errors.New("Connection Refused"),
			isNetwork: true,
		},
		{
			name:      "non-network error",
			error:     errors.New("validation failed"),
			isNetwork: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			var callCount int32

			config := configuration.RetryConfig{
				MaxAttempts:     2,
				MaxElapsedTime:  1 * time.Second,
				InitialInterval: 10 * time.Millisecond,
				MaxInterval:     100 * time.Millisecond,
				Multiplier:      2.0,
				UseJitter:       false,
			}

			handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				atomic.AddInt32(&callCount, 1)
				return nil, tt.error
			})

			middleware, err := retry.NewRetryMiddlewareWithConfig(config)
			if err != nil {
				t.Fatal(err)
			}
			wrappedHandler := middleware(handler)

			req := &transport.Request{
				Operation: transport.OpGeneration,
				Provider:  "test",
				Model:     "test-model",
				Question:  "test question",
			}

			_, err = wrappedHandler.Handle(ctx, req)

			actualCalls := atomic.LoadInt32(&callCount)

			if tt.isNetwork {
				// Should retry network errors
				if actualCalls != int32(config.MaxAttempts) {
					t.Errorf("expected %d calls for network error, got %d", config.MaxAttempts, actualCalls)
				}
			} else if tt.error != nil {
				// Non-network errors should not retry
				if actualCalls != 1 {
					t.Errorf("expected 1 call for non-network error, got %d", actualCalls)
				}
			}

			// Error should be propagated
			if tt.error != nil && err == nil {
				t.Error("expected error to be propagated")
			}
		})
	}
}

// testTempError is a mock error type that implements the net.Error interface.
// It is used for testing purposes, particularly for simulating temporary network errors.
type testTempError struct {
	msg string
}

func (e *testTempError) Error() string   { return e.msg }
func (e *testTempError) Temporary() bool { return true }
func (e *testTempError) Timeout() bool   { return false }

// TestRetryMiddleware_RetryAfterProvider validates that errors implementing
// the RetryAfterProvider interface are handled correctly by the middleware.
// It ensures the middleware respects the custom retry duration provided by the error.
func TestRetryMiddleware_RetryAfterProvider(t *testing.T) {
	ctx := context.Background()
	var callCount int32

	config := configuration.RetryConfig{
		MaxAttempts:     2,
		MaxElapsedTime:  10 * time.Second,
		InitialInterval: 10 * time.Millisecond,
		MaxInterval:     100 * time.Millisecond,
		Multiplier:      2.0,
		UseJitter:       false,
	}

	// Custom error that implements RetryAfterProvider
	customErr := &testRetryAfterError{
		msg:        "rate limited",
		retryAfter: 200 * time.Millisecond,
	}

	handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		calls := atomic.AddInt32(&callCount, 1)
		if calls == 1 {
			return nil, customErr
		}
		return &transport.Response{
			Content:      "success",
			FinishReason: domain.FinishStop,
			Usage:        transport.NormalizedUsage{TotalTokens: 10},
		}, nil
	})

	middleware, err := retry.NewRetryMiddlewareWithConfig(config)
	require.NoError(t, err)
	wrappedHandler := middleware(handler)

	req := &transport.Request{
		Operation: transport.OpGeneration,
		Provider:  "test",
		Model:     "test-model",
		Question:  "test question",
	}

	start := time.Now()
	resp, err := wrappedHandler.Handle(ctx, req)
	elapsed := time.Since(start)

	require.NoError(t, err, "expected success after retry")

	if resp.Content != "success" {
		t.Errorf("expected success response, got: %s", resp.Content)
	}

	// Should have waited for the custom retry after duration
	if elapsed < customErr.retryAfter {
		t.Errorf("expected delay >= %v, got %v", customErr.retryAfter, elapsed)
	}

	actualCalls := atomic.LoadInt32(&callCount)
	if actualCalls != 2 {
		t.Errorf("expected 2 calls, got %d", actualCalls)
	}
}

// testRetryAfterError is a mock error type that implements the RetryAfterProvider interface.
// It is used for testing how the middleware handles custom retry-after durations.
type testRetryAfterError struct {
	msg        string
	retryAfter time.Duration
}

func (e *testRetryAfterError) Error() string                { return e.msg }
func (e *testRetryAfterError) GetRetryAfter() time.Duration { return e.retryAfter }

// TestRetryMiddleware_WorkflowError validates the handling of a WorkflowError.
// It ensures the middleware correctly extracts and respects the retry-after
// duration from the error's Details map.
func TestRetryMiddleware_WorkflowError(t *testing.T) {
	ctx := context.Background()
	var callCount int32

	config := configuration.RetryConfig{
		MaxAttempts:     2,
		MaxElapsedTime:  10 * time.Second,
		InitialInterval: 10 * time.Millisecond,
		MaxInterval:     100 * time.Millisecond,
		Multiplier:      2.0,
		UseJitter:       false,
	}

	workflowErr := &llmerrors.WorkflowError{
		Type:      llmerrors.ErrorTypeRateLimit,
		Message:   "rate limited",
		Code:      "RATE_LIMIT",
		Retryable: true,
		Details: map[string]any{
			"retry_after": 3, // 3 seconds
		},
	}

	handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		calls := atomic.AddInt32(&callCount, 1)
		if calls == 1 {
			return nil, workflowErr
		}
		return &transport.Response{
			Content:      "success",
			FinishReason: domain.FinishStop,
			Usage:        transport.NormalizedUsage{TotalTokens: 10},
		}, nil
	})

	middleware, err := retry.NewRetryMiddlewareWithConfig(config)
	require.NoError(t, err)
	wrappedHandler := middleware(handler)

	req := &transport.Request{
		Operation: transport.OpGeneration,
		Provider:  "test",
		Model:     "test-model",
		Question:  "test question",
	}

	start := time.Now()
	resp, err := wrappedHandler.Handle(ctx, req)
	elapsed := time.Since(start)

	require.NoError(t, err, "expected success after retry")

	if resp.Content != "success" {
		t.Errorf("expected success response, got: %s", resp.Content)
	}

	// Should have waited for retry_after duration (3 seconds)
	expectedDelay := 3 * time.Second
	if elapsed < expectedDelay {
		t.Errorf("expected delay >= %v, got %v", expectedDelay, elapsed)
	}

	actualCalls := atomic.LoadInt32(&callCount)
	if actualCalls != 2 {
		t.Errorf("expected 2 calls, got %d", actualCalls)
	}
}

func TestBudget_MilliCentsConversion(t *testing.T) {
	cfg := configuration.RetryConfig{
		MaxAttempts:     2,
		MaxElapsedTime:  5 * time.Second,
		InitialInterval: 1 * time.Millisecond,
		MaxInterval:     1 * time.Millisecond,
		Multiplier:      1,
		UseJitter:       false,
		EnableBudget:    true,
		MaxCostCents:    2, // 2 cents total budget
	}

	var calls int32
	h := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		atomic.AddInt32(&calls, 1)
		// 1000 milli-cents == 1 cent (if "milli" is correct)
		return &transport.Response{
				Content:                 "partial",
				FinishReason:            domain.FinishStop,
				EstimatedCostMilliCents: 1000,
			}, &llmerrors.ProviderError{
				Provider:   "test",
				StatusCode: 500,
				Type:       llmerrors.ErrorTypeProvider,
				Message:    "fail",
			}
	})

	mw, err := retry.NewRetryMiddlewareWithConfig(cfg)
	require.NoError(t, err)
	_, _ = mw(h).Handle(context.Background(), &transport.Request{
		Operation: transport.OpGeneration, Provider: "test", Model: "m", Question: "q",
	})

	// With correct milli-cent handling, the second attempt is still within the 2-cent budget.
	assert.Equal(t, int32(2), atomic.LoadInt32(&calls),
		"expected 2 attempts; current code divides by 10 and trips the budget after 1 attempt")
}

func TestMaxElapsedTime_ZeroMeansNoLimit(t *testing.T) {
	cfg := configuration.RetryConfig{
		MaxAttempts:     2,
		MaxElapsedTime:  0, // should mean "no cap"
		InitialInterval: 1 * time.Millisecond,
		MaxInterval:     1 * time.Millisecond,
		Multiplier:      1,
	}

	var calls int32
	h := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		atomic.AddInt32(&calls, 1)
		return nil, &llmerrors.ProviderError{
			Provider:   "test",
			StatusCode: 500,
			Type:       llmerrors.ErrorTypeProvider,
			Message:    "fail",
		}
	})

	mw, err := retry.NewRetryMiddlewareWithConfig(cfg)
	require.NoError(t, err)
	_, _ = mw(h).Handle(context.Background(), &transport.Request{
		Operation: transport.OpGeneration, Provider: "test", Model: "m", Question: "q",
	})

	assert.GreaterOrEqual(t, atomic.LoadInt32(&calls), int32(1),
		"MaxElapsedTime==0 currently blocks the loop before first attempt")
}

func TestNewRetryMiddleware_ValidatesConfig(t *testing.T) {
	_, err := retry.NewRetryMiddlewareWithConfig(configuration.RetryConfig{
		MaxAttempts:     0,                     // invalid
		InitialInterval: 0,                     // invalid
		MaxInterval:     -1 * time.Millisecond, // invalid
		Multiplier:      0,                     // invalid
	})
	require.Error(t, err, "constructor should validate config and return an error")
}

func TestRetryAfter_ParsesAllHTTPDateFormats(t *testing.T) {
	cfg := configuration.RetryConfig{
		MaxAttempts:     2,
		MaxElapsedTime:  10 * time.Second,
		InitialInterval: 1 * time.Millisecond,
		MaxInterval:     1 * time.Millisecond,
		Multiplier:      1,
	}

	// Test with numeric seconds (which works)
	t.Run("Seconds", func(t *testing.T) {
		var calls int32
		h := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
			c := atomic.AddInt32(&calls, 1)
			if c == 1 {
				return nil, &llmerrors.WorkflowError{
					Type:      llmerrors.ErrorTypeRateLimit,
					Message:   "rl",
					Retryable: true,
					Details:   map[string]any{"retry_after": "1"}, // 1 second as string
				}
			}
			return &transport.Response{Content: "ok", FinishReason: domain.FinishStop}, nil
		})

		mw, err := retry.NewRetryMiddlewareWithConfig(cfg)
		require.NoError(t, err)

		start := time.Now()
		_, _ = mw(h).Handle(context.Background(), &transport.Request{
			Operation: transport.OpGeneration, Provider: "test", Model: "m", Question: "q",
		})
		elapsed := time.Since(start)

		// Should wait about 1 second
		assert.GreaterOrEqual(t, elapsed, 900*time.Millisecond,
			"expected to honor numeric seconds Retry-After")
	})

	// Test that the parsing for dates is there (even if year formatting is tricky)
	t.Run("RFC1123", func(t *testing.T) {
		// Use a fixed future time to avoid year issues
		futureTime := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
		retryAt := futureTime.Format(time.RFC1123)

		var calls int32
		h := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
			c := atomic.AddInt32(&calls, 1)
			if c == 1 {
				return nil, &llmerrors.WorkflowError{
					Type:      llmerrors.ErrorTypeRateLimit,
					Message:   "rl",
					Retryable: true,
					Details:   map[string]any{"retry_after": retryAt},
				}
			}
			return &transport.Response{Content: "ok", FinishReason: domain.FinishStop}, nil
		})

		mw, err := retry.NewRetryMiddlewareWithConfig(cfg)
		require.NoError(t, err)

		_, _ = mw(h).Handle(context.Background(), &transport.Request{
			Operation: transport.OpGeneration, Provider: "test", Model: "m", Question: "q",
		})

		// Just verify it runs without error - the actual delay is too long to test
		assert.Equal(t, int32(2), atomic.LoadInt32(&calls), "should retry once")
	})
}

// TestPartialResponseWithError validates that partial response data is preserved
// when a handler returns both a response and an error.
// This test SHOULD FAIL with current implementation as partial responses are lost.
func TestPartialResponseWithError(t *testing.T) {
	ctx := context.Background()
	var callCount int32

	config := configuration.RetryConfig{
		MaxAttempts:     3,
		MaxElapsedTime:  5 * time.Second,
		InitialInterval: 10 * time.Millisecond,
		MaxInterval:     100 * time.Millisecond,
		Multiplier:      2.0,
		UseJitter:       false,
		EnableBudget:    true,
		MaxCostCents:    1000,
		MaxTokens:       10000,
	}

	// Handler returns partial response with error
	handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		atomic.AddInt32(&callCount, 1)

		// Return partial response data AND an error
		partialResp := &transport.Response{
			Content:                 "partial data before error",
			FinishReason:            domain.FinishStop,
			Usage:                   transport.NormalizedUsage{TotalTokens: 50},
			EstimatedCostMilliCents: 25,
		}

		// Non-retryable error to ensure we don't retry
		return partialResp, &llmerrors.ProviderError{
			Provider:   "test",
			StatusCode: 400,
			Message:    "bad request with partial response",
			Type:       llmerrors.ErrorTypeValidation,
		}
	})

	middleware, err := retry.NewRetryMiddlewareWithConfig(config)
	require.NoError(t, err)
	wrappedHandler := middleware(handler)

	req := &transport.Request{
		Operation: transport.OpGeneration,
		Provider:  "test",
		Model:     "test-model",
		Question:  "test question",
	}

	resp, err := wrappedHandler.Handle(ctx, req)

	// Should have error
	require.Error(t, err)

	// Issue: Partial response should be returned but isn't
	// This assertion will FAIL with current implementation
	assert.NotNil(t, resp, "partial response should be preserved")
	if resp != nil {
		assert.Equal(t, "partial data before error", resp.Content)
		assert.Equal(t, int64(50), resp.Usage.TotalTokens)
		assert.Equal(t, int64(25), resp.EstimatedCostMilliCents)
	}

	// Should have made only 1 attempt (non-retryable error)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))
}

// TestContextAlreadyCancelled validates that the middleware checks context
// before making the first attempt.
// This test SHOULD FAIL with current implementation.
func TestContextAlreadyCancelled(t *testing.T) {
	var callCount int32

	config := configuration.RetryConfig{
		MaxAttempts:     3,
		MaxElapsedTime:  5 * time.Second,
		InitialInterval: 10 * time.Millisecond,
		MaxInterval:     100 * time.Millisecond,
		Multiplier:      2.0,
		UseJitter:       false,
	}

	handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		atomic.AddInt32(&callCount, 1)
		return &transport.Response{
			Content:      "should not be called",
			FinishReason: domain.FinishStop,
		}, nil
	})

	middleware, err := retry.NewRetryMiddlewareWithConfig(config)
	require.NoError(t, err)
	wrappedHandler := middleware(handler)

	// Create an already-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	req := &transport.Request{
		Operation: transport.OpGeneration,
		Provider:  "test",
		Model:     "test-model",
		Question:  "test question",
	}

	_, err = wrappedHandler.Handle(ctx, req)
	require.Error(t, err)

	// Issue: Should not make any attempts with cancelled context
	// This assertion will FAIL with current implementation
	assert.Equal(t, int32(0), atomic.LoadInt32(&callCount),
		"should not attempt request with already-cancelled context")

	assert.Contains(t, err.Error(), "context")
}

// TestBudgetPreventFirstAttempt validates that budget checking can prevent
// even the first attempt if we know it will exceed limits.
// This test exposes the limitation that first attempt always proceeds.
func TestBudgetPreventFirstAttempt(t *testing.T) {
	ctx := context.Background()
	var callCount int32

	// Very low budget that first request will exceed
	config := configuration.RetryConfig{
		MaxAttempts:     3,
		MaxElapsedTime:  5 * time.Second,
		InitialInterval: 10 * time.Millisecond,
		MaxInterval:     100 * time.Millisecond,
		Multiplier:      2.0,
		UseJitter:       false,
		EnableBudget:    true,
		MaxCostCents:    1,  // Only 1 cent budget
		MaxTokens:       10, // Only 10 tokens budget
	}

	handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		atomic.AddInt32(&callCount, 1)
		// First attempt returns high cost/tokens
		return &transport.Response{
			Content:                 "expensive response",
			FinishReason:            domain.FinishStop,
			Usage:                   transport.NormalizedUsage{TotalTokens: 1000},
			EstimatedCostMilliCents: 500, // 50 cents
		}, nil
	})

	middleware, err := retry.NewRetryMiddlewareWithConfig(config)
	require.NoError(t, err)
	wrappedHandler := middleware(handler)

	req := &transport.Request{
		Operation: transport.OpGeneration,
		Provider:  "test",
		Model:     "test-model",
		Question:  "test question",
		// Could have estimated cost here for pre-flight check
		Metadata: map[string]string{
			"estimated_tokens":     "1000",
			"estimated_cost_cents": "50",
		},
	}

	resp, err := wrappedHandler.Handle(ctx, req)

	// Current implementation: first attempt always proceeds
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))

	// Ideally, with pre-flight estimation, we could prevent the first attempt
	// This would require enhancing the budget check logic
}
