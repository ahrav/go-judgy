//go:build ignore

// These race tests use unrealistic timing and are flaky.
// Use the synctest versions instead for reliable timing-dependent tests.

package retry_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/configuration"
	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/retry"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// TestRetryMiddleware_ConcurrentStatisticsRace validates that the retry middleware's
// internal statistics are updated safely under concurrent access.
// It launches multiple goroutines to simulate high-load scenarios and ensures
// that atomic operations prevent race conditions.
func TestRetryMiddleware_ConcurrentStatisticsRace(t *testing.T) {
	config := configuration.RetryConfig{
		MaxAttempts:     3,
		MaxElapsedTime:  5 * time.Second,
		InitialInterval: 1 * time.Millisecond,
		MaxInterval:     10 * time.Millisecond,
		Multiplier:      2.0,
		UseJitter:       false,
	}

	var callCount int64
	handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		calls := atomic.AddInt64(&callCount, 1)
		if calls <= 6 { // Succeed after 2 attempts for first 3 requests
			if calls%2 == 0 {
				return &transport.Response{
					Content:      "success",
					FinishReason: domain.FinishStop,
					Usage:        transport.NormalizedUsage{TotalTokens: 10},
				}, nil
			}
		}
		return nil, &llmerrors.ProviderError{
			Provider:   "test",
			StatusCode: 500,
			Message:    "server error",
			Type:       llmerrors.ErrorTypeProvider,
		}
	})

	middleware, err := retry.NewRetryMiddlewareWithConfig(config)
	if err != nil {
		t.Fatalf("Failed to create middleware: %v", err)
	}
	wrappedHandler := middleware(handler)

	req := &transport.Request{
		Operation: transport.OpGeneration,
		Provider:  "test",
		Model:     "test-model",
		Question:  "test question",
	}

	// Launch concurrent requests to test race conditions
	const numGoroutines = 50
	var wg sync.WaitGroup
	results := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			_, err := wrappedHandler.Handle(ctx, req)
			results <- err
		}()
	}

	wg.Wait()
	close(results)

	// Count results
	var successCount, errorCount int
	for err := range results {
		if err == nil {
			successCount++
		} else {
			errorCount++
		}
	}

	// Validate we handled all requests
	totalRequests := successCount + errorCount
	if totalRequests != numGoroutines {
		t.Errorf("expected %d total requests, got %d", numGoroutines, totalRequests)
	}

	// Validate statistics are reasonable
	totalCalls := atomic.LoadInt64(&callCount)
	expectedMinCalls := int64(numGoroutines) // At least one call per request
	expectedMaxCalls := int64(numGoroutines * config.MaxAttempts)

	if totalCalls < expectedMinCalls || totalCalls > expectedMaxCalls {
		t.Errorf("expected calls between %d and %d, got %d", expectedMinCalls, expectedMaxCalls, totalCalls)
	}
}

// TestRetryMiddleware_ConcurrentBudgetTracking validates that the retry middleware's
// budget enforcement is thread-safe.
// It simulates concurrent requests that consume budget and verifies that atomic
// operations correctly prevent race conditions in cost and token accounting.
func TestRetryMiddleware_ConcurrentBudgetTracking(t *testing.T) {
	config := configuration.RetryConfig{
		MaxAttempts:     3,
		MaxElapsedTime:  5 * time.Second,
		InitialInterval: 1 * time.Millisecond,
		MaxInterval:     10 * time.Millisecond,
		Multiplier:      2.0,
		UseJitter:       false,
		EnableBudget:    true,
		MaxCostCents:    1000, // $10.00 total
		MaxTokens:       50000,
	}

	const tokensPerCall = 1000
	const costPerCall = 50 // 50 centi-cents

	handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		// Always return usage info with error to test budget tracking
		return &transport.Response{
				Content:                 "partial",
				FinishReason:            domain.FinishStop,
				Usage:                   transport.NormalizedUsage{TotalTokens: tokensPerCall},
				EstimatedCostMilliCents: costPerCall * 10, // Convert to milli-cents
			}, &llmerrors.ProviderError{
				Provider:   "test",
				StatusCode: 500,
				Message:    "server error",
				Type:       llmerrors.ErrorTypeProvider,
			}
	})

	middleware, err := retry.NewRetryMiddlewareWithConfig(config)
	if err != nil {
		t.Fatalf("Failed to create middleware: %v", err)
	}
	wrappedHandler := middleware(handler)

	req := &transport.Request{
		Operation: transport.OpGeneration,
		Provider:  "test",
		Model:     "test-model",
		Question:  "test question",
	}

	// Launch concurrent requests
	const numGoroutines = 20
	var wg sync.WaitGroup
	results := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			_, err := wrappedHandler.Handle(ctx, req)
			results <- err
		}()
	}

	wg.Wait()
	close(results)

	// Count budget errors vs other errors
	var budgetErrors, otherErrors int
	for err := range results {
		if err != nil {
			if workflowErr, ok := err.(*llmerrors.WorkflowError); ok && workflowErr.Type == llmerrors.ErrorTypeBudget {
				budgetErrors++
			} else {
				otherErrors++
			}
		}
	}

	// Should have some budget errors due to concurrent spending
	if budgetErrors == 0 {
		t.Error("expected some budget errors in concurrent scenario")
	}

	// Total errors should equal number of goroutines (all should fail)
	totalErrors := budgetErrors + otherErrors
	if totalErrors != numGoroutines {
		t.Errorf("expected %d total errors, got %d", numGoroutines, totalErrors)
	}
}

// TestRetryMiddleware_ConcurrentBackoffMetrics validates that backoff duration
// calculations, including jitter, are thread-safe.
// It stress-tests the middleware with concurrent requests that trigger retries,
// ensuring that random number generation and statistics updates are free of race
// conditions.
func TestRetryMiddleware_ConcurrentBackoffMetrics(t *testing.T) {
	config := configuration.RetryConfig{
		MaxAttempts:     5,
		MaxElapsedTime:  2 * time.Second,
		InitialInterval: 10 * time.Millisecond,
		MaxInterval:     100 * time.Millisecond,
		Multiplier:      2.0,
		UseJitter:       true, // Test concurrent jitter calculation
	}

	var callCount int64
	handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		atomic.AddInt64(&callCount, 1)
		return nil, &llmerrors.ProviderError{
			Provider:   "test",
			StatusCode: 500,
			Message:    "server error",
			Type:       llmerrors.ErrorTypeProvider,
		}
	})

	middleware, err := retry.NewRetryMiddlewareWithConfig(config)
	if err != nil {
		t.Fatalf("Failed to create middleware: %v", err)
	}
	wrappedHandler := middleware(handler)

	req := &transport.Request{
		Operation: transport.OpGeneration,
		Provider:  "test",
		Model:     "test-model",
		Question:  "test question",
	}

	// Launch concurrent requests to stress test backoff calculations
	const numGoroutines = 30
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			_, _ = wrappedHandler.Handle(ctx, req) // Ignore errors, focus on concurrency
		}()
	}

	wg.Wait()

	// Verify all requests were processed
	totalCalls := atomic.LoadInt64(&callCount)
	expectedMinCalls := int64(numGoroutines) // At least one call per request
	if totalCalls < expectedMinCalls {
		t.Errorf("expected at least %d calls, got %d", expectedMinCalls, totalCalls)
	}
}

// TestRetryMiddleware_ConcurrentCircuitBreakerInteraction validates the retry
// middleware's behavior when interacting with a circuit breaker under concurrent load.
// It ensures that the middleware correctly classifies circuit breaker errors and
// avoids retrying requests when the circuit is open.
func TestRetryMiddleware_ConcurrentCircuitBreakerInteraction(t *testing.T) {
	config := configuration.RetryConfig{
		MaxAttempts:     3,
		MaxElapsedTime:  2 * time.Second,
		InitialInterval: 1 * time.Millisecond,
		MaxInterval:     10 * time.Millisecond,
		Multiplier:      2.0,
		UseJitter:       false,
	}

	var errorToggle int64
	handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		// Alternate between different error types
		if atomic.AddInt64(&errorToggle, 1)%2 == 0 {
			return nil, &llmerrors.CircuitBreakerError{
				Provider: "test",
				Model:    "test-model",
				State:    "open",
				ResetAt:  time.Now().Add(30 * time.Second).Unix(),
			}
		}
		return nil, &llmerrors.ProviderError{
			Provider:   "test",
			StatusCode: 500,
			Message:    "server error",
			Type:       llmerrors.ErrorTypeProvider,
		}
	})

	middleware, err := retry.NewRetryMiddlewareWithConfig(config)
	if err != nil {
		t.Fatalf("Failed to create middleware: %v", err)
	}
	wrappedHandler := middleware(handler)

	req := &transport.Request{
		Operation: transport.OpGeneration,
		Provider:  "test",
		Model:     "test-model",
		Question:  "test question",
	}

	// Launch concurrent requests
	const numGoroutines = 20
	var wg sync.WaitGroup
	results := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			_, err := wrappedHandler.Handle(ctx, req)
			results <- err
		}()
	}

	wg.Wait()
	close(results)

	// Classify error types
	var cbErrors, providerErrors, otherErrors int
	for err := range results {
		if err == nil {
			continue
		}

		switch err.(type) {
		case *llmerrors.CircuitBreakerError:
			cbErrors++
		case *llmerrors.ProviderError:
			providerErrors++
		default:
			otherErrors++
		}
	}

	// Should have mix of both error types
	if cbErrors == 0 {
		t.Error("expected some circuit breaker errors")
	}
	if providerErrors == 0 {
		t.Error("expected some provider errors")
	}

	// Total should equal number of goroutines
	total := cbErrors + providerErrors + otherErrors
	if total != numGoroutines {
		t.Errorf("expected %d total errors, got %d", numGoroutines, total)
	}
}

// TestRetryMiddleware_ConcurrentContextCancellation validates the retry middleware's
// handling of context cancellation during concurrent operations.
// It launches multiple requests with short-lived contexts to ensure that retries
// are properly aborted and resources are cleaned up when a context is canceled.
func TestRetryMiddleware_ConcurrentContextCancellation(t *testing.T) {
	config := configuration.RetryConfig{
		MaxAttempts:     5,
		MaxElapsedTime:  5 * time.Second,
		InitialInterval: 50 * time.Millisecond,
		MaxInterval:     200 * time.Millisecond,
		Multiplier:      2.0,
		UseJitter:       false,
	}

	var callCount int64
	handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		atomic.AddInt64(&callCount, 1)
		return nil, &llmerrors.ProviderError{
			Provider:   "test",
			StatusCode: 500,
			Message:    "server error",
			Type:       llmerrors.ErrorTypeProvider,
		}
	})

	middleware, err := retry.NewRetryMiddlewareWithConfig(config)
	if err != nil {
		t.Fatalf("Failed to create middleware: %v", err)
	}
	wrappedHandler := middleware(handler)

	req := &transport.Request{
		Operation: transport.OpGeneration,
		Provider:  "test",
		Model:     "test-model",
		Question:  "test question",
	}

	// Launch concurrent requests with short timeouts
	const numGoroutines = 20
	var wg sync.WaitGroup
	results := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each request gets a short timeout
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
			defer cancel()

			_, err := wrappedHandler.Handle(ctx, req)
			results <- err
		}()
	}

	wg.Wait()
	close(results)

	// Count cancellation vs other errors
	var cancellationErrors, otherErrors int
	for err := range results {
		if err != nil {
			if err == context.DeadlineExceeded || err == context.Canceled {
				cancellationErrors++
			} else {
				otherErrors++
			}
		}
	}

	// Should have some cancellation errors
	if cancellationErrors == 0 {
		t.Error("expected some context cancellation errors")
	}

	// Verify call count is reasonable (should be limited by cancellations)
	totalCalls := atomic.LoadInt64(&callCount)
	maxExpectedCalls := int64(numGoroutines * config.MaxAttempts)
	if totalCalls >= maxExpectedCalls {
		t.Errorf("expected fewer than %d calls due to cancellation, got %d", maxExpectedCalls, totalCalls)
	}
}

// TestRetryMiddleware_ConcurrentSuccessAfterRetry validates that the retry middleware
// correctly handles requests that succeed after one or more attempts under
// concurrent load.
// It ensures that statistics are tracked accurately and that successful responses
// are propagated correctly, even when mixed with failing requests.
func TestRetryMiddleware_ConcurrentSuccessAfterRetry(t *testing.T) {
	config := configuration.RetryConfig{
		MaxAttempts:     4,
		MaxElapsedTime:  3 * time.Second,
		InitialInterval: 1 * time.Millisecond,
		MaxInterval:     10 * time.Millisecond,
		Multiplier:      2.0,
		UseJitter:       false,
	}

	var callCount int64
	handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		calls := atomic.AddInt64(&callCount, 1)

		// Different success patterns for different goroutines
		switch calls % 10 {
		case 1, 3, 5, 7, 9: // Odd numbered calls succeed immediately
			return &transport.Response{
				Content:      "immediate success",
				FinishReason: domain.FinishStop,
				Usage:        transport.NormalizedUsage{TotalTokens: 10},
			}, nil
		case 2, 6: // These calls succeed on 2nd attempt
			if calls%2 == 0 {
				return &transport.Response{
					Content:      "success after retry",
					FinishReason: domain.FinishStop,
					Usage:        transport.NormalizedUsage{TotalTokens: 15},
				}, nil
			}
		case 4, 8: // These calls succeed on 3rd attempt
			if calls%3 == 1 {
				return &transport.Response{
					Content:      "success after 2 retries",
					FinishReason: domain.FinishStop,
					Usage:        transport.NormalizedUsage{TotalTokens: 20},
				}, nil
			}
		}

		return nil, &llmerrors.ProviderError{
			Provider:   "test",
			StatusCode: 500,
			Message:    "server error",
			Type:       llmerrors.ErrorTypeProvider,
		}
	})

	middleware, err := retry.NewRetryMiddlewareWithConfig(config)
	if err != nil {
		t.Fatalf("Failed to create middleware: %v", err)
	}
	wrappedHandler := middleware(handler)

	req := &transport.Request{
		Operation: transport.OpGeneration,
		Provider:  "test",
		Model:     "test-model",
		Question:  "test question",
	}

	// Launch concurrent requests
	const numGoroutines = 30
	var wg sync.WaitGroup
	results := make(chan error, numGoroutines)
	responses := make(chan *transport.Response, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			resp, err := wrappedHandler.Handle(ctx, req)
			results <- err
			responses <- resp
		}()
	}

	wg.Wait()
	close(results)
	close(responses)

	// Count successes vs failures
	var successCount, errorCount int
	var responseCount int

	for err := range results {
		if err == nil {
			successCount++
		} else {
			errorCount++
		}
	}

	for resp := range responses {
		if resp != nil {
			responseCount++
		}
	}

	// Validate results
	if successCount != responseCount {
		t.Errorf("success count (%d) should match response count (%d)", successCount, responseCount)
	}

	if successCount+errorCount != numGoroutines {
		t.Errorf("expected %d total requests, got %d", numGoroutines, successCount+errorCount)
	}

	// Should have both successes and errors due to mixed patterns
	if successCount == 0 {
		t.Error("expected some successful requests")
	}
}

// TestRetryMiddleware_ConcurrentDifferentProviders validates that the retry middleware
// correctly handles concurrent requests to different providers.
// It ensures that provider-specific error types are classified correctly and that
// retry logic is applied appropriately for each provider.
func TestRetryMiddleware_ConcurrentDifferentProviders(t *testing.T) {
	config := configuration.RetryConfig{
		MaxAttempts:     3,
		MaxElapsedTime:  2 * time.Second,
		InitialInterval: 1 * time.Millisecond,
		MaxInterval:     10 * time.Millisecond,
		Multiplier:      2.0,
		UseJitter:       false,
	}

	handler := transport.HandlerFunc(func(_ context.Context, req *transport.Request) (*transport.Response, error) {
		// Different error patterns for different providers
		switch req.Provider {
		case "openai":
			return nil, &llmerrors.ProviderError{
				Provider:   "openai",
				StatusCode: 429,
				Message:    "rate limited",
				Type:       llmerrors.ErrorTypeRateLimit,
			}
		case "anthropic":
			return nil, &llmerrors.ProviderError{
				Provider:   "anthropic",
				StatusCode: 500,
				Message:    "server error",
				Type:       llmerrors.ErrorTypeProvider,
			}
		case "google":
			return nil, &llmerrors.ProviderError{
				Provider:   "google",
				StatusCode: 503,
				Message:    "service unavailable",
				Type:       llmerrors.ErrorTypeProvider,
			}
		}

		return nil, &llmerrors.ProviderError{
			Provider:   req.Provider,
			StatusCode: 500,
			Message:    "unknown provider error",
			Type:       llmerrors.ErrorTypeProvider,
		}
	})

	middleware, err := retry.NewRetryMiddlewareWithConfig(config)
	if err != nil {
		t.Fatalf("Failed to create middleware: %v", err)
	}
	wrappedHandler := middleware(handler)

	providers := []string{"openai", "anthropic", "google"}

	// Launch concurrent requests for different providers
	const requestsPerProvider = 10
	var wg sync.WaitGroup
	results := make(chan error, len(providers)*requestsPerProvider)
	providerResults := make(chan string, len(providers)*requestsPerProvider)

	for _, provider := range providers {
		for i := 0; i < requestsPerProvider; i++ {
			wg.Add(1)
			go func(p string) {
				defer wg.Done()
				req := &transport.Request{
					Operation: transport.OpGeneration,
					Provider:  p,
					Model:     "test-model",
					Question:  "test question",
				}

				ctx := context.Background()
				_, err := wrappedHandler.Handle(ctx, req)
				results <- err
				providerResults <- p
			}(provider)
		}
	}

	wg.Wait()
	close(results)
	close(providerResults)

	// Count results by provider
	providerCounts := make(map[string]int)
	for provider := range providerResults {
		providerCounts[provider]++
	}

	// Verify all providers were tested
	for _, provider := range providers {
		if count, exists := providerCounts[provider]; !exists || count != requestsPerProvider {
			t.Errorf("expected %d requests for %s, got %d", requestsPerProvider, provider, count)
		}
	}

	// Count total errors (all should fail)
	var errorCount int
	for err := range results {
		if err != nil {
			errorCount++
		}
	}

	expectedTotal := len(providers) * requestsPerProvider
	if errorCount != expectedTotal {
		t.Errorf("expected %d errors, got %d", expectedTotal, errorCount)
	}
}

// TestRetryMiddleware_RaceConditionStressTest stress-tests the retry middleware
// for race conditions under high concurrency.
// It uses a large number of goroutines with varying context timeouts to maximize
// contention and expose potential race conditions in internal state, such as
// statistics and budget tracking.
func TestRetryMiddleware_RaceConditionStressTest(t *testing.T) {
	config := configuration.RetryConfig{
		MaxAttempts:     3,
		MaxElapsedTime:  1 * time.Second,
		InitialInterval: 1 * time.Millisecond,
		MaxInterval:     5 * time.Millisecond,
		Multiplier:      2.0,
		UseJitter:       true,
		EnableBudget:    true,
		MaxCostCents:    5000, // $50.00
		MaxTokens:       100000,
	}

	var callCount int64
	handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		atomic.AddInt64(&callCount, 1)
		// Always return usage info and fail to stress test budget tracking
		return &transport.Response{
				Content:                 "partial",
				FinishReason:            domain.FinishStop,
				Usage:                   transport.NormalizedUsage{TotalTokens: 75},
				EstimatedCostMilliCents: 30,
			}, &llmerrors.ProviderError{
				Provider:   "test",
				StatusCode: 500,
				Message:    "server error",
				Type:       llmerrors.ErrorTypeProvider,
			}
	})

	middleware, err := retry.NewRetryMiddlewareWithConfig(config)
	if err != nil {
		t.Fatalf("Failed to create middleware: %v", err)
	}
	wrappedHandler := middleware(handler)

	req := &transport.Request{
		Operation: transport.OpGeneration,
		Provider:  "test",
		Model:     "test-model",
		Question:  "test question",
	}

	// High concurrency stress test
	const numGoroutines = 100
	var wg sync.WaitGroup
	results := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			// Vary context timeouts to increase contention
			timeout := time.Duration(5+id%20) * time.Millisecond
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			_, err := wrappedHandler.Handle(ctx, req)
			results <- err
		}(i)
	}

	wg.Wait()
	close(results)

	// Validate no panics occurred and all requests were handled
	var errorCount int
	for err := range results {
		if err != nil {
			errorCount++
		}
	}

	if errorCount != numGoroutines {
		t.Errorf("expected %d errors (all should fail), got %d", numGoroutines, errorCount)
	}

	// Verify reasonable call count
	totalCalls := atomic.LoadInt64(&callCount)
	if totalCalls == 0 {
		t.Error("expected some calls to be made")
	}

	if totalCalls > int64(numGoroutines*config.MaxAttempts) {
		t.Errorf("expected max %d calls, got %d", numGoroutines*config.MaxAttempts, totalCalls)
	}
}
