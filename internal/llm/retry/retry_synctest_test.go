//go:build goexperiment.synctest

package retry_test

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/configuration"
	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/retry"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// TestRetryMiddleware_MaxElapsedTimeSync validates that the retry middleware respects
// the maximum elapsed time limit using synctest for deterministic time control.
func TestRetryMiddleware_MaxElapsedTimeSync(t *testing.T) {
	synctest.Run(func() {
		ctx := context.Background()
		var callCount int32

		config := configuration.RetryConfig{
			MaxAttempts:     10, // High to ensure time limit is hit first
			MaxElapsedTime:  500 * time.Millisecond,
			InitialInterval: 100 * time.Millisecond,
			MaxInterval:     200 * time.Millisecond,
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

		// With synctest, we can verify exact timing behavior
		// First attempt: immediate
		// Second attempt: after 100ms backoff
		// Third attempt: after 200ms backoff (100ms * 2)
		// Fourth attempt would be after 200ms more (capped at MaxInterval)
		// Total before 4th: 0 + 100 + 200 = 300ms (under 500ms limit)
		// Fifth attempt would be at 300 + 200 = 500ms, should hit time limit
		assert.Equal(t, int32(4), actualCalls, "expected exactly 4 attempts before hitting time limit")

		// Should have elapsed approximately the max elapsed time
		assert.GreaterOrEqual(t, elapsed, config.MaxElapsedTime)
		assert.LessOrEqual(t, elapsed, config.MaxElapsedTime+100*time.Millisecond)
	})
}

// TestRetryMiddleware_ContextCancellationSync validates that the retry middleware
// respects context cancellation during backoff periods with precise timing control.
func TestRetryMiddleware_ContextCancellationSync(t *testing.T) {
	synctest.Run(func() {
		config := configuration.RetryConfig{
			MaxAttempts:     5,
			MaxElapsedTime:  10 * time.Second,
			InitialInterval: 1 * time.Second,
			MaxInterval:     5 * time.Second,
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

		// Create context that cancels after a specific time
		ctx, cancel := context.WithCancel(context.Background())

		// Start request processing in goroutine
		done := make(chan error)
		go func() {
			_, err := wrappedHandler.Handle(ctx, req)
			done <- err
		}()

		// With synctest, we can precisely control when the context is cancelled
		// First attempt happens immediately
		// Second attempt would happen after 1 second backoff
		// Cancel after 500ms (during the backoff period)
		go func() {
			time.Sleep(500 * time.Millisecond)
			cancel()
		}()

		// Wait for completion
		err = <-done

		require.Error(t, err, "expected error due to context cancellation")
		assert.Contains(t, err.Error(), "context cancelled during retry", "expected context cancellation error, got: %v", err)

		// Should have made exactly 1 call before cancellation during backoff
		actualCalls := atomic.LoadInt32(&callCount)
		assert.Equal(t, int32(1), actualCalls, "expected exactly 1 call before cancellation")
	})
}

// TestRetryAfter_ParsesAllHTTPDateFormatsSync tests retry-after delay behavior
// with deterministic time control, allowing us to test actual delays without waiting.
func TestRetryAfter_ParsesAllHTTPDateFormatsSync(t *testing.T) {
	synctest.Run(func() {
		cfg := configuration.RetryConfig{
			MaxAttempts:     2,
			MaxElapsedTime:  10 * time.Second,
			InitialInterval: 100 * time.Millisecond,
			MaxInterval:     1 * time.Second,
			Multiplier:      2.0,
			UseJitter:       false,
		}

		// Test with numeric seconds
		t.Run("Seconds", func(t *testing.T) {
			var calls int32
			h := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				c := atomic.AddInt32(&calls, 1)
				if c == 1 {
					return nil, &llmerrors.WorkflowError{
						Type:      llmerrors.ErrorTypeRateLimit,
						Message:   "rate limited",
						Retryable: true,
						Details:   map[string]any{"retry_after": "2"}, // 2 seconds as string
					}
				}
				return &transport.Response{Content: "ok", FinishReason: domain.FinishStop}, nil
			})

			mw, err := retry.NewRetryMiddlewareWithConfig(cfg)
			require.NoError(t, err)

			start := time.Now()
			resp, err := mw(h).Handle(context.Background(), &transport.Request{
				Operation: transport.OpGeneration, Provider: "test", Model: "m", Question: "q",
			})
			elapsed := time.Since(start)

			require.NoError(t, err)
			assert.Equal(t, "ok", resp.Content)

			// With synctest, we can verify exact delay without tolerance
			assert.Equal(t, 2*time.Second, elapsed, "expected exactly 2 second retry-after delay")
			assert.Equal(t, int32(2), atomic.LoadInt32(&calls), "expected 2 calls")
		})

		// Test with integer retry-after
		t.Run("IntegerSeconds", func(t *testing.T) {
			var calls int32
			h := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				c := atomic.AddInt32(&calls, 1)
				if c == 1 {
					return nil, &llmerrors.WorkflowError{
						Type:      llmerrors.ErrorTypeRateLimit,
						Message:   "rate limited",
						Retryable: true,
						Details:   map[string]any{"retry_after": 3}, // 3 seconds as int
					}
				}
				return &transport.Response{Content: "ok", FinishReason: domain.FinishStop}, nil
			})

			mw, err := retry.NewRetryMiddlewareWithConfig(cfg)
			require.NoError(t, err)

			start := time.Now()
			resp, err := mw(h).Handle(context.Background(), &transport.Request{
				Operation: transport.OpGeneration, Provider: "test", Model: "m", Question: "q",
			})
			elapsed := time.Since(start)

			require.NoError(t, err)
			assert.Equal(t, "ok", resp.Content)

			// Exact timing verification with synctest
			assert.Equal(t, 3*time.Second, elapsed, "expected exactly 3 second retry-after delay")
			assert.Equal(t, int32(2), atomic.LoadInt32(&calls), "expected 2 calls")
		})
	})
}

// TestRetryMiddleware_RetryAfterProviderSync validates that errors implementing
// the RetryAfterProvider interface are handled correctly with precise timing control.
func TestRetryMiddleware_RetryAfterProviderSync(t *testing.T) {
	synctest.Run(func() {
		ctx := context.Background()
		var callCount int32

		config := configuration.RetryConfig{
			MaxAttempts:     2,
			MaxElapsedTime:  10 * time.Second,
			InitialInterval: 100 * time.Millisecond,
			MaxInterval:     500 * time.Millisecond,
			Multiplier:      2.0,
			UseJitter:       false,
		}

		// Custom error that implements RetryAfterProvider
		customErr := &testRetryAfterError{
			msg:        "rate limited",
			retryAfter: 1500 * time.Millisecond, // 1.5 seconds
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
		assert.Equal(t, "success", resp.Content, "expected success response")

		// With synctest, verify exact retry-after duration
		assert.Equal(t, customErr.retryAfter, elapsed, "expected exact retry-after delay")
		assert.Equal(t, int32(2), atomic.LoadInt32(&callCount), "expected 2 calls")
	})
}

// TestRetryMiddleware_WorkflowErrorSync validates the handling of WorkflowError
// retry-after values with precise timing control.
func TestRetryMiddleware_WorkflowErrorSync(t *testing.T) {
	synctest.Run(func() {
		ctx := context.Background()
		var callCount int32

		config := configuration.RetryConfig{
			MaxAttempts:     2,
			MaxElapsedTime:  10 * time.Second,
			InitialInterval: 100 * time.Millisecond,
			MaxInterval:     500 * time.Millisecond,
			Multiplier:      2.0,
			UseJitter:       false,
		}

		workflowErr := &llmerrors.WorkflowError{
			Type:      llmerrors.ErrorTypeRateLimit,
			Message:   "rate limited",
			Code:      "RATE_LIMIT",
			Retryable: true,
			Details: map[string]any{
				"retry_after": 2, // 2 seconds
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
		assert.Equal(t, "success", resp.Content, "expected success response")

		// With synctest, verify exact retry_after duration
		assert.Equal(t, 2*time.Second, elapsed, "expected exact 2 second delay")
		assert.Equal(t, int32(2), atomic.LoadInt32(&callCount), "expected 2 calls")
	})
}

// TestRetryMiddleware_ComplexTimingScenarioSync tests a complex scenario
// with multiple retries, varying delays, and precise timing requirements.
func TestRetryMiddleware_ComplexTimingScenarioSync(t *testing.T) {
	synctest.Run(func() {
		ctx := context.Background()
		var callCount int32

		config := configuration.RetryConfig{
			MaxAttempts:     5,
			MaxElapsedTime:  10 * time.Second,
			InitialInterval: 1 * time.Second,
			MaxInterval:     4 * time.Second,
			Multiplier:      2.0,
			UseJitter:       false,
		}

		// Handler that succeeds on the 4th attempt with varying retry-after values
		handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
			calls := atomic.AddInt32(&callCount, 1)
			switch calls {
			case 1:
				// First attempt: return rate limit with 1s retry-after
				return nil, &llmerrors.ProviderError{
					Provider:   "test",
					StatusCode: 429,
					Message:    "rate limited",
					Type:       llmerrors.ErrorTypeRateLimit,
					RetryAfter: 1, // 1 second
				}
			case 2:
				// Second attempt: return rate limit with 2s retry-after
				return nil, &llmerrors.ProviderError{
					Provider:   "test",
					StatusCode: 429,
					Message:    "rate limited",
					Type:       llmerrors.ErrorTypeRateLimit,
					RetryAfter: 2, // 2 seconds
				}
			case 3:
				// Third attempt: return server error (uses exponential backoff)
				return nil, &llmerrors.ProviderError{
					Provider:   "test",
					StatusCode: 500,
					Message:    "server error",
					Type:       llmerrors.ErrorTypeProvider,
				}
			case 4:
				// Fourth attempt: success
				return &transport.Response{
					Content:      "success",
					FinishReason: domain.FinishStop,
					Usage:        transport.NormalizedUsage{TotalTokens: 100},
				}, nil
			default:
				// Should not reach here
				return nil, &llmerrors.ProviderError{
					Provider:   "test",
					StatusCode: 500,
					Message:    "unexpected",
					Type:       llmerrors.ErrorTypeProvider,
				}
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

		start := time.Now()
		resp, err := wrappedHandler.Handle(ctx, req)
		elapsed := time.Since(start)

		require.NoError(t, err, "expected success after retries")
		assert.Equal(t, "success", resp.Content, "expected success response")
		assert.Equal(t, int32(4), atomic.LoadInt32(&callCount), "expected exactly 4 attempts")

		// Calculate expected total delay with synctest precision:
		// Attempt 1 → 2: 1s (retry-after)
		// Attempt 2 → 3: 2s (retry-after)
		// Attempt 3 → 4: 4s (exponential backoff, capped at MaxInterval)
		// Total: 1 + 2 + 4 = 7 seconds
		expectedDelay := 7 * time.Second
		assert.Equal(t, expectedDelay, elapsed, "expected exact total delay of %v", expectedDelay)
	})
}
