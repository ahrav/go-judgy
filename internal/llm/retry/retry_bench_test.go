package retry_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/circuitbreaker"
	"github.com/ahrav/go-judgy/internal/llm/configuration"
	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/retry"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// BenchmarkExponentialBackoff measures the performance of backoff duration
// calculation.
// It evaluates the computational overhead of the exponential backoff algorithm.
// Scenarios include fast and slow backoff settings, jitter application,
// and high multipliers to ensure efficiency in high-throughput retry scenarios.
func BenchmarkExponentialBackoff(b *testing.B) {
	configs := []struct {
		name   string
		config configuration.RetryConfig
	}{
		{
			name: "FastBackoff",
			config: configuration.RetryConfig{
				InitialInterval: 10 * time.Millisecond,
				MaxInterval:     100 * time.Millisecond,
				Multiplier:      2.0,
				UseJitter:       false,
			},
		},
		{
			name: "SlowBackoff",
			config: configuration.RetryConfig{
				InitialInterval: 1 * time.Second,
				MaxInterval:     30 * time.Second,
				Multiplier:      2.0,
				UseJitter:       false,
			},
		},
		{
			name: "WithJitter",
			config: configuration.RetryConfig{
				InitialInterval: 100 * time.Millisecond,
				MaxInterval:     5 * time.Second,
				Multiplier:      1.5,
				UseJitter:       true,
			},
		},
		{
			name: "HighMultiplier",
			config: configuration.RetryConfig{
				InitialInterval: 50 * time.Millisecond,
				MaxInterval:     10 * time.Second,
				Multiplier:      3.0,
				UseJitter:       false,
			},
		},
	}

	attempts := []int{1, 3, 5, 10, 20}

	for _, cfg := range configs {
		for _, attempt := range attempts {
			b.Run(fmt.Sprintf("%s/Attempt%d", cfg.name, attempt), func(b *testing.B) {
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					_ = retry.ExponentialBackoff(attempt, cfg.config)
				}
			})
		}
	}
}

// BenchmarkExponentialBackoffParallel measures backoff calculation performance
// under concurrent access.
// It verifies that the algorithm remains efficient and thread-safe when called
// from multiple goroutines simultaneously.
func BenchmarkExponentialBackoffParallel(b *testing.B) {
	config := configuration.RetryConfig{
		InitialInterval: 100 * time.Millisecond,
		MaxInterval:     5 * time.Second,
		Multiplier:      2.0,
		UseJitter:       true,
	}

	b.RunParallel(func(pb *testing.PB) {
		attempt := 5
		for pb.Next() {
			_ = retry.ExponentialBackoff(attempt, config)
		}
	})
}

// BenchmarkRetryMiddlewareSuccess measures the performance of the retry middleware
// in scenarios where the underlying operation eventually succeeds.
// It evaluates the timing overhead for operations that succeed on the first attempt
// versus those that succeed after several retries, providing a baseline
// for middleware performance under ideal and near-ideal conditions.
func BenchmarkRetryMiddlewareSuccess(b *testing.B) {
	config := configuration.RetryConfig{
		MaxAttempts:     5,
		MaxElapsedTime:  10 * time.Second,
		InitialInterval: 10 * time.Millisecond,
		MaxInterval:     200 * time.Millisecond,
		Multiplier:      2.0,
		UseJitter:       false,
	}

	scenarios := []struct {
		name           string
		successAttempt int
	}{
		{"SuccessFirst", 1},
		{"SuccessSecond", 2},
		{"SuccessThird", 3},
		{"SuccessFifth", 5},
	}

	for _, scenario := range scenarios {
		b.Run(scenario.name, func(b *testing.B) {
			middleware, err := retry.NewRetryMiddlewareWithConfig(config)
			if err != nil {
				b.Fatal(err)
			}

			var callCount int
			handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				callCount++
				if callCount >= scenario.successAttempt {
					callCount = 0 // Reset for next iteration
					return &transport.Response{
						Content:      "success",
						FinishReason: domain.FinishStop,
						Usage:        transport.NormalizedUsage{TotalTokens: 100},
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
				Question:  "benchmark question",
			}

			b.ResetTimer()
			ctx := context.Background()
			for i := 0; i < b.N; i++ {
				_, _ = wrappedHandler.Handle(ctx, req)
			}
		})
	}
}

// BenchmarkRetryMiddlewareFailure measures the worst-case performance of the retry
// middleware when an operation consistently fails.
// It evaluates the overhead when all retry attempts are exhausted, providing
// a baseline for performance in persistent failure scenarios.
func BenchmarkRetryMiddlewareFailure(b *testing.B) {
	config := configuration.RetryConfig{
		MaxAttempts:     3,
		MaxElapsedTime:  5 * time.Second,
		InitialInterval: 1 * time.Millisecond, // Very fast for benchmarking
		MaxInterval:     5 * time.Millisecond,
		Multiplier:      2.0,
		UseJitter:       false,
	}

	middleware, err := retry.NewRetryMiddlewareWithConfig(config)
	if err != nil {
		b.Fatal(err)
	}
	handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		return nil, &llmerrors.ProviderError{
			Provider:   "test",
			StatusCode: 500,
			Message:    "persistent error",
			Type:       llmerrors.ErrorTypeProvider,
		}
	})

	wrappedHandler := middleware(handler)
	req := &transport.Request{
		Operation: transport.OpGeneration,
		Provider:  "test",
		Model:     "test-model",
		Question:  "benchmark question",
	}

	b.ResetTimer()
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		_, _ = wrappedHandler.Handle(ctx, req)
	}
}

// BenchmarkRetryMiddlewareWithBudget measures the performance impact of enabling
// cost and token budget tracking within the retry middleware.
// It compares the overhead of requests with and without budget management
// to quantify the performance cost of this feature.
func BenchmarkRetryMiddlewareWithBudget(b *testing.B) {
	scenarios := []struct {
		name         string
		enableBudget bool
	}{
		{"WithoutBudget", false},
		{"WithBudget", true},
	}

	for _, scenario := range scenarios {
		b.Run(scenario.name, func(b *testing.B) {
			config := configuration.RetryConfig{
				MaxAttempts:     2,
				MaxElapsedTime:  5 * time.Second,
				InitialInterval: 1 * time.Millisecond,
				MaxInterval:     5 * time.Millisecond,
				Multiplier:      2.0,
				UseJitter:       false,
				EnableBudget:    scenario.enableBudget,
				MaxCostCents:    1000,
				MaxTokens:       10000,
			}

			middleware, err := retry.NewRetryMiddlewareWithConfig(config)
			if err != nil {
				b.Fatal(err)
			}
			handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				return &transport.Response{
					Content:                 "success",
					FinishReason:            domain.FinishStop,
					Usage:                   transport.NormalizedUsage{TotalTokens: 50},
					EstimatedCostMilliCents: 25,
				}, nil
			})

			wrappedHandler := middleware(handler)
			req := &transport.Request{
				Operation: transport.OpGeneration,
				Provider:  "test",
				Model:     "test-model",
				Question:  "benchmark question",
			}

			b.ResetTimer()
			ctx := context.Background()
			for i := 0; i < b.N; i++ {
				_, _ = wrappedHandler.Handle(ctx, req)
			}
		})
	}
}

// BenchmarkErrorClassification measures the performance of the error classification
// logic within the retry middleware.
// It evaluates the computational overhead of analyzing different error types—such
// as provider errors, rate limits, and network failures—to determine
// whether a failed operation should be retried.
func BenchmarkErrorClassification(b *testing.B) {
	errorTypes := []struct {
		name string
		err  error
	}{
		{
			name: "ProviderError",
			err: &llmerrors.ProviderError{
				Provider:   "test",
				StatusCode: 500,
				Message:    "internal server error",
				Type:       llmerrors.ErrorTypeProvider,
			},
		},
		{
			name: "RateLimitError",
			err: &llmerrors.ProviderError{
				Provider:   "test",
				StatusCode: 429,
				Message:    "rate limit exceeded",
				Type:       llmerrors.ErrorTypeRateLimit,
			},
		},
		{
			name: "NetworkError",
			err:  errors.New("connection refused"),
		},
		{
			name: "CircuitBreakerError",
			err: &llmerrors.CircuitBreakerError{
				Provider: "test",
				Model:    "test-model",
				State:    "open",
				ResetAt:  time.Now().Add(30 * time.Second).Unix(),
			},
		},
		{
			name: "ValidationError",
			err: &llmerrors.ProviderError{
				Provider:   "test",
				StatusCode: 400,
				Message:    "bad request",
				Type:       llmerrors.ErrorTypeValidation,
			},
		},
	}

	config := configuration.RetryConfig{
		MaxAttempts:     3,
		MaxElapsedTime:  5 * time.Second,
		InitialInterval: 1 * time.Millisecond,
		MaxInterval:     5 * time.Millisecond,
		Multiplier:      2.0,
		UseJitter:       false,
	}

	for _, errorType := range errorTypes {
		b.Run(errorType.name, func(b *testing.B) {
			middleware, err := retry.NewRetryMiddlewareWithConfig(config)
			if err != nil {
				b.Fatal(err)
			}
			handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				return nil, errorType.err
			})

			wrappedHandler := middleware(handler)
			req := &transport.Request{
				Operation: transport.OpGeneration,
				Provider:  "test",
				Model:     "test-model",
				Question:  "benchmark question",
			}

			b.ResetTimer()
			ctx := context.Background()
			for i := 0; i < b.N; i++ {
				_, _ = wrappedHandler.Handle(ctx, req)
			}
		})
	}
}

// BenchmarkParseRetryAfterValue measures the parsing performance of the 'Retry-After'
// header value.
// It tests various common formats, including seconds-based integers and
// different timestamp layouts, to ensure efficient handling of rate limit
// directives from providers.
func BenchmarkParseRetryAfterValue(b *testing.B) {
	testCases := []struct {
		name  string
		value string
	}{
		{"IntegerSeconds", "30"},
		{"SmallInteger", "5"},
		{"LargeInteger", "3600"},
		{"RFC1123", "Mon, 02 Jan 2006 15:04:05 GMT"},
		{"RFC822", "02 Jan 06 15:04 MST"},
		{"RFC850", "Monday, 02-Jan-06 15:04:05 GMT"},
		{"ANSIC", "Mon Jan  2 15:04:05 2006"},
		{"Invalid", "not_a_number"},
		{"Empty", ""},
		{"Zero", "0"},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			// Test parsing through WorkflowError mechanism
			workflowErr := &llmerrors.WorkflowError{
				Type:      llmerrors.ErrorTypeRateLimit,
				Message:   "rate limited",
				Code:      "RATE_LIMIT",
				Retryable: true,
				Details: map[string]any{
					"retry_after": tc.value,
				},
			}

			config := configuration.RetryConfig{
				MaxAttempts:     2,
				MaxElapsedTime:  30 * time.Second,
				InitialInterval: 100 * time.Millisecond,
				MaxInterval:     5 * time.Second,
				Multiplier:      2.0,
				UseJitter:       false,
			}

			middleware, err := retry.NewRetryMiddlewareWithConfig(config)
			if err != nil {
				b.Fatal(err)
			}
			handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				return nil, workflowErr
			})

			wrappedHandler := middleware(handler)
			req := &transport.Request{
				Operation: transport.OpGeneration,
				Provider:  "test",
				Model:     "test-model",
				Question:  "benchmark question",
			}

			b.ResetTimer()
			ctx := context.Background()
			for i := 0; i < b.N; i++ {
				_, _ = wrappedHandler.Handle(ctx, req)
			}
		})
	}
}

// BenchmarkNetworkErrorDetection measures the performance of network error
// pattern matching.
// It evaluates how quickly the middleware can identify network-related errors
// from a mix of network and non-network error messages of varying lengths.
func BenchmarkNetworkErrorDetection(b *testing.B) {
	networkErrors := []string{
		"connection refused",
		"connection reset by peer",
		"broken pipe",
		"no such host",
		"network is unreachable",
		"i/o timeout",
		"EOF",
		"dial tcp: connection refused",
		"read tcp: connection reset by peer",
		"write tcp: broken pipe",
		strings.Repeat("connection refused ", 100), // Long error message
	}

	nonNetworkErrors := []string{
		"internal server error",
		"validation failed",
		"authentication error",
		"not found",
		"bad request",
	}

	networkErrors = append(networkErrors, nonNetworkErrors...)
	allErrors := networkErrors

	config := configuration.RetryConfig{
		MaxAttempts:     2,
		MaxElapsedTime:  1 * time.Second,
		InitialInterval: 1 * time.Millisecond,
		MaxInterval:     5 * time.Millisecond,
		Multiplier:      2.0,
		UseJitter:       false,
	}

	b.Run("NetworkErrors", func(b *testing.B) {
		middleware, err := retry.NewRetryMiddlewareWithConfig(config)
		if err != nil {
			b.Fatal(err)
		}
		req := &transport.Request{
			Operation: transport.OpGeneration,
			Provider:  "test",
			Model:     "test-model",
			Question:  "benchmark question",
		}

		b.ResetTimer()
		ctx := context.Background()
		for i := 0; i < b.N; i++ {
			errorMsg := networkErrors[i%len(networkErrors)]
			handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				return nil, errors.New(errorMsg)
			})
			wrappedHandler := middleware(handler)
			_, _ = wrappedHandler.Handle(ctx, req)
		}
	})

	b.Run("AllErrorTypes", func(b *testing.B) {
		middleware, err := retry.NewRetryMiddlewareWithConfig(config)
		if err != nil {
			b.Fatal(err)
		}
		req := &transport.Request{
			Operation: transport.OpGeneration,
			Provider:  "test",
			Model:     "test-model",
			Question:  "benchmark question",
		}

		b.ResetTimer()
		ctx := context.Background()
		for i := 0; i < b.N; i++ {
			errorMsg := allErrors[i%len(allErrors)]
			handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				return nil, errors.New(errorMsg)
			})
			wrappedHandler := middleware(handler)
			_, _ = wrappedHandler.Handle(ctx, req)
		}
	})
}

// BenchmarkCircuitBreakerIntegration measures the performance of the retry middleware
// when it is chained with the circuit breaker middleware.
// It evaluates the combined overhead under various failure patterns and circuit
// breaker configurations to ensure efficient coordination between these
// two resilience components.
func BenchmarkCircuitBreakerIntegration(b *testing.B) {
	scenarios := []struct {
		name           string
		failurePattern int // Every Nth request fails
		circuitConfig  circuitbreaker.Config
	}{
		{
			name:           "NoCircuitBreaker",
			failurePattern: 3,
			circuitConfig:  circuitbreaker.Config{}, // Default/disabled
		},
		{
			name:           "StrictCircuitBreaker",
			failurePattern: 2,
			circuitConfig: circuitbreaker.Config{
				FailureThreshold: 1,
				SuccessThreshold: 1,
				HalfOpenProbes:   1,
				OpenTimeout:      5 * time.Millisecond,
			},
		},
		{
			name:           "RelaxedCircuitBreaker",
			failurePattern: 5,
			circuitConfig: circuitbreaker.Config{
				FailureThreshold: 5,
				SuccessThreshold: 3,
				HalfOpenProbes:   2,
				OpenTimeout:      10 * time.Millisecond,
			},
		},
	}

	for _, scenario := range scenarios {
		b.Run(scenario.name, func(b *testing.B) {
			retryConfig := configuration.RetryConfig{
				MaxAttempts:     3,
				MaxElapsedTime:  2 * time.Second,
				InitialInterval: 1 * time.Millisecond,
				MaxInterval:     5 * time.Millisecond,
				Multiplier:      2.0,
				UseJitter:       false,
			}

			var callCount int
			handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				callCount++
				if callCount%scenario.failurePattern == 0 {
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
					Usage:        transport.NormalizedUsage{TotalTokens: 50},
				}, nil
			})

			// Chain middlewares: retry -> circuit breaker -> handler
			retryMiddleware, err := retry.NewRetryMiddlewareWithConfig(retryConfig)
			if err != nil {
				b.Fatal(err)
			}
			var wrappedHandler transport.Handler

			if scenario.circuitConfig.FailureThreshold > 0 {
				circuitBreakerMiddleware, err := circuitbreaker.NewCircuitBreakerMiddlewareWithRedis(scenario.circuitConfig, nil)
				if err != nil {
					b.Fatal(err)
				}
				wrappedHandler = retryMiddleware(circuitBreakerMiddleware(handler))
			} else {
				wrappedHandler = retryMiddleware(handler)
			}

			req := &transport.Request{
				Operation: transport.OpGeneration,
				Provider:  "test",
				Model:     "test-model",
				Question:  "benchmark question",
			}

			b.ResetTimer()
			ctx := context.Background()
			for i := 0; i < b.N; i++ {
				_, _ = wrappedHandler.Handle(ctx, req)
			}
		})
	}
}

// BenchmarkContextCancellation measures the performance impact of context
// cancellation on retry operations.
// It evaluates how quickly the retry loop terminates when the context is canceled
// at different points, including immediate cancellation and cancellation
// after a timeout.
func BenchmarkContextCancellation(b *testing.B) {
	config := configuration.RetryConfig{
		MaxAttempts:     5,
		MaxElapsedTime:  10 * time.Second,
		InitialInterval: 10 * time.Millisecond,
		MaxInterval:     100 * time.Millisecond,
		Multiplier:      2.0,
		UseJitter:       false,
	}

	scenarios := []struct {
		name              string
		timeoutAfter      time.Duration
		cancelImmediately bool
	}{
		{"NoTimeout", 0, false},
		{"ShortTimeout", 5 * time.Millisecond, false},
		{"MediumTimeout", 50 * time.Millisecond, false},
		{"ImmediateCancel", 0, true},
	}

	for _, scenario := range scenarios {
		b.Run(scenario.name, func(b *testing.B) {
			middleware, err := retry.NewRetryMiddlewareWithConfig(config)
			if err != nil {
				b.Fatal(err)
			}
			handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				// Always fail to trigger retries and test cancellation
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
				Question:  "benchmark question",
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var ctx context.Context
				var cancel context.CancelFunc

				switch {
				case scenario.timeoutAfter > 0:
					ctx, cancel = context.WithTimeout(context.Background(), scenario.timeoutAfter)
				case scenario.cancelImmediately:
					ctx, cancel = context.WithCancel(context.Background())
					cancel() // Cancel immediately
				default:
					ctx = context.Background()
				}

				_, _ = wrappedHandler.Handle(ctx, req)

				if cancel != nil && !scenario.cancelImmediately {
					cancel()
				}
			}
		})
	}
}

// BenchmarkRetryMiddlewareParallel measures the retry middleware's performance
// under high concurrent load.
// It simulates a realistic workload with a mix of successful and failed
// operations to assess the middleware's behavior in a production-like
// environment.
func BenchmarkRetryMiddlewareParallel(b *testing.B) {
	config := configuration.RetryConfig{
		MaxAttempts:     3,
		MaxElapsedTime:  5 * time.Second,
		InitialInterval: 5 * time.Millisecond,
		MaxInterval:     50 * time.Millisecond,
		Multiplier:      2.0,
		UseJitter:       true,
		EnableBudget:    true,
		MaxCostCents:    10000,
		MaxTokens:       100000,
	}

	middleware, err := retry.NewRetryMiddlewareWithConfig(config)
	if err != nil {
		b.Fatal(err)
	}

	// Realistic handler with mixed success/failure pattern
	var globalCallCount int
	handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		globalCallCount++
		// 80% success rate on first attempt, 95% on retry
		if globalCallCount%5 == 0 || globalCallCount%10 > 7 {
			return nil, &llmerrors.ProviderError{
				Provider:   "test",
				StatusCode: 500,
				Message:    "temporary error",
				Type:       llmerrors.ErrorTypeProvider,
			}
		}
		return &transport.Response{
			Content:                 "success",
			FinishReason:            domain.FinishStop,
			Usage:                   transport.NormalizedUsage{TotalTokens: 75},
			EstimatedCostMilliCents: 35,
		}, nil
	})

	wrappedHandler := middleware(handler)
	req := &transport.Request{
		Operation: transport.OpGeneration,
		Provider:  "test",
		Model:     "test-model",
		Question:  "parallel benchmark question",
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		ctx := context.Background()
		for pb.Next() {
			_, _ = wrappedHandler.Handle(ctx, req)
		}
	})
}

// BenchmarkLargePayload measures the retry middleware's performance when handling
// large request and response payloads.
// This benchmark helps identify any performance bottlenecks related to data
// handling during retry operations, simulating real-world usage with
// significant data transfer.
func BenchmarkLargePayload(b *testing.B) {
	payloadSizes := []struct {
		name         string
		questionSize int
		responseSize int
	}{
		{"Small", 100, 200},
		{"Medium", 1000, 2000},
		{"Large", 10000, 20000},
		{"ExtraLarge", 50000, 100000},
	}

	config := configuration.RetryConfig{
		MaxAttempts:     2,
		MaxElapsedTime:  5 * time.Second,
		InitialInterval: 5 * time.Millisecond,
		MaxInterval:     25 * time.Millisecond,
		Multiplier:      2.0,
		UseJitter:       false,
	}

	for _, payload := range payloadSizes {
		b.Run(payload.name, func(b *testing.B) {
			middleware, err := retry.NewRetryMiddlewareWithConfig(config)
			if err != nil {
				b.Fatal(err)
			}

			largeQuestion := strings.Repeat("word ", payload.questionSize/5)
			largeResponse := strings.Repeat("response ", payload.responseSize/9)

			handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				// Succeed immediately to focus on payload handling overhead
				return &transport.Response{
					Content:      largeResponse,
					FinishReason: domain.FinishStop,
					Usage:        transport.NormalizedUsage{TotalTokens: int64(payload.responseSize / 4)},
				}, nil
			})

			wrappedHandler := middleware(handler)
			req := &transport.Request{
				Operation: transport.OpGeneration,
				Provider:  "test",
				Model:     "test-model",
				Question:  largeQuestion,
			}

			b.ResetTimer()
			b.SetBytes(int64(payload.questionSize + payload.responseSize))
			ctx := context.Background()
			for i := 0; i < b.N; i++ {
				_, _ = wrappedHandler.Handle(ctx, req)
			}
		})
	}
}

// BenchmarkMemoryAllocation measures the memory allocation patterns of the retry
// middleware during its operation.
// By reporting allocations, it helps identify potential optimizations to reduce
// the middleware's memory footprint.
func BenchmarkMemoryAllocation(b *testing.B) {
	config := configuration.RetryConfig{
		MaxAttempts:     3,
		MaxElapsedTime:  5 * time.Second,
		InitialInterval: 1 * time.Millisecond,
		MaxInterval:     10 * time.Millisecond,
		Multiplier:      2.0,
		UseJitter:       false,
		EnableBudget:    true,
		MaxCostCents:    1000,
		MaxTokens:       10000,
	}

	middleware, err := retry.NewRetryMiddlewareWithConfig(config)
	if err != nil {
		b.Fatal(err)
	}
	handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		return &transport.Response{
			Content:                 "success",
			FinishReason:            domain.FinishStop,
			Usage:                   transport.NormalizedUsage{TotalTokens: 100},
			EstimatedCostMilliCents: 50,
		}, nil
	})

	wrappedHandler := middleware(handler)
	req := &transport.Request{
		Operation: transport.OpGeneration,
		Provider:  "test",
		Model:     "test-model",
		Question:  "memory benchmark question",
	}

	b.ResetTimer()
	b.ReportAllocs()
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		_, _ = wrappedHandler.Handle(ctx, req)
	}
}
