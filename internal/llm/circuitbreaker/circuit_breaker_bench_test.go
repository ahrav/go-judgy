package circuitbreaker_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/circuitbreaker"
	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// BenchmarkCircuitBreakerAllow measures the performance of the critical allow() method.
// It benchmarks the method across different circuit states to identify performance
// characteristics and ensure consistent sub-microsecond response times in
// production workloads.
func BenchmarkCircuitBreakerAllow(b *testing.B) {
	ctx := context.Background()

	configs := []struct {
		name   string
		config circuitbreaker.Config
		state  string // "closed", "open", "half-open"
	}{
		{
			name: "closed_state",
			config: circuitbreaker.Config{
				FailureThreshold: 5,
				SuccessThreshold: 2,
				HalfOpenProbes:   3,
				OpenTimeout:      10 * time.Millisecond,
			},
			state: "closed",
		},
		{
			name: "open_state",
			config: circuitbreaker.Config{
				FailureThreshold: 1,
				SuccessThreshold: 2,
				HalfOpenProbes:   3,
				OpenTimeout:      1 * time.Hour, // Keep open for test
			},
			state: "open",
		},
		{
			name: "half_open_state",
			config: circuitbreaker.Config{
				FailureThreshold: 1,
				SuccessThreshold: 100, // Keep in half-open
				HalfOpenProbes:   10,
				OpenTimeout:      1 * time.Millisecond,
			},
			state: "half-open",
		},
	}

	for _, tc := range configs {
		b.Run(tc.name, func(b *testing.B) {
			// Setup circuit breaker in desired state
			handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				return &transport.Response{
					Content:      "success",
					FinishReason: domain.FinishStop,
					Usage:        transport.NormalizedUsage{TotalTokens: 10},
				}, nil
			})

			cbMiddleware, _ := circuitbreaker.NewCircuitBreakerMiddlewareWithRedis(tc.config, nil)
			cbHandler := cbMiddleware(handler)

			req := &transport.Request{
				Operation: transport.OpGeneration,
				Provider:  "test",
				Model:     "test-model",
				Question:  "test",
			}

			// Set up the desired state
			switch tc.state {
			case "open":
				// Trigger failure to open circuit
				failHandler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
					return nil, &llmerrors.ProviderError{
						Provider:   "test",
						StatusCode: 500,
						Message:    "error",
						Type:       llmerrors.ErrorTypeProvider,
					}
				})
				failCBHandler := cbMiddleware(failHandler)
				for i := 0; i < tc.config.FailureThreshold; i++ {
					_, _ = failCBHandler.Handle(ctx, req)
				}
			case "half-open":
				// Open then wait for half-open
				failHandler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
					return nil, &llmerrors.ProviderError{
						Provider:   "test",
						StatusCode: 500,
						Message:    "error",
						Type:       llmerrors.ErrorTypeProvider,
					}
				})
				failCBHandler := cbMiddleware(failHandler)
				_, _ = failCBHandler.Handle(ctx, req)
				time.Sleep(2 * time.Millisecond)
			}

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				_, _ = cbHandler.Handle(ctx, req)
			}
		})
	}
}

// BenchmarkCircuitBreakerBuildKey measures the performance impact of key generation algorithms.
// This benchmark helps optimize string building strategies and minimize allocation
// overhead in high-throughput scenarios with diverse provider configurations.
func BenchmarkCircuitBreakerBuildKey(b *testing.B) {
	ctx := context.Background()

	testCases := []struct {
		name     string
		provider string
		model    string
		region   string
	}{
		{
			name:     "short_keys",
			provider: "ai",
			model:    "v1",
			region:   "",
		},
		{
			name:     "medium_keys",
			provider: "openai",
			model:    "gpt-4-turbo",
			region:   "us-east-1",
		},
		{
			name:     "long_keys",
			provider: "anthropic-claude-3-opus",
			model:    "claude-3-opus-20240229-preview",
			region:   "eu-central-1-frankfurt",
		},
		{
			name:     "unicode_keys",
			provider: "提供者",
			model:    "模型",
			region:   "地区",
		},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			config := circuitbreaker.Config{
				FailureThreshold: 5,
				SuccessThreshold: 2,
				HalfOpenProbes:   3,
				OpenTimeout:      10 * time.Millisecond,
			}

			handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				return &transport.Response{
					Content: "success",
				}, nil
			})

			cbMiddleware, _ := circuitbreaker.NewCircuitBreakerMiddlewareWithRedis(config, nil)
			cbHandler := cbMiddleware(handler)

			req := &transport.Request{
				Operation: transport.OpGeneration,
				Provider:  tc.provider,
				Model:     tc.model,
				Question:  "test",
			}

			if tc.region != "" {
				req.Metadata = map[string]string{"region": tc.region}
			}

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				_, _ = cbHandler.Handle(ctx, req)
			}
		})
	}
}

// BenchmarkCircuitBreakerGetShard measures hash function performance for shard distribution.
// It ensures uniform load balancing and minimal CPU overhead during high-frequency
// circuit breaker lookups across concurrent request patterns.
func BenchmarkCircuitBreakerGetShard(b *testing.B) {
	keys := []string{
		"openai:gpt-4:us-east-1",
		"anthropic:claude-3:eu-west-1",
		"google:gemini:ap-southeast-1",
		"azure:gpt-4:us-west-2",
		"aws:titan:us-east-2",
	}

	b.ResetTimer()
	b.ReportAllocs()

	const numShards = 16
	for i := 0; i < b.N; i++ {
		key := keys[i%len(keys)]

		// Hash function (must match implementation)
		var hash uint32
		for j := 0; j < len(key); j++ {
			hash = hash*31 + uint32(key[j])
		}
		_ = int(hash % uint32(numShards))
	}
}

// BenchmarkShardedBreakers evaluates the performance of the sharded circuit breaker storage system.
// It operates under varying contention scenarios to validate scalability
// characteristics and identify optimal concurrency thresholds for production deployment.
func BenchmarkShardedBreakers(b *testing.B) {
	contentionLevels := []struct {
		name        string
		numKeys     int
		numWorkers  int
		maxBreakers int
	}{
		{
			name:        "low_contention",
			numKeys:     100,
			numWorkers:  4,
			maxBreakers: 1000,
		},
		{
			name:        "medium_contention",
			numKeys:     50,
			numWorkers:  10,
			maxBreakers: 1000,
		},
		{
			name:        "high_contention",
			numKeys:     10,
			numWorkers:  20,
			maxBreakers: 1000,
		},
		{
			name:        "max_breakers_limit",
			numKeys:     200,
			numWorkers:  10,
			maxBreakers: 100,
		},
	}

	for _, level := range contentionLevels {
		b.Run(level.name, func(b *testing.B) {
			ctx := context.Background()

			config := circuitbreaker.Config{
				FailureThreshold: 5,
				SuccessThreshold: 2,
				HalfOpenProbes:   3,
				OpenTimeout:      10 * time.Millisecond,
				MaxBreakers:      level.maxBreakers,
			}

			handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				return &transport.Response{
					Content: "success",
				}, nil
			})

			cbMiddleware, _ := circuitbreaker.NewCircuitBreakerMiddlewareWithRedis(config, nil)
			cbHandler := cbMiddleware(handler)

			// Pre-generate keys
			keys := make([]*transport.Request, level.numKeys)
			for i := 0; i < level.numKeys; i++ {
				keys[i] = &transport.Request{
					Operation: transport.OpGeneration,
					Provider:  fmt.Sprintf("provider-%d", i),
					Model:     fmt.Sprintf("model-%d", i),
					Question:  "test",
				}
			}

			b.ResetTimer()
			b.ReportAllocs()
			b.SetParallelism(level.numWorkers)

			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					req := keys[i%level.numKeys]
					_, _ = cbHandler.Handle(ctx, req)
					i++
				}
			})
		})
	}
}

// BenchmarkStateTransitions measures the overhead of atomic state transitions
// in the circuit breaker's state machine.
// This benchmark ensures that state changes remain performant under concurrent
// access patterns and maintain sub-microsecond transition times.
func BenchmarkStateTransitions(b *testing.B) {
	transitions := []struct {
		name       string
		fromState  string
		toState    string
		concurrent bool
	}{
		{
			name:       "closed_to_open",
			fromState:  "closed",
			toState:    "open",
			concurrent: false,
		},
		{
			name:       "open_to_half_open",
			fromState:  "open",
			toState:    "half-open",
			concurrent: false,
		},
		{
			name:       "half_open_to_closed",
			fromState:  "half-open",
			toState:    "closed",
			concurrent: false,
		},
		{
			name:       "concurrent_transitions",
			fromState:  "closed",
			toState:    "open",
			concurrent: true,
		},
	}

	for _, trans := range transitions {
		b.Run(trans.name, func(b *testing.B) {
			ctx := context.Background()

			config := circuitbreaker.Config{
				FailureThreshold: 2,
				SuccessThreshold: 2,
				HalfOpenProbes:   5,
				OpenTimeout:      5 * time.Millisecond,
			}

			b.ResetTimer()
			b.ReportAllocs()

			if trans.concurrent {
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						runStateTransition(ctx, config, trans.fromState, trans.toState)
					}
				})
			} else {
				for i := 0; i < b.N; i++ {
					runStateTransition(ctx, config, trans.fromState, trans.toState)
				}
			}
		})
	}
}

// runStateTransition is a helper function that simulates a state transition
// for a circuit breaker.
func runStateTransition(ctx context.Context, config circuitbreaker.Config, _ string, to string) {
	var shouldFail atomic.Bool

	handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		if shouldFail.Load() {
			return nil, &llmerrors.ProviderError{
				Provider:   "test",
				StatusCode: 500,
				Message:    "error",
				Type:       llmerrors.ErrorTypeProvider,
			}
		}
		return &transport.Response{
			Content: "success",
		}, nil
	})

	cbMiddleware, _ := circuitbreaker.NewCircuitBreakerMiddlewareWithRedis(config, nil)
	cbHandler := cbMiddleware(handler)

	req := &transport.Request{
		Operation: transport.OpGeneration,
		Provider:  "test",
		Model:     "test-model",
		Question:  "test",
	}

	// Perform transition
	switch to {
	case "open":
		shouldFail.Store(true)
		for i := 0; i < config.FailureThreshold; i++ {
			_, _ = cbHandler.Handle(ctx, req)
		}
	case "half-open":
		// First open
		shouldFail.Store(true)
		for i := 0; i < config.FailureThreshold; i++ {
			_, _ = cbHandler.Handle(ctx, req)
		}
		// Wait for timeout
		time.Sleep(config.OpenTimeout + time.Millisecond)
		// Trigger half-open
		shouldFail.Store(false)
		_, _ = cbHandler.Handle(ctx, req)
	case "closed":
		// Open, then half-open, then close
		shouldFail.Store(true)
		for i := 0; i < config.FailureThreshold; i++ {
			_, _ = cbHandler.Handle(ctx, req)
		}
		time.Sleep(config.OpenTimeout + time.Millisecond)
		shouldFail.Store(false)
		for i := 0; i < config.SuccessThreshold; i++ {
			_, _ = cbHandler.Handle(ctx, req)
		}
	}
}

// BenchmarkCircuitBreakerWithAdaptiveThresholds quantifies the performance cost
// of adaptive threshold calculations.
// It validates that dynamic threshold adjustment maintains acceptable overhead
// while providing enhanced circuit sensitivity.
func BenchmarkCircuitBreakerWithAdaptiveThresholds(b *testing.B) {
	configs := []struct {
		name     string
		adaptive bool
	}{
		{
			name:     "without_adaptive",
			adaptive: false,
		},
		{
			name:     "with_adaptive",
			adaptive: true,
		},
	}

	for _, tc := range configs {
		b.Run(tc.name, func(b *testing.B) {
			ctx := context.Background()

			config := circuitbreaker.Config{
				FailureThreshold:   10,
				SuccessThreshold:   5,
				HalfOpenProbes:     5,
				OpenTimeout:        10 * time.Millisecond,
				AdaptiveThresholds: tc.adaptive,
			}

			// Handler with 30% failure rate
			var counter atomic.Int32
			handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				count := counter.Add(1)
				if count%10 < 3 {
					return nil, &llmerrors.ProviderError{
						Provider:   "test",
						StatusCode: 500,
						Message:    "error",
						Type:       llmerrors.ErrorTypeProvider,
					}
				}
				return &transport.Response{
					Content: "success",
				}, nil
			})

			cbMiddleware, _ := circuitbreaker.NewCircuitBreakerMiddlewareWithRedis(config, nil)
			cbHandler := cbMiddleware(handler)

			req := &transport.Request{
				Operation: transport.OpGeneration,
				Provider:  "test",
				Model:     "test-model",
				Question:  "test",
			}

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				_, _ = cbHandler.Handle(ctx, req)
			}
		})
	}
}

// BenchmarkCircuitBreakerMemoryAllocation profiles heap allocation patterns
// in critical execution paths to identify memory optimization opportunities
// and ensure minimal garbage collection pressure during high-throughput operations.
func BenchmarkCircuitBreakerMemoryAllocation(b *testing.B) {
	scenarios := []struct {
		name     string
		metadata bool
		region   bool
	}{
		{
			name:     "minimal_request",
			metadata: false,
			region:   false,
		},
		{
			name:     "with_metadata",
			metadata: true,
			region:   false,
		},
		{
			name:     "with_region",
			metadata: true,
			region:   true,
		},
	}

	for _, scenario := range scenarios {
		b.Run(scenario.name, func(b *testing.B) {
			ctx := context.Background()

			config := circuitbreaker.Config{
				FailureThreshold: 5,
				SuccessThreshold: 2,
				HalfOpenProbes:   3,
				OpenTimeout:      10 * time.Millisecond,
			}

			handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				return &transport.Response{
					Content: "success",
				}, nil
			})

			cbMiddleware, _ := circuitbreaker.NewCircuitBreakerMiddlewareWithRedis(config, nil)
			cbHandler := cbMiddleware(handler)

			req := &transport.Request{
				Operation: transport.OpGeneration,
				Provider:  "test",
				Model:     "test-model",
				Question:  "test",
			}

			if scenario.metadata {
				req.Metadata = make(map[string]string)
				if scenario.region {
					req.Metadata["region"] = "us-east-1"
				}
			}

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				_, _ = cbHandler.Handle(ctx, req)
			}
		})
	}
}

// BenchmarkCircuitBreakerHighConcurrency evaluates system performance under
// extreme concurrent load to validate scalability limits and identify performance
// degradation thresholds for capacity planning and SLA establishment.
func BenchmarkCircuitBreakerHighConcurrency(b *testing.B) {
	concurrencyLevels := []int{1, 10, 50, 100, 500}

	for _, level := range concurrencyLevels {
		b.Run(fmt.Sprintf("concurrency_%d", level), func(b *testing.B) {
			ctx := context.Background()

			config := circuitbreaker.Config{
				FailureThreshold:   20,
				SuccessThreshold:   10,
				HalfOpenProbes:     20,
				OpenTimeout:        10 * time.Millisecond,
				AdaptiveThresholds: true,
				MaxBreakers:        1000,
			}

			// Mixed success/failure handler
			handler := transport.HandlerFunc(func(_ context.Context, req *transport.Request) (*transport.Response, error) {
				// Use request data for deterministic behavior
				if len(req.Provider)%3 == 0 {
					return nil, &llmerrors.ProviderError{
						Provider:   req.Provider,
						StatusCode: 500,
						Message:    "error",
						Type:       llmerrors.ErrorTypeProvider,
					}
				}
				return &transport.Response{
					Content: "success",
				}, nil
			})

			cbMiddleware, _ := circuitbreaker.NewCircuitBreakerMiddlewareWithRedis(config, nil)
			cbHandler := cbMiddleware(handler)

			// Pre-generate requests
			requests := make([]*transport.Request, 100)
			for i := 0; i < len(requests); i++ {
				requests[i] = &transport.Request{
					Operation: transport.OpGeneration,
					Provider:  fmt.Sprintf("provider-%d", i%10),
					Model:     fmt.Sprintf("model-%d", i%5),
					Question:  "test",
				}
			}

			b.ResetTimer()
			b.ReportAllocs()
			b.SetParallelism(level)

			var counter int32
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					idx := atomic.AddInt32(&counter, 1) % int32(len(requests))
					req := requests[idx]
					_, _ = cbHandler.Handle(ctx, req)
				}
			})
		})
	}
}

// BenchmarkStringBuilder compares string concatenation strategies to optimize
// key generation performance, measuring allocation overhead and execution time
// across different approaches to identify the most efficient implementation.
func BenchmarkStringBuilder(b *testing.B) {
	testCases := []struct {
		provider string
		model    string
		region   string
	}{
		{"openai", "gpt-4", "us-east-1"},
		{"anthropic", "claude-3", "eu-west-1"},
		{"google", "gemini", ""},
		{"azure", "gpt-4-turbo-preview", "us-central-1"},
	}

	b.Run("with_string_builder", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			tc := testCases[i%len(testCases)]

			var builder strings.Builder
			builder.Grow(len(tc.provider) + len(tc.model) + 20)
			builder.WriteString(tc.provider)
			builder.WriteByte(':')
			builder.WriteString(tc.model)
			if tc.region != "" {
				builder.WriteByte(':')
				builder.WriteString(tc.region)
			}
			_ = builder.String()
		}
	})

	b.Run("with_concatenation", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			tc := testCases[i%len(testCases)]

			key := tc.provider + ":" + tc.model
			if tc.region != "" {
				key = key + ":" + tc.region
			}
			_ = key
		}
	})

	b.Run("with_sprintf", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			tc := testCases[i%len(testCases)]

			var key string
			if tc.region != "" {
				key = fmt.Sprintf("%s:%s:%s", tc.provider, tc.model, tc.region)
			} else {
				key = fmt.Sprintf("%s:%s", tc.provider, tc.model)
			}
			_ = key
		}
	})
}

// BenchmarkCircuitBreakerProbeCleanup measures the performance overhead of probe
// cleanup mechanisms using defer functions to ensure resource cleanup operations
// maintain acceptable performance characteristics during half-open state management.
func BenchmarkCircuitBreakerProbeCleanup(b *testing.B) {
	ctx := context.Background()

	config := circuitbreaker.Config{
		FailureThreshold: 1,
		SuccessThreshold: 100, // Keep in half-open
		HalfOpenProbes:   10,
		OpenTimeout:      1 * time.Millisecond,
	}

	// Setup circuit in half-open state
	cbMiddleware, _ := circuitbreaker.NewCircuitBreakerMiddlewareWithRedis(config, nil)

	// Open circuit first
	failHandler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		return nil, &llmerrors.ProviderError{
			Provider:   "test",
			StatusCode: 500,
			Message:    "error",
			Type:       llmerrors.ErrorTypeProvider,
		}
	})

	failCBHandler := cbMiddleware(failHandler)
	req := &transport.Request{
		Operation: transport.OpGeneration,
		Provider:  "test",
		Model:     "test-model",
		Question:  "test",
	}

	_, _ = failCBHandler.Handle(ctx, req)
	time.Sleep(2 * time.Millisecond)

	// Now in half-open, benchmark probe operations
	successHandler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		return &transport.Response{
			Content: "success",
		}, nil
	})

	successCBHandler := cbMiddleware(successHandler)

	b.ResetTimer()
	b.ReportAllocs()

	// Use a pool of goroutines to simulate concurrent probes
	var wg sync.WaitGroup
	probesPerWorker := b.N / 10
	if probesPerWorker < 1 {
		probesPerWorker = 1
	}

	for w := 0; w < 10; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < probesPerWorker; i++ {
				_, _ = successCBHandler.Handle(ctx, req)
			}
		}()
	}

	wg.Wait()
}
