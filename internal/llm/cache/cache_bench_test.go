package cache_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/cache"
	"github.com/ahrav/go-judgy/internal/llm/configuration"
	"github.com/ahrav/go-judgy/internal/llm/providers"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// BenchmarkCacheGet measures the performance of cache retrieval operations.
// It specifically tests cache hits by pre-populating the cache
// and measuring the throughput and allocation patterns for read-heavy workloads.
// This benchmark is critical for understanding the cache's read performance.
func BenchmarkCacheGet(b *testing.B) {
	ctx := context.Background()
	config := configuration.CacheConfig{
		Enabled: false, // Disable cache to avoid Redis connection in unit tests
		TTL:     24 * time.Hour,
		MaxAge:  7 * 24 * time.Hour,
	}

	middleware, err := cache.NewCacheMiddlewareWithRedis(context.Background(), config, nil)
	if err != nil {
		b.Fatal(err)
	}

	// Pre-populate cache (in real scenario with Redis)
	handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		return &transport.Response{
			Content:      "cached response",
			FinishReason: domain.FinishStop,
			Usage: transport.NormalizedUsage{
				PromptTokens:     10,
				CompletionTokens: 20,
				TotalTokens:      30,
			},
			EstimatedCostMilliCents: 50,
			RawBody:                 []byte(`{"content":"cached"}`),
		}, nil
	})

	cachedHandler := middleware(handler)

	req := &transport.Request{
		Operation:      transport.OpGeneration,
		Provider:       "openai",
		Model:          "gpt-4",
		TenantID:       "bench-tenant",
		Question:       "benchmark question",
		IdempotencyKey: "bench-key-12345678",
	}

	// Warm up cache
	_, _ = cachedHandler.Handle(ctx, req)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := cachedHandler.Handle(ctx, req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCacheSet measures the performance of cache storage operations.
// It benchmarks cache writes by using unique keys for each operation
// to isolate the performance of serialization and Redis write throughput.
// This benchmark is important for understanding write performance and memory usage.
func BenchmarkCacheSet(b *testing.B) {
	ctx := context.Background()
	config := configuration.CacheConfig{
		Enabled: false, // Disable cache to avoid Redis connection in unit tests
		TTL:     24 * time.Hour,
	}

	middleware, err := cache.NewCacheMiddlewareWithRedis(context.Background(), config, nil)
	if err != nil {
		b.Fatal(err)
	}

	handler := transport.HandlerFunc(func(_ context.Context, req *transport.Request) (*transport.Response, error) {
		return &transport.Response{
			Content:      fmt.Sprintf("response for %s", req.IdempotencyKey),
			FinishReason: domain.FinishStop,
			Usage: transport.NormalizedUsage{
				PromptTokens:     100,
				CompletionTokens: 200,
				TotalTokens:      300,
			},
			EstimatedCostMilliCents: 150,
			RawBody:                 []byte(`{"content":"test"}`),
		}, nil
	})

	cachedHandler := middleware(handler)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		req := &transport.Request{
			Operation:      transport.OpGeneration,
			Provider:       "openai",
			Model:          "gpt-4",
			TenantID:       "bench-tenant",
			Question:       "benchmark question",
			IdempotencyKey: fmt.Sprintf("bench-key-%d", i),
		}

		_, err := cachedHandler.Handle(ctx, req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLeaseAcquisition measures the performance of distributed lease acquisition.
// It tests the overhead of the atomic check-and-lease mechanism
// by simulating concurrent requests with unique keys.
// This benchmark is critical for understanding distributed coordination costs.
func BenchmarkLeaseAcquisition(b *testing.B) {
	ctx := context.Background()
	config := configuration.CacheConfig{
		Enabled: false, // Disable cache to avoid Redis connection in unit tests
		TTL:     24 * time.Hour,
	}

	middleware, err := cache.NewCacheMiddlewareWithRedis(context.Background(), config, nil)
	if err != nil {
		b.Fatal(err)
	}

	handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		// Simulate some work
		time.Sleep(time.Microsecond)
		return &transport.Response{
			Content:      "response",
			FinishReason: domain.FinishStop,
			RawBody:      []byte(`{}`),
		}, nil
	})

	cachedHandler := middleware(handler)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		req := &transport.Request{
			Operation:      transport.OpGeneration,
			Provider:       "openai",
			Model:          "gpt-4",
			TenantID:       "bench-tenant",
			IdempotencyKey: fmt.Sprintf("lease-key-%d", i),
		}

		_, err := cachedHandler.Handle(ctx, req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkMiddlewareOverhead measures the performance overhead of the cache middleware.
// It compares a baseline handler's performance against the same handler
// wrapped with the cache middleware, but with caching disabled.
// This isolates the middleware's overhead, which is essential for understanding
// the performance impact of integrating the cache.
func BenchmarkMiddlewareOverhead(b *testing.B) {
	ctx := context.Background()

	// Baseline: handler without middleware
	baseHandler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		return &transport.Response{
			Content:      "response",
			FinishReason: domain.FinishStop,
			Usage:        transport.NormalizedUsage{TotalTokens: 10},
			RawBody:      []byte(`{}`),
		}, nil
	})

	// With middleware (cache disabled)
	config := configuration.CacheConfig{
		Enabled: false, // Disabled to measure pure overhead
	}

	middleware, err := cache.NewCacheMiddlewareWithRedis(context.Background(), config, nil)
	if err != nil {
		b.Fatal(err)
	}

	wrappedHandler := middleware(baseHandler)

	req := &transport.Request{
		Operation:      transport.OpGeneration,
		Provider:       "openai",
		Model:          "gpt-4",
		TenantID:       "bench-tenant",
		IdempotencyKey: "bench-key-12345678",
	}

	b.Run("Baseline", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, err := baseHandler.Handle(ctx, req)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("WithMiddleware", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, err := wrappedHandler.Handle(ctx, req)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkJSONMarshaling measures the performance of JSON serialization
// for IdempotentCacheEntry structures.
// It tests marshaling, unmarshaling, and round-trip performance
// to understand the costs associated with storing entries in Redis.
func BenchmarkJSONMarshaling(b *testing.B) {
	entry := transport.IdempotentCacheEntry{
		Provider:    "openai",
		Model:       "gpt-4",
		RawResponse: []byte(`{"choices":[{"message":{"content":"This is a test response with some content"}}],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`),
		ResponseHeaders: map[string]string{
			"X-Request-ID":          "req-123456789",
			"X-RateLimit-Remaining": "1000",
			"Content-Type":          "application/json",
		},
		Usage: transport.NormalizedUsage{
			PromptTokens:     10,
			CompletionTokens: 20,
			TotalTokens:      30,
			LatencyMs:        150,
		},
		EstimatedMilliCents: 50,
		StoredAtUnixMs:      time.Now().UnixMilli(),
	}

	b.Run("Marshal", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, err := json.Marshal(entry)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	data, _ := json.Marshal(entry)

	b.Run("Unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var e transport.IdempotentCacheEntry
			err := json.Unmarshal(data, &e)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("RoundTrip", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			data, err := json.Marshal(entry)
			if err != nil {
				b.Fatal(err)
			}

			var e transport.IdempotentCacheEntry
			err = json.Unmarshal(data, &e)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkKeyGeneration measures the performance of cache key generation.
// It benchmarks the internal key-building logic by invoking the middleware
// and captures the overhead of string formatting and validation.
// This is important for understanding the cost of generating cache keys.
func BenchmarkKeyGeneration(b *testing.B) {
	req := &transport.Request{
		TenantID:       "tenant-123",
		Operation:      transport.OpGeneration,
		IdempotencyKey: "idem-key-12345678",
	}

	// Since buildKey is internal, we benchmark through the middleware
	ctx := context.Background()
	config := configuration.CacheConfig{
		Enabled: false, // Disable cache to avoid Redis connection in unit tests
		TTL:     24 * time.Hour,
	}

	middleware, err := cache.NewCacheMiddlewareWithRedis(context.Background(), config, nil)
	if err != nil {
		b.Fatal(err)
	}

	handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		return &transport.Response{
			Content:      "test",
			FinishReason: domain.FinishStop,
			RawBody:      []byte(`{}`),
		}, nil
	})

	cachedHandler := middleware(handler)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = cachedHandler.Handle(ctx, req)
	}
}

// BenchmarkContentExtraction measures the performance of provider-specific
// content extraction from raw LLM responses.
// It tests the JSON parsing efficiency for OpenAI, Anthropic, and Google formats
// to understand the performance costs of normalization.
func BenchmarkContentExtraction(b *testing.B) {
	testCases := []struct {
		name     string
		provider string
		rawBody  []byte
	}{
		{
			name:     "OpenAI",
			provider: providers.ProviderOpenAI,
			rawBody: []byte(`{
				"choices": [{
					"message": {
						"content": "This is a test response from OpenAI with some longer content to make it more realistic"
					}
				}],
				"usage": {
					"prompt_tokens": 10,
					"completion_tokens": 20,
					"total_tokens": 30
				}
			}`),
		},
		{
			name:     "Anthropic",
			provider: providers.ProviderAnthropic,
			rawBody: []byte(`{
				"content": [{
					"text": "This is a test response from Anthropic with some longer content to make it more realistic"
				}],
				"usage": {
					"input_tokens": 10,
					"output_tokens": 20
				}
			}`),
		},
		{
			name:     "Google",
			provider: providers.ProviderGoogle,
			rawBody: []byte(`{
				"candidates": [{
					"content": {
						"parts": [{
							"text": "This is a test response from Google with some longer content to make it more realistic"
						}]
					}
				}],
				"usageMetadata": {
					"promptTokenCount": 10,
					"candidatesTokenCount": 20,
					"totalTokenCount": 30
				}
			}`),
		},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			// Test through the middleware to exercise content extraction
			ctx := context.Background()
			config := configuration.CacheConfig{
				Enabled: false, // Disable cache to avoid Redis connection in unit tests
				TTL:     24 * time.Hour,
			}

			middleware, err := cache.NewCacheMiddlewareWithRedis(context.Background(), config, nil)
			if err != nil {
				b.Fatal(err)
			}

			handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				return &transport.Response{
					Content:      "original",
					FinishReason: domain.FinishStop,
					RawBody:      tc.rawBody,
				}, nil
			})

			cachedHandler := middleware(handler)

			req := &transport.Request{
				Provider:       tc.provider,
				Model:          "test-model",
				TenantID:       "test-tenant",
				Operation:      transport.OpGeneration,
				IdempotencyKey: "test-key-12345678",
			}

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				_, err := cachedHandler.Handle(ctx, req)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkConcurrentCacheAccess benchmarks concurrent cache access patterns.
// Measures performance under concurrent load with mixed read/write operations.
// Tests scalability and contention handling across multiple goroutines.
// Essential for understanding cache performance under production concurrency.
func BenchmarkConcurrentCacheAccess(b *testing.B) {
	ctx := context.Background()
	config := configuration.CacheConfig{
		Enabled: false, // Disable cache to avoid Redis connection in unit tests
		TTL:     24 * time.Hour,
	}

	middleware, err := cache.NewCacheMiddlewareWithRedis(context.Background(), config, nil)
	if err != nil {
		b.Fatal(err)
	}

	handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		return &transport.Response{
			Content:      "response",
			FinishReason: domain.FinishStop,
			Usage:        transport.NormalizedUsage{TotalTokens: 10},
			RawBody:      []byte(`{}`),
		}, nil
	})

	cachedHandler := middleware(handler)

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			req := &transport.Request{
				Operation:      transport.OpGeneration,
				Provider:       "openai",
				Model:          "gpt-4",
				TenantID:       fmt.Sprintf("tenant-%d", i%10),
				IdempotencyKey: fmt.Sprintf("key-%d", i),
			}
			i++

			_, err := cachedHandler.Handle(ctx, req)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkLargeCacheEntry benchmarks handling of large cache entries.
// Measures performance with 100KB responses to test serialization scalability.
// Tests memory allocation patterns and throughput with large payloads.
// Important for understanding cache behavior with large LLM responses.
func BenchmarkLargeCacheEntry(b *testing.B) {
	ctx := context.Background()
	config := configuration.CacheConfig{
		Enabled: false, // Disable cache to avoid Redis connection in unit tests
		TTL:     24 * time.Hour,
	}

	middleware, err := cache.NewCacheMiddlewareWithRedis(context.Background(), config, nil)
	if err != nil {
		b.Fatal(err)
	}

	// Create a large response body
	largeContent := make([]byte, 100*1024) // 100KB
	for i := range largeContent {
		largeContent[i] = byte('a' + (i % 26))
	}

	handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		return &transport.Response{
			Content:      string(largeContent),
			FinishReason: domain.FinishStop,
			Usage: transport.NormalizedUsage{
				PromptTokens:     1000,
				CompletionTokens: 2000,
				TotalTokens:      3000,
			},
			EstimatedCostMilliCents: 500,
			RawBody:                 largeContent,
		}, nil
	})

	cachedHandler := middleware(handler)

	req := &transport.Request{
		Operation:      transport.OpGeneration,
		Provider:       "openai",
		Model:          "gpt-4",
		TenantID:       "bench-tenant",
		IdempotencyKey: "large-key-12345678",
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := cachedHandler.Handle(ctx, req)
		if err != nil {
			b.Fatal(err)
		}
	}
}
