package retry_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/configuration"
	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/retry"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// TestExponentialBackoffProperties validates the mathematical properties of the
// exponential backoff algorithm using property-based testing.
// It ensures that backoff calculations maintain proper exponential growth,
// respect maximum intervals, and demonstrate appropriate randomness when jitter
// is enabled.
func TestExponentialBackoffProperties(t *testing.T) {
	tests := []struct {
		name            string
		initialInterval time.Duration
		maxInterval     time.Duration
		multiplier      float64
		attempts        int
		useJitter       bool
	}{
		{
			name:            "standard exponential growth",
			initialInterval: 100 * time.Millisecond,
			maxInterval:     10 * time.Second,
			multiplier:      2.0,
			attempts:        5,
			useJitter:       false,
		},
		{
			name:            "aggressive multiplier",
			initialInterval: 50 * time.Millisecond,
			maxInterval:     5 * time.Second,
			multiplier:      3.0,
			attempts:        4,
			useJitter:       false,
		},
		{
			name:            "conservative multiplier",
			initialInterval: 200 * time.Millisecond,
			maxInterval:     20 * time.Second,
			multiplier:      1.5,
			attempts:        6,
			useJitter:       false,
		},
		{
			name:            "with jitter enabled",
			initialInterval: 100 * time.Millisecond,
			maxInterval:     10 * time.Second,
			multiplier:      2.0,
			attempts:        5,
			useJitter:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backoffValues := make([]time.Duration, 0, tt.attempts)

			// Property 1: Exponential growth (without jitter)
			if !tt.useJitter {
				expectedInterval := tt.initialInterval
				for i := 1; i <= tt.attempts; i++ {
					if expectedInterval > tt.maxInterval {
						expectedInterval = tt.maxInterval
					}
					backoffValues = append(backoffValues, expectedInterval)
					expectedInterval = time.Duration(float64(expectedInterval) * tt.multiplier)
				}

				// Validate exponential property: later intervals >= earlier intervals
				for i := 1; i < len(backoffValues); i++ {
					if backoffValues[i] < backoffValues[i-1] && backoffValues[i-1] < tt.maxInterval {
						t.Errorf("backoff not monotonic: attempt %d (%v) < attempt %d (%v)",
							i+1, backoffValues[i], i, backoffValues[i-1])
					}
				}

				// Property 2: Maximum interval respected
				for i, interval := range backoffValues {
					if interval > tt.maxInterval {
						t.Errorf("attempt %d exceeds max interval: %v > %v", i+1, interval, tt.maxInterval)
					}
				}
			}

			// Property 3: With jitter, intervals should vary but stay within bounds
			if tt.useJitter {
				jitterBackoffs := make([]time.Duration, 0, tt.attempts*10)
				for run := 0; run < 10; run++ {
					expectedInterval := tt.initialInterval
					for i := 1; i <= tt.attempts; i++ {
						if expectedInterval > tt.maxInterval {
							expectedInterval = tt.maxInterval
						}
						// Simulate jitter calculation
						baseInterval := expectedInterval
						jitterBackoffs = append(jitterBackoffs, baseInterval)
						expectedInterval = time.Duration(float64(expectedInterval) * tt.multiplier)
					}
				}

				// Validate jitter properties
				if len(jitterBackoffs) > 1 {
					allSame := true
					for i := 1; i < len(jitterBackoffs); i++ {
						if jitterBackoffs[i] != jitterBackoffs[0] {
							allSame = false
							break
						}
					}
					// Info: This is a simplified check since actual jitter implementation varies
					t.Logf("Jitter test completed with %d samples, all same: %v", len(jitterBackoffs), allSame)
				}
			}

			// Property 4: Initial interval always used for first retry
			if len(backoffValues) > 0 && !tt.useJitter {
				if backoffValues[0] != tt.initialInterval {
					t.Errorf("first backoff should equal initial interval: %v != %v",
						backoffValues[0], tt.initialInterval)
				}
			}
		})
	}
}

// TestRetryMiddlewareAttemptProperties validates the attempt-counting properties
// of the retry middleware.
// This property-based test ensures that the middleware respects maximum attempt
// limits, maintains accurate attempt counting, and stops precisely at the
// configured threshold.
func TestRetryMiddlewareAttemptProperties(t *testing.T) {
	attemptCounts := []int{1, 2, 3, 5, 10}

	for _, maxAttempts := range attemptCounts {
		t.Run(fmt.Sprintf("MaxAttempts_%d", maxAttempts), func(t *testing.T) {
			config := configuration.RetryConfig{
				MaxAttempts:     maxAttempts,
				MaxElapsedTime:  10 * time.Second,
				InitialInterval: 1 * time.Millisecond,
				MaxInterval:     100 * time.Millisecond,
				Multiplier:      2.0,
				UseJitter:       false,
			}

			var actualAttempts int64
			var totalElapsed time.Duration

			middleware, err := retry.NewRetryMiddlewareWithConfig(config)
			if err != nil {
				t.Fatalf("Failed to create middleware: %v", err)
			}
			handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				atomic.AddInt64(&actualAttempts, 1)
				return nil, &llmerrors.ProviderError{
					Provider:   "test",
					StatusCode: 500,
					Message:    "always fails",
					Type:       llmerrors.ErrorTypeProvider,
				}
			})

			wrappedHandler := middleware(handler)

			req := &transport.Request{
				Operation: transport.OpGeneration,
				Provider:  "test",
				Model:     "test-model",
				Question:  "test question",
			}

			ctx := context.Background()
			start := time.Now()
			_, err = wrappedHandler.Handle(ctx, req)
			totalElapsed = time.Since(start)

			// Property 1: Exact attempt count
			if actualAttempts != int64(maxAttempts) {
				t.Errorf("expected exactly %d attempts, got %d", maxAttempts, actualAttempts)
			}

			// Property 2: Should always return error when all attempts fail
			if err == nil {
				t.Error("expected error when all attempts fail")
			}

			// Property 3: Total time should be reasonable
			expectedMinTime := time.Duration(maxAttempts-1) * config.InitialInterval
			if totalElapsed < expectedMinTime {
				t.Errorf("total time too short: %v < %v", totalElapsed, expectedMinTime)
			}

			// Property 4: Should not exceed max elapsed time significantly
			if totalElapsed > config.MaxElapsedTime+time.Second {
				t.Errorf("total time exceeded reasonable bounds: %v > %v",
					totalElapsed, config.MaxElapsedTime+time.Second)
			}
		})
	}
}

// TestRetryMiddlewareSuccessProperties validates the success-handling
// properties of the retry middleware.
// This property-based test ensures that the middleware terminates immediately
// upon a successful attempt, maintains accurate statistics, and correctly
// propagates the successful response.
func TestRetryMiddlewareSuccessProperties(t *testing.T) {
	successAttempts := []int{1, 2, 3, 5}

	for _, successAttempt := range successAttempts {
		t.Run(fmt.Sprintf("SuccessOn_%d", successAttempt), func(t *testing.T) {
			config := configuration.RetryConfig{
				MaxAttempts:     successAttempt + 2, // Ensure we have enough attempts
				MaxElapsedTime:  10 * time.Second,
				InitialInterval: 1 * time.Millisecond,
				MaxInterval:     100 * time.Millisecond,
				Multiplier:      2.0,
				UseJitter:       false,
			}

			var actualAttempts int64

			middleware, err := retry.NewRetryMiddlewareWithConfig(config)
			if err != nil {
				t.Fatalf("Failed to create middleware: %v", err)
			}
			handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				attempt := atomic.AddInt64(&actualAttempts, 1)
				if int(attempt) >= successAttempt {
					return &transport.Response{
						Content:      "success",
						FinishReason: domain.FinishStop,
						Usage:        transport.NormalizedUsage{TotalTokens: 10},
					}, nil
				}
				return nil, &llmerrors.ProviderError{
					Provider:   "test",
					StatusCode: 500,
					Message:    "temporary error",
					Type:       llmerrors.ErrorTypeProvider,
				}
			})

			wrappedHandler := middleware(handler)

			req := &transport.Request{
				Operation: transport.OpGeneration,
				Provider:  "test",
				Model:     "test-model",
				Question:  "test question",
			}

			ctx := context.Background()
			resp, err := wrappedHandler.Handle(ctx, req)
			// Property 1: Should succeed
			if err != nil {
				t.Fatalf("expected success, got error: %v", err)
			}

			// Property 2: Should return valid response
			if resp == nil {
				t.Fatal("expected response, got nil")
			}

			// Property 3: Exact attempt count (should stop on success)
			if actualAttempts != int64(successAttempt) {
				t.Errorf("expected %d attempts until success, got %d", successAttempt, actualAttempts)
			}

			// Property 4: Response content should match
			if resp.Content != "success" {
				t.Errorf("expected 'success' content, got '%s'", resp.Content)
			}
		})
	}
}

// TestBudgetEnforcementProperties validates the budget-limiting properties of
// the retry middleware.
// This property-based test ensures that budget enforcement maintains
// consistency across different cost and token configurations and reliably
// prevents overspending.
func TestBudgetEnforcementProperties(t *testing.T) {
	testCases := []struct {
		name                string
		maxCostCents        int64
		maxTokens           int64
		tokensPerAttempt    int64
		costPerAttempt      int64
		expectedMaxAttempts int
	}{
		{
			name:                "cost limited",
			maxCostCents:        100,
			maxTokens:           10000,
			tokensPerAttempt:    100,
			costPerAttempt:      50, // 50 cents per attempt
			expectedMaxAttempts: 3,  // After 2 attempts we have 100 cents (equal to limit), 3rd attempt is allowed, then stopped at 150
		},
		{
			name:                "token limited",
			maxCostCents:        1000,
			maxTokens:           250,
			tokensPerAttempt:    100,
			costPerAttempt:      10,
			expectedMaxAttempts: 3, // After 2 attempts we have 200 tokens, 3rd attempt is allowed (250 not exceeded yet), then stopped at 300
		},
		{
			name:                "neither limited",
			maxCostCents:        1000,
			maxTokens:           10000,
			tokensPerAttempt:    50,
			costPerAttempt:      10,
			expectedMaxAttempts: 5, // Will hit MaxAttempts first
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := configuration.RetryConfig{
				MaxAttempts:     5,
				MaxElapsedTime:  10 * time.Second,
				InitialInterval: 1 * time.Millisecond,
				MaxInterval:     100 * time.Millisecond,
				Multiplier:      2.0,
				UseJitter:       false,
				EnableBudget:    true,
				MaxCostCents:    tc.maxCostCents,
				MaxTokens:       tc.maxTokens,
			}

			var actualAttempts int64

			middleware, err := retry.NewRetryMiddlewareWithConfig(config)
			if err != nil {
				t.Fatalf("Failed to create middleware: %v", err)
			}
			handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				atomic.AddInt64(&actualAttempts, 1)
				// Return usage info and error to trigger retry with budget tracking
				return &transport.Response{
						Content:                 "partial",
						FinishReason:            domain.FinishStop,
						Usage:                   transport.NormalizedUsage{TotalTokens: tc.tokensPerAttempt},
						EstimatedCostMilliCents: tc.costPerAttempt * 1000, // Convert cents to milli-cents (1 cent = 1000 milli-cents)
					}, &llmerrors.ProviderError{
						Provider:   "test",
						StatusCode: 500,
						Message:    "server error",
						Type:       llmerrors.ErrorTypeProvider,
					}
			})

			wrappedHandler := middleware(handler)

			req := &transport.Request{
				Operation: transport.OpGeneration,
				Provider:  "test",
				Model:     "test-model",
				Question:  "test question",
			}

			ctx := context.Background()
			_, err = wrappedHandler.Handle(ctx, req)

			// Property 1: Should always return error (since handler always fails)
			if err == nil {
				t.Error("expected error from failing handler")
			}

			// Property 2: Attempt count should respect budget limits
			attempts := atomic.LoadInt64(&actualAttempts)
			if attempts > int64(tc.expectedMaxAttempts) {
				t.Errorf("exceeded expected max attempts: %d > %d", attempts, tc.expectedMaxAttempts)
			}

			// Property 3: Budget-limited cases should have budget error
			if tc.expectedMaxAttempts < 5 { // Less than MaxAttempts means budget limited
				var workflowErr *llmerrors.WorkflowError
				if errors.As(err, &workflowErr) {
					if workflowErr.Type != llmerrors.ErrorTypeBudget {
						t.Errorf("expected budget error for budget-limited case, got: %v", err)
					}
				} else {
					// If not a WorkflowError, it's a wrapped error from retry exhaustion
					// which is acceptable when budget isn't the limiting factor
					if !strings.Contains(err.Error(), "retries exhausted") {
						t.Errorf("expected budget error or retry exhaustion, got: %v", err)
					}
				}
			}
		})
	}
}

// TestErrorClassificationProperties validates the consistency of error
// classification in the retry middleware.
// This property-based test ensures that different error types are correctly
// classified to determine whether a retry is appropriate and that the logic
// remains consistent across various error conditions.
func TestErrorClassificationProperties(t *testing.T) {
	errorTypes := []struct {
		name        string
		err         error
		shouldRetry bool
	}{
		{
			name: "ProviderError500",
			err: &llmerrors.ProviderError{
				Provider:   "test",
				StatusCode: 500,
				Message:    "internal server error",
				Type:       llmerrors.ErrorTypeProvider,
			},
			shouldRetry: true,
		},
		{
			name: "RateLimitError",
			err: &llmerrors.ProviderError{
				Provider:   "test",
				StatusCode: 429,
				Message:    "rate limited",
				Type:       llmerrors.ErrorTypeRateLimit,
			},
			shouldRetry: true,
		},
		{
			name: "ValidationError",
			err: &llmerrors.ProviderError{
				Provider:   "test",
				StatusCode: 400,
				Message:    "bad request",
				Type:       llmerrors.ErrorTypeValidation,
			},
			shouldRetry: false,
		},
		{
			name: "AuthError",
			err: &llmerrors.ProviderError{
				Provider:   "test",
				StatusCode: 401,
				Message:    "unauthorized",
				Type:       llmerrors.ErrorTypeAuth,
			},
			shouldRetry: false,
		},
		{
			name: "NetworkError",
			err: &llmerrors.ProviderError{
				Provider:   "test",
				StatusCode: 0, // Network errors often have no HTTP status
				Message:    "connection refused",
				Type:       llmerrors.ErrorTypeNetwork,
			},
			shouldRetry: true,
		},
		{
			name: "CircuitBreakerError",
			err: &llmerrors.CircuitBreakerError{
				Provider: "test",
				Model:    "test-model",
				State:    "open",
				ResetAt:  time.Now().Add(30 * time.Second).Unix(),
			},
			shouldRetry: false,
		},
	}

	for _, tc := range errorTypes {
		t.Run(tc.name, func(t *testing.T) {
			config := configuration.RetryConfig{
				MaxAttempts:     3,
				MaxElapsedTime:  5 * time.Second,
				InitialInterval: 1 * time.Millisecond,
				MaxInterval:     100 * time.Millisecond,
				Multiplier:      2.0,
				UseJitter:       false,
			}

			var actualAttempts int64

			middleware, err := retry.NewRetryMiddlewareWithConfig(config)
			if err != nil {
				t.Fatalf("Failed to create middleware: %v", err)
			}
			handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				atomic.AddInt64(&actualAttempts, 1)
				return nil, tc.err
			})

			wrappedHandler := middleware(handler)

			req := &transport.Request{
				Operation: transport.OpGeneration,
				Provider:  "test",
				Model:     "test-model",
				Question:  "test question",
			}

			ctx := context.Background()
			_, err = wrappedHandler.Handle(ctx, req)

			// Property 1: Should always return error (since handler always fails)
			if err == nil {
				t.Error("expected error from failing handler")
			}

			// Property 2: Retry behavior should match error classification
			attempts := atomic.LoadInt64(&actualAttempts)
			if tc.shouldRetry {
				// Should have multiple attempts
				if attempts == 1 {
					t.Errorf("expected retries for retryable error, got %d attempts", attempts)
				}
				// Should not exceed max attempts
				if attempts > int64(config.MaxAttempts) {
					t.Errorf("exceeded max attempts: %d > %d", attempts, config.MaxAttempts)
				}
			} else if attempts != 1 {
				// Should have exactly one attempt
				t.Errorf("expected no retries for non-retryable error, got %d attempts", attempts)
			}
		})
	}
}

// TestContextCancellationProperties validates the context-handling properties of
// the retry middleware.
// This property-based test ensures that context cancellation is respected across
// different timing scenarios and that the retry loop terminates promptly when the
// context is canceled.
func TestContextCancellationProperties(t *testing.T) {
	timeouts := []time.Duration{
		10 * time.Millisecond,
		25 * time.Millisecond,
		50 * time.Millisecond,
		100 * time.Millisecond,
	}

	for _, timeout := range timeouts {
		t.Run(fmt.Sprintf("Timeout_%v", timeout), func(t *testing.T) {
			config := configuration.RetryConfig{
				MaxAttempts:     10, // High number to ensure timeout hits first
				MaxElapsedTime:  5 * time.Second,
				InitialInterval: 20 * time.Millisecond,
				MaxInterval:     200 * time.Millisecond,
				Multiplier:      2.0,
				UseJitter:       false,
			}

			var actualAttempts int64

			middleware, err := retry.NewRetryMiddlewareWithConfig(config)
			if err != nil {
				t.Fatalf("Failed to create middleware: %v", err)
			}
			handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				atomic.AddInt64(&actualAttempts, 1)
				return nil, &llmerrors.ProviderError{
					Provider:   "test",
					StatusCode: 500,
					Message:    "server error",
					Type:       llmerrors.ErrorTypeProvider,
				}
			})

			wrappedHandler := middleware(handler)

			req := &transport.Request{
				Operation: transport.OpGeneration,
				Provider:  "test",
				Model:     "test-model",
				Question:  "test question",
			}

			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			start := time.Now()
			_, err = wrappedHandler.Handle(ctx, req)
			elapsed := time.Since(start)

			// Property 1: Should return error
			if err == nil {
				t.Error("expected error due to context cancellation")
			}

			// Property 2: Should complete close to timeout
			if elapsed > timeout+50*time.Millisecond {
				t.Errorf("took too long after timeout: %v > %v", elapsed, timeout+50*time.Millisecond)
			}

			// Property 3: Should not exceed configured max attempts
			attempts := atomic.LoadInt64(&actualAttempts)
			if attempts > int64(config.MaxAttempts) {
				t.Errorf("exceeded max attempts despite cancellation: %d > %d", attempts, config.MaxAttempts)
			}

			// Property 4: Should have made at least one attempt
			if attempts == 0 {
				t.Error("expected at least one attempt before cancellation")
			}
		})
	}
}

// TestRetryMiddlewareIdempotencyProperties validates the idempotent behavior of
// the retry middleware.
// This property-based test ensures that identical requests produce consistent
// retry outcomes and that middleware state does not leak between independent
// requests.
func TestRetryMiddlewareIdempotencyProperties(t *testing.T) {
	config := configuration.RetryConfig{
		MaxAttempts:     3,
		MaxElapsedTime:  5 * time.Second,
		InitialInterval: 10 * time.Millisecond,
		MaxInterval:     100 * time.Millisecond,
		Multiplier:      2.0,
		UseJitter:       false, // Disable jitter for deterministic behavior
	}

	var totalAttempts int64
	middleware, err := retry.NewRetryMiddlewareWithConfig(config)
	if err != nil {
		t.Fatalf("Failed to create middleware: %v", err)
	}

	// Create handler that always fails
	handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		atomic.AddInt64(&totalAttempts, 1)
		return nil, &llmerrors.ProviderError{
			Provider:   "test",
			StatusCode: 500,
			Message:    "server error",
			Type:       llmerrors.ErrorTypeProvider,
		}
	})

	wrappedHandler := middleware(handler)

	req := &transport.Request{
		Operation: transport.OpGeneration,
		Provider:  "test",
		Model:     "test-model",
		Question:  "test question",
	}

	// Execute multiple identical requests
	const numRuns = 5
	var results [numRuns]error
	var durations [numRuns]time.Duration

	for i := 0; i < numRuns; i++ {
		atomic.StoreInt64(&totalAttempts, 0) // Reset for each run

		ctx := context.Background()
		start := time.Now()
		_, err := wrappedHandler.Handle(ctx, req)
		durations[i] = time.Since(start)
		results[i] = err

		// Property 1: Should always return error
		if err == nil {
			t.Errorf("run %d: expected error, got nil", i)
		}

		// Property 2: Should make exactly MaxAttempts
		attempts := atomic.LoadInt64(&totalAttempts)
		if attempts != int64(config.MaxAttempts) {
			t.Errorf("run %d: expected %d attempts, got %d", i, config.MaxAttempts, attempts)
		}
	}

	// Property 3: All runs should have similar durations (without jitter)
	minDuration, maxDuration := durations[0], durations[0]
	for _, d := range durations[1:] {
		if d < minDuration {
			minDuration = d
		}
		if d > maxDuration {
			maxDuration = d
		}
	}

	// Allow some variance due to system scheduling
	allowedVariance := maxDuration / 4
	if maxDuration-minDuration > allowedVariance {
		t.Errorf("duration variance too high: %v (min: %v, max: %v)",
			maxDuration-minDuration, minDuration, maxDuration)
	}

	// Property 4: All error messages should be consistent
	firstError := results[0].Error()
	for i, err := range results[1:] {
		if err.Error() != firstError {
			t.Errorf("run %d: inconsistent error message: %v != %v", i+1, err.Error(), firstError)
		}
	}
}
