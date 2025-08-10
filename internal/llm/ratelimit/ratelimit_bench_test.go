//go:build integration
// +build integration

// This file contains performance benchmarks for the rate limiting middleware.
//
// This benchmark suite measures the performance characteristics of the rate
// limiting middleware under various load patterns and configurations. These
// benchmarks are critical for understanding the performance impact of rate
// limiting in the request processing hot path.
//
// Run benchmarks with: go test -bench=. -benchmem -cpu=1,4,8
package ratelimit_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/configuration"
	"github.com/ahrav/go-judgy/internal/llm/ratelimit"
	"github.com/ahrav/go-judgy/internal/llm/transport"
	"github.com/redis/go-redis/v9"
)

// BenchmarkRateLimitMiddleware_FullMiddlewareChain measures the performance of the
// rate limit middleware with a single, consistent request key.
// This benchmark helps establish a baseline for middleware overhead in the ideal
// case where the rate limiter's internal map has only one entry.
func BenchmarkRateLimitMiddleware_FullMiddlewareChain(b *testing.B) {
	cfg := &configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			TokensPerSecond: 10000, // High rate to focus on middleware overhead
			BurstSize:       10000,
			Enabled:         true,
		},
	}

	middleware, err := ratelimit.NewRateLimitMiddlewareWithRedis(cfg, nil)
	if err != nil {
		b.Fatal(err)
	}

	// Mock handler with minimal overhead
	mockHandler := transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
		return &transport.Response{}, nil
	})

	wrappedHandler := middleware(mockHandler)

	req := &transport.Request{
		TenantID:  "benchmark-tenant",
		Provider:  "benchmark-provider",
		Model:     "benchmark-model",
		Operation: transport.OpGeneration,
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := wrappedHandler.Handle(context.Background(), req)
		if err != nil {
			b.Fatalf("Middleware error: %v", err)
		}
	}
}

// BenchmarkRateLimitMiddleware_MiddlewareChain_VaryingKeys measures middleware
// performance as the number of unique request keys increases.
// This helps quantify the impact of map contention and key management overhead
// on the rate limiter's performance under more realistic, multi-tenant loads.
func BenchmarkRateLimitMiddleware_MiddlewareChain_VaryingKeys(b *testing.B) {
	cfg := &configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			TokensPerSecond: 10000,
			BurstSize:       10000,
			Enabled:         true,
		},
	}

	middleware, err := ratelimit.NewRateLimitMiddlewareWithRedis(cfg, nil)
	if err != nil {
		b.Fatal(err)
	}

	mockHandler := transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
		return &transport.Response{}, nil
	})

	wrappedHandler := middleware(mockHandler)

	keyVariations := []int{1, 10, 100, 1000}

	for _, keyVar := range keyVariations {
		b.Run(fmt.Sprintf("key_variations_%d", keyVar), func(b *testing.B) {
			requests := make([]*transport.Request, keyVar)
			for i := 0; i < keyVar; i++ {
				requests[i] = &transport.Request{
					TenantID:  fmt.Sprintf("tenant-%d", i),
					Provider:  fmt.Sprintf("provider-%d", i%5),
					Model:     fmt.Sprintf("model-%d", i%3),
					Operation: transport.OpGeneration,
				}
			}

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				req := requests[i%keyVar]
				_, err := wrappedHandler.Handle(context.Background(), req)
				if err != nil {
					b.Fatalf("Middleware error: %v", err)
				}
			}
		})
	}
}

// BenchmarkRateLimitMiddleware_MiddlewareChain_Parallel assesses the middleware's
// performance and concurrency safety under parallel load.
// It uses b.RunParallel to simulate multiple goroutines concurrently accessing
// the rate limiter, highlighting potential contention points.
func BenchmarkRateLimitMiddleware_MiddlewareChain_Parallel(b *testing.B) {
	cfg := &configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			TokensPerSecond: 50000, // Very high rate for parallel testing
			BurstSize:       50000,
			Enabled:         true,
		},
	}

	middleware, err := ratelimit.NewRateLimitMiddlewareWithRedis(cfg, nil)
	if err != nil {
		b.Fatal(err)
	}

	mockHandler := transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
		return &transport.Response{}, nil
	})

	wrappedHandler := middleware(mockHandler)

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		counter := 0
		for pb.Next() {
			req := &transport.Request{
				TenantID:  fmt.Sprintf("parallel-tenant-%d", counter%10),
				Provider:  "parallel-provider",
				Model:     "parallel-model",
				Operation: transport.OpGeneration,
			}

			_, err := wrappedHandler.Handle(context.Background(), req)
			if err != nil {
				b.Fatalf("Parallel middleware error: %v", err)
			}
			counter++
		}
	})
}

// BenchmarkRateLimitMiddleware_MemoryAllocation_NewMiddleware measures the memory
// allocation patterns and overhead of creating a new middleware instance.
// This is useful for understanding the cost of initializing the rate limiter,
// especially in dynamic environments where configurations might change.
func BenchmarkRateLimitMiddleware_MemoryAllocation_NewMiddleware(b *testing.B) {
	cfg := &configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			TokensPerSecond: 1000,
			BurstSize:       1000,
			Enabled:         true,
		},
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := ratelimit.NewRateLimitMiddlewareWithRedis(cfg, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRateLimitMiddleware_WithRedis(b *testing.B) {
	// Skip if Redis is not available
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   1, // Use test database
	})

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		b.Skip("Redis not available for benchmarking")
	}

	cfg := &configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			TokensPerSecond: 10000,
			BurstSize:       10000,
			Enabled:         true,
		},
		Global: configuration.GlobalRateLimitConfig{
			Enabled:           true,
			RequestsPerSecond: 10000, // High limit for benchmarking
		},
	}

	middleware, err := ratelimit.NewRateLimitMiddlewareWithRedis(cfg, client)
	if err != nil {
		b.Fatal(err)
	}

	mockHandler := transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
		return &transport.Response{
			Content:      "response",
			FinishReason: domain.FinishStop,
			Usage:        transport.NormalizedUsage{TotalTokens: 10},
		}, nil
	})

	wrappedHandler := middleware(mockHandler)

	// Clean up test keys after benchmark
	testKeys := make([]string, 100)
	for i := 0; i < 100; i++ {
		testKeys[i] = fmt.Sprintf("rl:global:bench-key-%d", i)
	}

	defer func() {
		client.Del(context.Background(), testKeys...)
	}()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		req := &transport.Request{
			TenantID:  fmt.Sprintf("tenant-%d", i%10),
			Provider:  "benchmark-provider",
			Model:     "benchmark-model",
			Operation: transport.OpGeneration,
		}

		_, err := wrappedHandler.Handle(context.Background(), req)
		if err != nil {
			// Some rate limiting may occur, but shouldn't be frequent with high limits
			b.Logf("Rate limiting occurred: %v", err)
		}
	}
}

func BenchmarkRateLimitMiddleware_HighConcurrency(b *testing.B) {
	concurrencyLevels := []int{1, 10, 50, 100}

	for _, level := range concurrencyLevels {
		b.Run(fmt.Sprintf("concurrency_%d", level), func(b *testing.B) {
			cfg := &configuration.RateLimitConfig{
				Local: configuration.LocalRateLimitConfig{
					TokensPerSecond: 100000, // Very high rate
					BurstSize:       100000,
					Enabled:         true,
				},
			}

			middleware, err := ratelimit.NewRateLimitMiddlewareWithRedis(cfg, nil)
			if err != nil {
				b.Fatal(err)
			}

			mockHandler := transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
				return &transport.Response{
					Content: "success",
				}, nil
			})

			wrappedHandler := middleware(mockHandler)

			b.ResetTimer()
			b.ReportAllocs()
			b.SetParallelism(level)

			b.RunParallel(func(pb *testing.PB) {
				counter := 0
				for pb.Next() {
					req := &transport.Request{
						TenantID:  fmt.Sprintf("tenant-%d", counter%100),
						Provider:  "provider",
						Model:     "model",
						Operation: transport.OpGeneration,
					}

					_, _ = wrappedHandler.Handle(context.Background(), req)
					counter++
				}
			})
		})
	}
}

func BenchmarkRateLimitMiddleware_DifferentOperations(b *testing.B) {
	cfg := &configuration.RateLimitConfig{
		Local: configuration.LocalRateLimitConfig{
			TokensPerSecond: 10000,
			BurstSize:       10000,
			Enabled:         true,
		},
	}

	middleware, err := ratelimit.NewRateLimitMiddlewareWithRedis(cfg, nil)
	if err != nil {
		b.Fatal(err)
	}

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
		b.Run(string(op), func(b *testing.B) {
			req := &transport.Request{
				TenantID:  "benchmark-tenant",
				Provider:  "benchmark-provider",
				Model:     "benchmark-model",
				Operation: op,
			}

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				_, err := wrappedHandler.Handle(context.Background(), req)
				if err != nil {
					b.Fatalf("Middleware error: %v", err)
				}
			}
		})
	}
}
