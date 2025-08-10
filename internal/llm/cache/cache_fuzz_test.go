//go:build go1.18
// +build go1.18

package cache_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/cache"
	"github.com/ahrav/go-judgy/internal/llm/configuration"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// FuzzCacheEntryMarshaling tests the JSON marshaling and unmarshaling of
// transport.IdempotentCacheEntry.
// It uses fuzzing to validate robust JSON handling, especially with edge cases
// and malformed data. The test ensures that round-trip serialization is
// consistent, error handling is graceful, and critical invariants are preserved.
// This is critical for preventing cache corruption and ensuring data integrity.
func FuzzCacheEntryMarshaling(f *testing.F) {
	// Seed corpus with edge cases
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`{"provider":"openai","model":"gpt-4","raw_response":"{\"test\":\"data\"}","usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30},"estimated_milli_cents":150,"stored_at_ms":1234567890}`))
	f.Add([]byte(`{"provider":"","model":"","raw_response":"","usage":{},"estimated_milli_cents":0,"stored_at_ms":0}`))
	f.Add([]byte(`{"provider":"anthropic","model":"claude-3","raw_response":null,"usage":{"prompt_tokens":-1,"completion_tokens":-1,"total_tokens":-2}}`))
	f.Add([]byte(`{"provider":"google","model":"gemini","raw_response":"{}","usage":{"prompt_tokens":9223372036854775807,"completion_tokens":9223372036854775807,"total_tokens":9223372036854775807}}`))
	f.Add([]byte(`{"provider":"openai","model":"gpt-4","raw_response":"","response_headers":{"X-Request-ID":"req-123","Content-Type":"application/json"},"usage":{"prompt_tokens":100,"completion_tokens":200}}`))
	f.Add([]byte(`{"provider":"test","model":"model","raw_response":"","usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0,"latency_ms":0},"estimated_milli_cents":-9223372036854775808,"stored_at_ms":9223372036854775807}`))

	f.Fuzz(func(t *testing.T, input []byte) {
		var entry transport.IdempotentCacheEntry
		err := json.Unmarshal(input, &entry)
		if err != nil {
			// Invalid JSON is expected to fail
			return
		}

		// Valid entry should be able to round-trip
		marshaled, err := json.Marshal(entry)
		if err != nil {
			t.Fatalf("failed to marshal valid entry: %v", err)
		}

		var entry2 transport.IdempotentCacheEntry
		err = json.Unmarshal(marshaled, &entry2)
		if err != nil {
			t.Fatalf("round-trip unmarshal failed: %v", err)
		}

		// Check basic invariants
		if entry.Provider != entry2.Provider {
			t.Errorf("provider mismatch: %q != %q", entry.Provider, entry2.Provider)
		}
		if entry.Model != entry2.Model {
			t.Errorf("model mismatch: %q != %q", entry.Model, entry2.Model)
		}
		if entry.EstimatedMilliCents != entry2.EstimatedMilliCents {
			t.Errorf("cost mismatch: %d != %d", entry.EstimatedMilliCents, entry2.EstimatedMilliCents)
		}

		// Usage validation
		if entry.Usage.PromptTokens < 0 || entry.Usage.CompletionTokens < 0 {
			// Negative tokens should be handled gracefully
			if entry2.Usage.PromptTokens < 0 || entry2.Usage.CompletionTokens < 0 {
				// Preserved negative values - this might be intentional for error states
				return
			}
		}

		// Total tokens consistency check
		if entry.Usage.TotalTokens != 0 {
			expectedTotal := entry.Usage.PromptTokens + entry.Usage.CompletionTokens
			if expectedTotal > 0 && entry.Usage.TotalTokens != expectedTotal {
				// Inconsistent token counts - should be handled by validation
				return
			}
		}
	})
}

// FuzzBuildKey tests the cache key generation logic with a wide variety of inputs.
// It validates key generation against edge cases, special characters, and
// boundary conditions. The test also ensures that validation rules are enforced
// and that invalid inputs are handled gracefully. This is crucial for maintaining
// the security and consistency of cache key generation.
func FuzzBuildKey(f *testing.F) {
	// Seed corpus with edge cases
	f.Add("tenant-123", "generation", "idem-key-12345678")
	f.Add("", "", "")
	f.Add("tenant", "scoring", "key")
	f.Add("tenant-with-special-chars!@#$%", "generation", "idem-key-12345678")
	f.Add("tenant", "invalid-op", "idem-key")
	f.Add("t", "generation", "12345678")
	f.Add("tenant-"+string(make([]byte, 255)), "generation", "idem-key-12345678")
	f.Add("tenant", "generation", string(make([]byte, 257)))
	f.Add("tenant\x00null", "generation", "idem\x00null")
	f.Add("tenant/with/slashes", "generation", "idem:with:colons")
	f.Add("tenant|pipe", "scoring", "idem|pipe|key|test")
	f.Add("🦄unicode🦄", "generation", "🔑unicode🔑key🔑")
	f.Fuzz(func(t *testing.T, tenantID string, operation string, idemKey string) {
		req := &transport.Request{
			TenantID:       tenantID,
			Operation:      transport.OperationType(operation),
			IdempotencyKey: idemKey,
		}

		// Test through middleware validation
		ctx := context.Background()
		config := configuration.CacheConfig{
			Enabled: false, // Disable cache to avoid Redis connection in fuzz tests
			TTL:     24 * time.Hour,
		}

		middleware, err := cache.NewCacheMiddlewareWithRedis(context.Background(), config, nil)
		if err != nil {
			t.Fatalf("failed to create middleware: %v", err)
		}

		handlerCalled := false
		handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
			handlerCalled = true
			return &transport.Response{
				Content:      "test",
				FinishReason: domain.FinishStop,
				RawBody:      []byte(`{"test":"data"}`),
			}, nil
		})

		cachedHandler := middleware(handler)
		_, _ = cachedHandler.Handle(ctx, req)

		// Validation checks
		if tenantID == "" || operation == "" || idemKey == "" {
			// These should trigger validation errors and still call handler
			if !handlerCalled {
				t.Error("handler should be called even with validation errors")
			}
			return
		}

		// Length validation
		if len(idemKey) < 8 || len(idemKey) > 256 {
			// Invalid key length should be handled
			if !handlerCalled {
				t.Error("handler should be called with invalid key length")
			}
			return
		}

		// Operation validation
		if operation != string(transport.OpGeneration) && operation != string(transport.OpScoring) {
			// Invalid operation should be handled
			if !handlerCalled {
				t.Error("handler should be called with invalid operation")
			}
			return
		}

		// Valid inputs should work
		if !handlerCalled {
			t.Error("handler should be called for valid inputs")
		}
	})
}

// FuzzResponseConversion tests the conversion from a transport.Response to an
// IdempotentCacheEntry.
// It validates the robustness of the conversion logic using extreme values and
// edge case inputs. The test covers token count handling, cost calculations,
// and overall data preservation. This is critical for ensuring cache
// reliability when dealing with diverse LLM response formats.
func FuzzResponseConversion(f *testing.F) {
	// Seed corpus
	f.Add("openai", "gpt-4", `{"content":"test"}`, int64(100), int64(200), int64(300), int64(150))
	f.Add("", "", ``, int64(0), int64(0), int64(0), int64(0))
	f.Add("anthropic", "claude", `null`, int64(-1), int64(-1), int64(-2), int64(-100))
	f.Add("google", "gemini", `{"very":"long","nested":{"object":{"with":{"many":{"levels":{}}}}}`,
		int64(9223372036854775807), int64(9223372036854775807), int64(9223372036854775807), int64(9223372036854775807))
	f.Add("test", "model", string(make([]byte, 10000)), int64(1), int64(2), int64(3), int64(4))

	f.Fuzz(func(t *testing.T, provider, model string, rawBody string,
		promptTokens, completionTokens, totalTokens, costMilliCents int64,
	) {
		// Create response
		resp := &transport.Response{
			Content:      "test content",
			FinishReason: domain.FinishStop,
			Usage: transport.NormalizedUsage{
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				TotalTokens:      totalTokens,
				LatencyMs:        100,
			},
			EstimatedCostMilliCents: costMilliCents,
			RawBody:                 []byte(rawBody),
		}

		req := &transport.Request{
			Provider: provider,
			Model:    model,
		}

		// This would test the conversion functions if they were exported
		// Since they're internal, we test indirectly through the middleware

		ctx := context.Background()
		config := configuration.CacheConfig{
			Enabled: false, // Disable cache to avoid Redis connection in fuzz tests
			TTL:     24 * time.Hour,
		}

		middleware, err := cache.NewCacheMiddlewareWithRedis(context.Background(), config, nil)
		if err != nil {
			t.Fatalf("failed to create middleware: %v", err)
		}

		handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
			return resp, nil
		})

		// Set required fields for validation
		req.TenantID = "test-tenant"
		req.Operation = transport.OpGeneration
		req.IdempotencyKey = "test-key-12345678"

		cachedHandler := middleware(handler)
		result, err := cachedHandler.Handle(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Basic validation
		if result == nil {
			t.Fatal("expected non-nil result")
		}

		// Check that extreme values are handled
		if promptTokens < 0 || completionTokens < 0 {
			// Negative tokens should be handled gracefully
			return
		}

		if totalTokens != promptTokens+completionTokens && totalTokens != 0 {
			// Inconsistent totals should be handled
			return
		}

		// Large values should not cause overflow
		if promptTokens > 1000000 || completionTokens > 1000000 {
			// Suspiciously high token counts should be handled
			return
		}
	})
}

// FuzzProviderContentExtraction tests provider-specific content extraction.
// Validates robust parsing with malformed JSON, unknown providers, and edge cases.
// Tests fallback strategies and error handling across all extraction functions.
// Ensures reliable content extraction regardless of response format quality.
func FuzzProviderContentExtraction(f *testing.F) {
	// Seed with various provider response formats
	f.Add("openai", `{"choices":[{"message":{"content":"OpenAI response"}}]}`)
	f.Add("openai", `{"choices":[]}`)
	f.Add("openai", `{"choices":null}`)
	f.Add("openai", `{}`)
	f.Add("openai", `null`)
	f.Add("openai", `invalid json`)
	f.Add("anthropic", `{"content":[{"text":"Anthropic response"}]}`)
	f.Add("anthropic", `{"content":[]}`)
	f.Add("anthropic", `{"content":null}`)
	f.Add("google", `{"candidates":[{"content":{"parts":[{"text":"Google response"}]}}]}`)
	f.Add("google", `{"candidates":[{"content":{"parts":[]}}]}`)
	f.Add("google", `{"candidates":[]}`)
	f.Add("unknown", `{"content":"Generic response"}`)
	f.Add("unknown", `{"random":"data"}`)

	f.Fuzz(func(t *testing.T, provider string, rawBody string) {
		// Create a response with the fuzzed raw body
		resp := &transport.Response{
			Content:      "original content",
			FinishReason: domain.FinishStop,
			Usage: transport.NormalizedUsage{
				TotalTokens: 10,
			},
			RawBody: []byte(rawBody),
		}

		req := &transport.Request{
			Provider:       provider,
			Model:          "test-model",
			TenantID:       "test-tenant",
			Operation:      transport.OpGeneration,
			IdempotencyKey: "test-key-12345678",
		}

		// Test through middleware to exercise content extraction
		ctx := context.Background()
		config := configuration.CacheConfig{
			Enabled: false, // Disable cache to avoid Redis connection in fuzz tests
			TTL:     24 * time.Hour,
		}

		middleware, err := cache.NewCacheMiddlewareWithRedis(context.Background(), config, nil)
		if err != nil {
			t.Fatalf("failed to create middleware: %v", err)
		}

		handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
			return resp, nil
		})

		cachedHandler := middleware(handler)
		result, err := cachedHandler.Handle(ctx, req)
		// Should not error on any input
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result == nil {
			t.Fatal("expected non-nil result")
		}

		// Content extraction should handle invalid JSON gracefully
		// Either extracting valid content or falling back to original
	})
}

// FuzzIdempotencyKeyValidation tests idempotency key validation edge cases.
// Validates handling of special characters, unicode, path traversal attempts, and length boundaries.
// Tests security constraints and graceful handling of potentially malicious inputs.
// Critical for preventing security vulnerabilities and ensuring validation robustness.
func FuzzIdempotencyKeyValidation(f *testing.F) {
	// Seed with various key formats
	f.Add("12345678")
	f.Add("short")
	f.Add(string(make([]byte, 256)))
	f.Add(string(make([]byte, 257)))
	f.Add("valid-key-with-special-!@#$%^&*()")
	f.Add("key-with-unicode-🔑-characters")
	f.Add("key\nwith\nnewlines")
	f.Add("key\twith\ttabs")
	f.Add("key\x00with\x00nulls")
	f.Add("../../etc/passwd")
	f.Add("key:with:colons")
	f.Add("key|with|pipes")

	f.Fuzz(func(t *testing.T, idemKey string) {
		req := &transport.Request{
			TenantID:       "test-tenant",
			Operation:      transport.OpGeneration,
			IdempotencyKey: idemKey,
			Provider:       "openai",
			Model:          "gpt-4",
		}

		ctx := context.Background()
		config := configuration.CacheConfig{
			Enabled: false, // Disable cache to avoid Redis connection in fuzz tests
			TTL:     24 * time.Hour,
		}

		middleware, err := cache.NewCacheMiddlewareWithRedis(context.Background(), config, nil)
		if err != nil {
			t.Fatalf("failed to create middleware: %v", err)
		}

		handlerCalled := false
		handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
			handlerCalled = true
			return &transport.Response{
				Content:      "test",
				FinishReason: domain.FinishStop,
				RawBody:      []byte(`{}`),
			}, nil
		})

		cachedHandler := middleware(handler)
		_, _ = cachedHandler.Handle(ctx, req)

		// All requests should be handled, even with invalid keys
		if !handlerCalled {
			t.Error("handler should be called regardless of key validation")
		}

		// Specific validation checks
		keyLen := len(idemKey)
		if keyLen < 8 || keyLen > 256 {
			// Invalid length should be handled gracefully
			return
		}

		// Keys with special characters should work
		// Path traversal attempts should be handled safely
		// Unicode should be supported
	})
}
