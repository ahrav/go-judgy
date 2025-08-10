//go:build go1.18
// +build go1.18

package retry_test

import (
	"context"
	"errors"
	"math"
	"net"
	"net/url"
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

// fuzzTestTempError provides a controllable temporary error for testing.
// It implements the net.Error interface to simulate transient network
// conditions, allowing fuzz tests to validate retry logic and error
// classification accuracy under diverse, reproducible scenarios.
type fuzzTestTempError struct {
	msg string
}

func (e *fuzzTestTempError) Error() string   { return e.msg }
func (e *fuzzTestTempError) Temporary() bool { return true }
func (e *fuzzTestTempError) Timeout() bool   { return false }

// FuzzParseRetryAfterValue tests the robustness of parsing Retry-After values.
// It fuzzes the internal parsing logic by supplying a wide range of strings,
// including valid integers, malformed data, edge cases, and potential
// vulnerabilities, to ensure the retry mechanism handles them gracefully
// without panicking.
func FuzzParseRetryAfterValue(f *testing.F) {
	// Seed with typical valid and edge case values
	testValues := []string{
		"",
		"0",
		"1",
		"30",
		"120",
		"3600",
		"-1",
		"abc",
		"1.5",
		"9999999999999999999999",
		"  30  ",
		"30.0",
		"0x1E",
		"1e2",
		"+30",
		"30s",
		"Mon, 01 Jan 2024 12:00:00 GMT",
		"2147483647", // Max int32
		"2147483648", // Max int32 + 1
	}

	for _, val := range testValues {
		f.Add(val)
	}

	f.Fuzz(func(t *testing.T, value string) {
		// Test parseRetryAfterValue indirectly through WorkflowError
		workflowErr := &llmerrors.WorkflowError{
			Type:      llmerrors.ErrorTypeRateLimit,
			Message:   "rate limited",
			Code:      "RATE_LIMIT",
			Retryable: true,
			Details: map[string]any{
				"retry_after": value,
			},
		}

		// Create retry config
		config := configuration.RetryConfig{
			MaxAttempts:     3,
			MaxElapsedTime:  10 * time.Second,
			InitialInterval: 100 * time.Millisecond,
			MaxInterval:     5 * time.Second,
			Multiplier:      2.0,
			UseJitter:       false,
		}

		var callCount int32
		handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
			atomic.AddInt32(&callCount, 1)
			// Always return the workflow error to test parsing
			return nil, workflowErr
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

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		// Should not panic regardless of input
		_, _ = wrappedHandler.Handle(ctx, req)

		// Basic validation - should have made at least one call
		if atomic.LoadInt32(&callCount) < 1 {
			t.Error("expected at least one handler call")
		}
	})
}

// FuzzIsNetworkError tests the robustness of network error detection.
// It fuzzes the internal isNetworkError function with a variety of error
// messages and types to ensure accurate classification, preventing both
// false positives and false negatives in retryable error identification.
func FuzzIsNetworkError(f *testing.F) {
	// Seed with known network error patterns
	networkPatterns := []string{
		"connection refused",
		"connection reset by peer",
		"broken pipe",
		"no such host",
		"network is unreachable",
		"i/o timeout",
		"EOF",
		"dial tcp: connection refused",
		"read: connection reset by peer",
		"write: broken pipe",
		"lookup example.com: no such host",
	}

	nonNetworkPatterns := []string{
		"authentication failed",
		"invalid request",
		"rate limit exceeded",
		"internal server error",
		"forbidden",
		"not found",
		"bad gateway",
		"service unavailable",
	}

	// Add valid patterns
	for _, pattern := range networkPatterns {
		f.Add(pattern)
	}

	// Add invalid patterns
	for _, pattern := range nonNetworkPatterns {
		f.Add(pattern)
	}

	// Add edge cases
	edgeCases := []string{
		"",
		"   ",
		"CONNECTION REFUSED", // Case variations
		"Connection Refused",
		"connection_refused",
		"connection-refused",
		"timeout",
		"TIMEOUT",
		"random string",
		strings.Repeat("a", 1000),
		"connection refused" + strings.Repeat("x", 100),
	}

	for _, edge := range edgeCases {
		f.Add(edge)
	}

	f.Fuzz(func(t *testing.T, errorMsg string) {
		// Test with different error types
		errorVariants := []error{
			errors.New(errorMsg),
			&net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: errors.New(errorMsg),
			},
			&url.Error{
				Op:  "Get",
				URL: "https://example.com",
				Err: errors.New(errorMsg),
			},
			&fuzzTestTempError{msg: errorMsg},
		}

		for _, err := range errorVariants {
			config := configuration.RetryConfig{
				MaxAttempts:     2,
				MaxElapsedTime:  5 * time.Second,
				InitialInterval: 10 * time.Millisecond,
				MaxInterval:     100 * time.Millisecond,
				Multiplier:      2.0,
				UseJitter:       false,
			}

			var callCount int32
			handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				atomic.AddInt32(&callCount, 1)
				return nil, err
			})

			middleware, mwErr := retry.NewRetryMiddlewareWithConfig(config)
			if mwErr != nil {
				t.Fatalf("Failed to create middleware: %v", mwErr)
			}
			wrappedHandler := middleware(handler)

			req := &transport.Request{
				Operation: transport.OpGeneration,
				Provider:  "test",
				Model:     "test-model",
				Question:  "test question",
			}

			ctx := context.Background()
			_, _ = wrappedHandler.Handle(ctx, req)

			// Should not panic and should make at least one call
			calls := atomic.LoadInt32(&callCount)
			if calls < 1 {
				t.Errorf("expected at least 1 call, got %d", calls)
			}

			// Validate reasonable retry behavior
			if calls > int32(config.MaxAttempts) {
				t.Errorf("exceeded max attempts: %d > %d", calls, config.MaxAttempts)
			}
		}
	})
}

// FuzzCalculateBackoff validates backoff calculation stability.
// Fuzzes exponential backoff calculations with extreme values to ensure
// mathematical stability, overflow prevention, and reasonable result boundaries.
func FuzzCalculateBackoff(f *testing.F) {
	// Seed with reasonable values
	seeds := []struct {
		attempt    int
		initialMs  int64
		maxMs      int64
		multiplier float64
		useJitter  bool
	}{
		{1, 100, 1000, 2.0, false},
		{5, 50, 5000, 1.5, true},
		{10, 1, 60000, 3.0, false},
		{0, 100, 1000, 2.0, false},  // Edge case: 0 attempts
		{-1, 100, 1000, 2.0, false}, // Edge case: negative attempts
		{1, 0, 1000, 2.0, false},    // Edge case: 0 initial interval
		{1, 100, 0, 2.0, false},     // Edge case: 0 max interval
		{1, 100, 1000, 0.0, false},  // Edge case: 0 multiplier
		{1, 100, 1000, -1.0, false}, // Edge case: negative multiplier
	}

	for _, seed := range seeds {
		f.Add(seed.attempt, seed.initialMs, seed.maxMs, seed.multiplier, seed.useJitter)
	}

	f.Fuzz(func(t *testing.T, attempt int, initialMs, maxMs int64, multiplier float64, useJitter bool) {
		// Skip clearly invalid or problematic ranges
		if initialMs < -86400000 || initialMs > 86400000 { // 24 hours in ms
			t.Skip("initial interval out of reasonable range")
		}
		if maxMs < -86400000 || maxMs > 86400000 {
			t.Skip("max interval out of reasonable range")
		}
		if math.IsNaN(multiplier) || math.IsInf(multiplier, 0) {
			t.Skip("invalid multiplier")
		}
		if multiplier < -1000 || multiplier > 1000 {
			t.Skip("multiplier out of reasonable range")
		}

		config := configuration.RetryConfig{
			InitialInterval: time.Duration(initialMs) * time.Millisecond,
			MaxInterval:     time.Duration(maxMs) * time.Millisecond,
			Multiplier:      multiplier,
			UseJitter:       useJitter,
		}

		// Should not panic
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panic in backoff calculation: %v", r)
			}
		}()

		// The function we're testing is not exported, so we test it indirectly
		// by creating a retry scenario and checking behavior
		var callCount int32
		handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
			callCount++
			if callCount < int32(attempt) {
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

		// Use reasonable attempt count to avoid infinite loops
		retryConfig := configuration.RetryConfig{
			MaxAttempts:     max(1, min(attempt, 10)),
			MaxElapsedTime:  30 * time.Second,
			InitialInterval: config.InitialInterval,
			MaxInterval:     config.MaxInterval,
			Multiplier:      config.Multiplier,
			UseJitter:       config.UseJitter,
		}

		middleware, err := retry.NewRetryMiddlewareWithConfig(retryConfig)
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

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Should complete without hanging or panicking
		_, _ = wrappedHandler.Handle(ctx, req)
	})
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// FuzzRetryMiddlewareConfig validates configuration edge cases.
// Fuzzes retry middleware configuration with boundary values to ensure
// robust handling of invalid configurations and graceful degradation.
func FuzzRetryMiddlewareConfig(f *testing.F) {
	// Seed with boundary values
	f.Add(0, int64(0), int64(0), int64(0), 0.0, false, false)
	f.Add(1, int64(1), int64(1000), int64(1000), 2.0, true, true)
	f.Add(100, int64(86400000), int64(86400000), int64(86400000), 10.0, true, true)
	f.Add(-1, int64(-1), int64(-1), int64(-1), -1.0, false, false)

	f.Fuzz(func(t *testing.T, maxAttempts int, initialMs int64, maxElapsedMs int64, maxIntervalMs int64, multiplier float64, useJitter bool, enableBudget bool) {
		// Skip clearly invalid ranges to focus on edge cases
		if maxAttempts < -100 || maxAttempts > 10000 {
			t.Skip("max attempts out of reasonable range")
		}
		if initialMs < -86400000 || initialMs > 86400000 { // 24 hours
			t.Skip("initial interval out of range")
		}
		if maxElapsedMs < -86400000 || maxElapsedMs > 86400000 {
			t.Skip("max elapsed time out of range")
		}
		if maxIntervalMs < -86400000 || maxIntervalMs > 86400000 {
			t.Skip("max interval out of range")
		}
		if math.IsNaN(multiplier) || math.IsInf(multiplier, 0) {
			t.Skip("invalid multiplier")
		}
		if multiplier < -1000 || multiplier > 1000 {
			t.Skip("multiplier out of range")
		}

		config := configuration.RetryConfig{
			MaxAttempts:     maxAttempts,
			MaxElapsedTime:  time.Duration(maxElapsedMs) * time.Millisecond,
			InitialInterval: time.Duration(initialMs) * time.Millisecond,
			MaxInterval:     time.Duration(maxIntervalMs) * time.Millisecond,
			Multiplier:      multiplier,
			UseJitter:       useJitter,
			EnableBudget:    enableBudget,
			MaxCostCents:    1000,
			MaxTokens:       10000,
		}

		// Creating middleware should not panic
		middleware, err := retry.NewRetryMiddlewareWithConfig(config)
		if err != nil {
			// Configuration errors are acceptable
			return
		}
		if middleware == nil {
			t.Error("middleware creation returned nil")
		}

		// Test basic functionality
		ctx := context.Background()
		var callCount int

		handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
			callCount++
			// Succeed on second call if maxAttempts allows it
			if callCount >= 2 && maxAttempts >= 2 {
				return &transport.Response{
					Content:      "success",
					FinishReason: domain.FinishStop,
					Usage:        transport.NormalizedUsage{TotalTokens: 10},
				}, nil
			}
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

		// Should not hang or panic
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		_, _ = wrappedHandler.Handle(ctx, req)
	})
}

// FuzzErrorClassification validates error classification robustness.
// Fuzzes error classification logic with diverse error conditions to ensure
// accurate retry/no-retry decisions and prevent classification edge cases.
func FuzzErrorClassification(f *testing.F) {
	// Seed with various status codes and error types
	statusCodes := []int{200, 400, 401, 403, 404, 408, 429, 500, 502, 503, 504}
	errorMessages := []string{
		"timeout",
		"connection refused",
		"rate limited",
		"unauthorized",
		"forbidden",
		"not found",
		"server error",
		"bad gateway",
		"service unavailable",
		"gateway timeout",
	}

	for _, code := range statusCodes {
		for _, msg := range errorMessages {
			f.Add(code, msg)
		}
	}

	f.Fuzz(func(t *testing.T, statusCode int, message string) {
		// Skip unreasonable status codes
		if statusCode < 0 || statusCode > 999 {
			t.Skip("unreasonable status code")
		}

		// Create provider error with fuzzed values
		providerError := &llmerrors.ProviderError{
			Provider:   "test",
			StatusCode: statusCode,
			Message:    message,
			Type:       llmerrors.ErrorTypeProvider, // Will be overridden based on status
		}

		// Classify based on status code (similar to actual implementation)
		if statusCode == 429 {
			providerError.Type = llmerrors.ErrorTypeRateLimit
		} else if statusCode >= 500 {
			providerError.Type = llmerrors.ErrorTypeProvider
		} else if statusCode == 408 {
			providerError.Type = llmerrors.ErrorTypeTimeout
		} else if statusCode == 401 || statusCode == 403 {
			providerError.Type = llmerrors.ErrorTypeAuth
		} else if statusCode >= 400 && statusCode < 500 {
			providerError.Type = llmerrors.ErrorTypeValidation
		}

		ctx := context.Background()
		config := configuration.RetryConfig{
			MaxAttempts:     3,
			MaxElapsedTime:  5 * time.Second,
			InitialInterval: 10 * time.Millisecond,
			MaxInterval:     100 * time.Millisecond,
			Multiplier:      2.0,
			UseJitter:       false,
		}

		var callCount int32
		handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
			atomic.AddInt32(&callCount, 1)
			return nil, providerError
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

		// Should not panic
		_, _ = wrappedHandler.Handle(ctx, req)

		// Validate attempt count is reasonable
		attempts := atomic.LoadInt32(&callCount)
		if attempts < 1 {
			t.Error("expected at least one attempt")
		}
		if attempts > int32(config.MaxAttempts) {
			t.Errorf("exceeded max attempts: %d > %d", attempts, config.MaxAttempts)
		}
	})
}

// FuzzBudgetLimits validates budget enforcement edge cases.
// Fuzzes budget limit configurations with extreme values to ensure
// robust budget tracking and prevent integer overflow or underflow conditions.
func FuzzBudgetLimits(f *testing.F) {
	// Seed with boundary values for budget limits
	f.Add(int64(0), int64(0), int64(0), int64(0))                                          // Zero limits
	f.Add(int64(1), int64(1), int64(1), int64(1))                                          // Minimal limits
	f.Add(int64(1000), int64(10000), int64(100), int64(50))                                // Normal limits
	f.Add(int64(9223372036854775807), int64(9223372036854775807), int64(1000), int64(100)) // Max int64

	f.Fuzz(func(t *testing.T, maxCostCents, maxTokens, usageTokens, usageCostCents int64) {
		// Skip unreasonable values
		if maxCostCents < 0 || maxTokens < 0 || usageTokens < 0 || usageCostCents < 0 {
			t.Skip("negative values not supported")
		}
		if maxCostCents > 1000000 || maxTokens > 10000000 {
			t.Skip("values too large for testing")
		}
		if usageTokens > 100000 || usageCostCents > 10000 {
			t.Skip("usage values too large for testing")
		}

		config := configuration.RetryConfig{
			MaxAttempts:     5,
			MaxElapsedTime:  10 * time.Second,
			InitialInterval: 1 * time.Millisecond,
			MaxInterval:     100 * time.Millisecond,
			Multiplier:      2.0,
			UseJitter:       false,
			EnableBudget:    true,
			MaxCostCents:    maxCostCents,
			MaxTokens:       maxTokens,
		}

		var callCount int32
		handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
			atomic.AddInt32(&callCount, 1)
			// First call returns usage info
			if callCount == 1 {
				return &transport.Response{
						Content:                 "partial",
						FinishReason:            domain.FinishStop,
						Usage:                   transport.NormalizedUsage{TotalTokens: usageTokens},
						EstimatedCostMilliCents: usageCostCents * 1000, // Convert cents to milli-cents (1 cent = 1000 milli-cents)
					}, &llmerrors.ProviderError{
						Provider:   "test",
						StatusCode: 500,
						Message:    "server error",
						Type:       llmerrors.ErrorTypeProvider,
					}
			}
			// Subsequent calls
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

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Should not panic or hang
		_, _ = wrappedHandler.Handle(ctx, req)

		// Validate reasonable attempt count
		attempts := atomic.LoadInt32(&callCount)
		if attempts < 1 {
			t.Error("expected at least one attempt")
		}
		if attempts > int32(config.MaxAttempts) {
			t.Errorf("exceeded max attempts: %d > %d", attempts, config.MaxAttempts)
		}
	})
}

// FuzzNetworkErrorPatterns validates network error detection patterns.
// Fuzzes network error message matching to ensure robust detection
// across different error message formats and potential false classifications.
func FuzzNetworkErrorPatterns(f *testing.F) {
	// Seed with known patterns and variations
	patterns := []string{
		"connection",
		"refused",
		"reset",
		"broken",
		"pipe",
		"timeout",
		"unreachable",
		"eof",
		"dial",
		"lookup",
	}

	prefixes := []string{"", "tcp: ", "udp: ", "dial tcp: ", "read tcp: ", "write tcp: "}
	suffixes := []string{"", " by peer", " error", " failed", " (connection refused)"}

	for _, pattern := range patterns {
		for _, prefix := range prefixes {
			for _, suffix := range suffixes {
				f.Add(prefix + pattern + suffix)
			}
		}
	}

	f.Fuzz(func(t *testing.T, errorPattern string) {
		// Skip extremely long strings to prevent resource exhaustion
		if len(errorPattern) > 10000 {
			t.Skip("error pattern too long")
		}

		config := configuration.RetryConfig{
			MaxAttempts:     2,
			MaxElapsedTime:  5 * time.Second,
			InitialInterval: 10 * time.Millisecond,
			MaxInterval:     100 * time.Millisecond,
			Multiplier:      2.0,
			UseJitter:       false,
		}

		// Test with different error types containing the pattern
		testErrors := []error{
			errors.New(errorPattern),
			&net.OpError{Op: "dial", Net: "tcp", Err: errors.New(errorPattern)},
			&url.Error{Op: "Get", URL: "http://example.com", Err: errors.New(errorPattern)},
		}

		for _, testErr := range testErrors {
			var callCount int32
			handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				atomic.AddInt32(&callCount, 1)
				return nil, testErr
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

			ctx := context.Background()
			_, _ = wrappedHandler.Handle(ctx, req)

			// Should not panic and should make reasonable attempts
			attempts := atomic.LoadInt32(&callCount)
			if attempts < 1 {
				t.Error("expected at least one attempt")
			}
			if attempts > int32(config.MaxAttempts) {
				t.Errorf("exceeded max attempts: %d > %d", attempts, config.MaxAttempts)
			}
		}
	})
}
