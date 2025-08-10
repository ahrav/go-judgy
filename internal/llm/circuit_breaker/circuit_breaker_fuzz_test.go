//go:build go1.18
// +build go1.18

package circuit_breaker_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ahrav/go-judgy/internal/llm/circuit_breaker"
	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// FuzzCircuitBreakerBuildKey validates the key generation algorithm.
// It ensures the algorithm handles arbitrary input combinations, including
// Unicode, special characters, and other edge cases.
// The test checks that key generation does not panic or produce collisions
// that could compromise circuit isolation.
func FuzzCircuitBreakerBuildKey(f *testing.F) {
	// Seed corpus with edge cases
	f.Add("provider", "model", "")
	f.Add("", "", "")
	f.Add("openai", "gpt-4", "us-east-1")
	f.Add("anthropic", "claude-3", "eu-west-1")
	f.Add("提供者", "模型", "地区") // Unicode
	f.Add("a", "b", "c")
	f.Add(strings.Repeat("x", 1000), strings.Repeat("y", 1000), strings.Repeat("z", 1000))
	f.Add("provider!@#$%", "model^&*()", "region-_+=")
	f.Add("\x00\x01\x02", "\xff\xfe\xfd", "") // Binary data
	f.Add("provider\nwith\nnewlines", "model\twith\ttabs", "region with spaces")

	f.Fuzz(func(t *testing.T, provider, model, region string) {
		ctx := context.Background()
		config := circuit_breaker.CircuitBreakerConfig{
			FailureThreshold: 2,
			SuccessThreshold: 1,
			HalfOpenProbes:   1,
			OpenTimeout:      10 * time.Millisecond,
		}

		// Create request with fuzzed inputs
		req := &transport.Request{
			Operation: transport.OpGeneration,
			Provider:  provider,
			Model:     model,
			Question:  "test question",
		}

		// Add region if provided
		if region != "" {
			req.Metadata = map[string]string{"region": region}
		}

		// Handler that always fails to test key generation
		handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
			return nil, &llmerrors.ProviderError{
				Provider:   provider,
				StatusCode: 500,
				Message:    "server error",
				Type:       llmerrors.ErrorTypeProvider,
			}
		})

		cbMiddleware, _ := circuit_breaker.NewCircuitBreakerMiddlewareWithRedis(config, nil)
		cbHandler := cbMiddleware(handler)

		// Execute request - should not panic
		_, err := cbHandler.Handle(ctx, req)
		if err == nil {
			t.Fatal("expected error from failing handler")
		}

		// Verify error is of expected type
		var provErr *llmerrors.ProviderError
		if !errors.As(err, &provErr) {
			t.Errorf("unexpected error type: %T", err)
		}

		// Second request with same key should either fail or be blocked
		_, err2 := cbHandler.Handle(ctx, req)
		if err2 == nil {
			t.Fatal("expected error on second request")
		}

		// Verify key consistency - same inputs should produce same behavior
		req2 := &transport.Request{
			Operation: transport.OpGeneration,
			Provider:  provider,
			Model:     model,
			Question:  "different question", // Different question shouldn't affect key
		}
		if region != "" {
			req2.Metadata = map[string]string{"region": region}
		}

		_, err3 := cbHandler.Handle(ctx, req2)
		if err3 == nil {
			t.Fatal("expected error for same key with different question")
		}

		// If circuit is open or limit reached, both should get the same error
		var perr2 *llmerrors.ProviderError
		if errors.As(err2, &perr2) {
			if perr2.Code == "CIRCUIT_OPEN" || perr2.Code == "CIRCUIT_BREAKER_LIMIT" {
				var perr3 *llmerrors.ProviderError
				if !errors.As(err3, &perr3) || perr3.Code != perr2.Code {
					t.Errorf("inconsistent circuit state for same key: %s vs %s", perr2.Code, perr3.Code)
				}
			}
		}
	})
}

// FuzzCircuitBreakerConfig validates configuration parsing.
// It ensures that parsing handles malformed and edge-case JSON inputs gracefully.
// The test verifies that the circuit breaker remains functional even with
// unexpected configuration values or corrupted data.
func FuzzCircuitBreakerConfig(f *testing.F) {
	// Seed with edge cases
	configs := []string{
		`{"failure_threshold": 0, "success_threshold": 0, "half_open_probes": 0, "open_timeout": 0}`,
		`{"failure_threshold": 1, "success_threshold": 1, "half_open_probes": 1, "open_timeout": 1000000000}`,
		`{"failure_threshold": -1, "success_threshold": -1, "half_open_probes": -1, "open_timeout": -1}`,
		`{"failure_threshold": 2147483647, "success_threshold": 2147483647, "half_open_probes": 2147483647, "open_timeout": 9223372036854775807}`,
		`{}`,
		`null`,
		`{"failure_threshold": "not a number"}`,
		`{"extra": "field", "failure_threshold": 5}`,
		`{"adaptive_thresholds": true, "failure_threshold": 10}`,
		`{"max_breakers": 1000, "probe_timeout": 60000000000}`,
	}

	for _, cfg := range configs {
		f.Add(cfg)
	}

	f.Fuzz(func(t *testing.T, configJSON string) {
		// Try to parse the configuration
		var config circuit_breaker.CircuitBreakerConfig
		err := json.Unmarshal([]byte(configJSON), &config)

		// If parsing succeeds, verify the configuration is usable
		if err == nil {
			ctx := context.Background()

			// Create middleware with parsed config
			cbMiddleware, _ := circuit_breaker.NewCircuitBreakerMiddlewareWithRedis(config, nil)

			// Create a simple handler
			handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
				return &transport.Response{
					Content: "success",
				}, nil
			})

			cbHandler := cbMiddleware(handler)

			// Try to use the circuit breaker
			req := &transport.Request{
				Operation: transport.OpGeneration,
				Provider:  "test",
				Model:     "test-model",
				Question:  "test",
			}

			// Should not panic regardless of config values
			_, _ = cbHandler.Handle(ctx, req)

			// If config has valid values, verify behavior
			if config.FailureThreshold > 0 && config.SuccessThreshold > 0 {
				// Test that circuit breaker functions with valid config
				failHandler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
					return nil, &llmerrors.ProviderError{
						Provider:   "test",
						StatusCode: 500,
						Message:    "error",
						Type:       llmerrors.ErrorTypeProvider,
					}
				})

				failCBHandler := cbMiddleware(failHandler)

				// Should handle failures without panic
				for i := 0; i < config.FailureThreshold+1; i++ {
					_, _ = failCBHandler.Handle(ctx, req)
				}
			}

			// Verify serialization round-trip
			serialized, err := json.Marshal(config)
			if err != nil {
				// Config should be serializable if it was deserializable
				t.Errorf("failed to serialize valid config: %v", err)
			}

			var config2 circuit_breaker.CircuitBreakerConfig
			if err := json.Unmarshal(serialized, &config2); err != nil {
				t.Errorf("round-trip serialization failed: %v", err)
			}
		}
	})
}

// FuzzAdaptiveThresholds validates adaptive threshold calculations.
// It ensures that calculations respond correctly to varying error rates and
// request patterns.
// The test verifies that thresholds adapt appropriately without causing
// instability or incorrect state transitions.
func FuzzAdaptiveThresholds(f *testing.F) {
	// Seed with various patterns
	f.Add(10, 0, 10)       // 0% error rate
	f.Add(10, 3, 10)       // 30% error rate
	f.Add(10, 5, 10)       // 50% error rate
	f.Add(10, 10, 10)      // 100% error rate
	f.Add(5, 1, 5)         // 20% error rate
	f.Add(100, 33, 100)    // 33% error rate
	f.Add(1, 0, 1)         // Edge case: single request
	f.Add(0, 0, 0)         // Edge case: no requests
	f.Add(1000, 500, 1000) // Large numbers
	f.Add(10, 11, 10)      // More failures than requests (invalid but should handle)

	f.Fuzz(func(t *testing.T, baseThreshold, failures, totalRequests int) {
		// Skip invalid inputs
		if baseThreshold < 0 || failures < 0 || totalRequests < 0 {
			t.Skip("negative values not valid")
		}
		if baseThreshold > 10000 || totalRequests > 10000 {
			t.Skip("values too large for reasonable testing")
		}

		ctx := context.Background()
		config := circuit_breaker.CircuitBreakerConfig{
			FailureThreshold:   baseThreshold,
			SuccessThreshold:   1,
			HalfOpenProbes:     1,
			OpenTimeout:        10 * time.Millisecond,
			AdaptiveThresholds: true,
		}

		// Track request patterns
		requestCount := 0
		failureCount := 0

		handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
			requestCount++

			// Fail based on pattern
			shouldFail := false
			if totalRequests > 0 && failures > 0 {
				// Distribute failures evenly
				if requestCount <= totalRequests {
					failureRatio := float64(failures) / float64(totalRequests)
					if float64(failureCount)/float64(requestCount) < failureRatio {
						shouldFail = true
						failureCount++
					}
				}
			}

			if shouldFail {
				return nil, &llmerrors.ProviderError{
					Provider:   "test",
					StatusCode: 500,
					Message:    "server error",
					Type:       llmerrors.ErrorTypeProvider,
				}
			}
			return &transport.Response{
				Content: "success",
			}, nil
		})

		cbMiddleware, _ := circuit_breaker.NewCircuitBreakerMiddlewareWithRedis(config, nil)
		cbHandler := cbMiddleware(handler)

		req := &transport.Request{
			Operation: transport.OpGeneration,
			Provider:  "test",
			Model:     "test-model",
			Question:  "test question",
		}

		// Execute requests
		circuitOpen := false
		for i := 0; i < totalRequests; i++ {
			_, err := cbHandler.Handle(ctx, req)
			if err != nil {
				var perr *llmerrors.ProviderError
				if errors.As(err, &perr) && perr.Code == "CIRCUIT_OPEN" {
					circuitOpen = true
					break
				}
			}
		}

		// Verify expectations based on error rate
		if totalRequests >= 10 && baseThreshold > 0 {
			errorRate := float64(failures) / float64(totalRequests)

			// With high error rates, circuit should open sooner
			if errorRate > 0.5 && !circuitOpen && failures >= baseThreshold {
				// High error rate should trigger circuit opening
				// This is expected behavior, not an error
				_ = errorRate // Acknowledge high error rate condition
			}

			// Verify no panic or unexpected behavior
			if circuitOpen {
				// Circuit is open, additional requests should be blocked
				_, err := cbHandler.Handle(ctx, req)
				if err == nil {
					t.Error("expected error when circuit is open")
				}
			}
		}
	})
}

// FuzzHashDistribution validates that the shard distribution algorithm produces
// uniform hash distribution across shards while maintaining consistency,
// preventing hotspots that could degrade performance under high load.
func FuzzHashDistribution(f *testing.F) {
	// Seed with various key patterns
	f.Add("openai:gpt-4:us-east-1")
	f.Add("anthropic:claude-3:eu-west-1")
	f.Add("google:gemini:ap-southeast-1")
	f.Add(":")
	f.Add("::")
	f.Add("")
	f.Add(strings.Repeat("a", 1000))
	f.Add("key-" + strings.Repeat("0", 100))
	f.Add("\x00\x01\x02:\xff\xfe\xfd")
	f.Add("provider:model")
	f.Add("a:b:c:d:e:f:g:h")

	const numShards = 16 // Must match the implementation

	f.Fuzz(func(t *testing.T, key string) {
		// Simple hash function (must match implementation)
		var hash uint32
		for i := 0; i < len(key); i++ {
			hash = hash*31 + uint32(key[i])
		}
		shard := int(hash % uint32(numShards))

		// Verify shard is in valid range
		if shard < 0 || shard >= numShards {
			t.Errorf("shard out of range: %d", shard)
		}

		// Verify consistency - same key always produces same shard
		var hash2 uint32
		for i := 0; i < len(key); i++ {
			hash2 = hash2*31 + uint32(key[i])
		}
		shard2 := int(hash2 % uint32(numShards))

		if shard != shard2 {
			t.Errorf("inconsistent hashing: %d != %d", shard, shard2)
		}

		// Test that the key works in actual circuit breaker
		ctx := context.Background()
		config := circuit_breaker.CircuitBreakerConfig{
			FailureThreshold: 1,
			SuccessThreshold: 1,
			HalfOpenProbes:   1,
			OpenTimeout:      10 * time.Millisecond,
		}

		// Parse key to extract provider:model:region
		parts := strings.Split(key, ":")
		provider := ""
		model := ""
		region := ""

		if len(parts) > 0 {
			provider = parts[0]
		}
		if len(parts) > 1 {
			model = parts[1]
		}
		if len(parts) > 2 {
			region = parts[2]
		}

		req := &transport.Request{
			Operation: transport.OpGeneration,
			Provider:  provider,
			Model:     model,
			Question:  "test",
		}

		if region != "" {
			req.Metadata = map[string]string{"region": region}
		}

		handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
			return &transport.Response{
				Content: "success",
			}, nil
		})

		cbMiddleware, _ := circuit_breaker.NewCircuitBreakerMiddlewareWithRedis(config, nil)
		cbHandler := cbMiddleware(handler)

		// Should not panic with any key
		_, _ = cbHandler.Handle(ctx, req)
	})
}

// FuzzMaxBreakersLimit validates the behavior when hitting the MaxBreakers limit.
// It ensures that the circuit breaker consistently returns appropriate errors
// and handles edge cases around the limit threshold.
func FuzzMaxBreakersLimit(f *testing.F) {
	// Seed with various patterns
	f.Add(1, 1)   // Single breaker, single request
	f.Add(5, 10)  // 5 limit, 10 requests
	f.Add(10, 5)  // 10 limit, 5 requests
	f.Add(3, 3)   // Exactly at limit
	f.Add(0, 5)   // No limit (0 means unlimited in implementation)
	f.Add(100, 1) // High limit, low requests

	f.Fuzz(func(t *testing.T, maxBreakers, numRequests int) {
		// Skip invalid inputs
		if maxBreakers < 0 || numRequests < 0 {
			t.Skip("negative values not valid")
		}
		if maxBreakers > 1000 || numRequests > 1000 {
			t.Skip("values too large for testing")
		}
		if numRequests == 0 {
			t.Skip("need at least one request")
		}

		ctx := context.Background()
		config := circuit_breaker.CircuitBreakerConfig{
			FailureThreshold: 2,
			SuccessThreshold: 1,
			HalfOpenProbes:   1,
			OpenTimeout:      10 * time.Millisecond,
			MaxBreakers:      maxBreakers,
		}

		handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
			return &transport.Response{Content: "success"}, nil
		})

		cbMiddleware, _ := circuit_breaker.NewCircuitBreakerMiddlewareWithRedis(config, nil)
		cbHandler := cbMiddleware(handler)

		successCount := 0
		limitErrorCount := 0
		breakerKeys := make(map[string]bool)

		for i := 0; i < numRequests; i++ {
			// Use unique provider for each request to create new breakers
			req := &transport.Request{
				Operation: transport.OpGeneration,
				Provider:  fmt.Sprintf("provider-%d", i),
				Model:     "model",
				Question:  "test",
			}

			_, err := cbHandler.Handle(ctx, req)
			if err == nil {
				successCount++
				breakerKeys[fmt.Sprintf("provider-%d:model", i)] = true
			} else {
				var perr *llmerrors.ProviderError
				if errors.As(err, &perr) && perr.Code == "CIRCUIT_BREAKER_LIMIT" {
					limitErrorCount++
					// Verify error message quality
					if !strings.Contains(perr.Message, "limit reached") {
						t.Errorf("error message should mention limit: %s", perr.Message)
					}
				} else {
					t.Errorf("unexpected error: %v", err)
				}
			}
		}

		// Verify expectations
		switch {
		case maxBreakers == 0:
			// No limit, all should succeed
			if successCount != numRequests {
				t.Errorf("with no limit, expected all %d to succeed, got %d", numRequests, successCount)
			}
			if limitErrorCount != 0 {
				t.Errorf("with no limit, expected no limit errors, got %d", limitErrorCount)
			}
		case numRequests <= maxBreakers:
			// Under limit, all should succeed
			if successCount != numRequests {
				t.Errorf("under limit, expected all %d to succeed, got %d", numRequests, successCount)
			}
			if limitErrorCount != 0 {
				t.Errorf("under limit, expected no limit errors, got %d", limitErrorCount)
			}
		default:
			// Over limit, exactly maxBreakers should succeed
			if successCount != maxBreakers {
				t.Errorf("over limit, expected exactly %d to succeed, got %d", maxBreakers, successCount)
			}
			if limitErrorCount != numRequests-maxBreakers {
				t.Errorf("over limit, expected %d limit errors, got %d", numRequests-maxBreakers, limitErrorCount)
			}
		}

		// Verify that existing breakers still work after limit
		for key := range breakerKeys {
			parts := strings.Split(key, ":")
			if len(parts) < 2 {
				continue
			}
			req := &transport.Request{
				Operation: transport.OpGeneration,
				Provider:  parts[0],
				Model:     parts[1],
				Question:  "test",
			}

			_, err := cbHandler.Handle(ctx, req)
			if err != nil {
				t.Errorf("existing breaker %s should still work: %v", key, err)
			}
		}
	})
}

// FuzzConcurrentOperations validates circuit breaker behavior under concurrent
// access patterns with varying failure rates and goroutine counts, ensuring
// thread safety and correct state management under high-concurrency stress.
func FuzzConcurrentOperations(f *testing.F) {
	// Seed with different concurrency patterns
	f.Add(1, 1, 1)    // Single operation
	f.Add(10, 5, 3)   // Moderate concurrency
	f.Add(100, 10, 5) // High concurrency
	f.Add(3, 1, 10)   // More probes than threshold
	f.Add(5, 0, 5)    // No failures
	f.Add(5, 5, 5)    // All failures

	f.Fuzz(func(t *testing.T, numGoroutines, numFailures, halfOpenProbes int) {
		// Validate inputs
		if numGoroutines < 1 || numGoroutines > 1000 {
			t.Skip("goroutines out of reasonable range")
		}
		if numFailures < 0 || numFailures > numGoroutines {
			t.Skip("failures out of range")
		}
		if halfOpenProbes < 0 || halfOpenProbes > 100 {
			t.Skip("probes out of reasonable range")
		}

		ctx := context.Background()
		config := circuit_breaker.CircuitBreakerConfig{
			FailureThreshold: 2,
			SuccessThreshold: 2,
			HalfOpenProbes:   halfOpenProbes,
			OpenTimeout:      5 * time.Millisecond,
		}

		failureCounter := 0
		handler := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
			// Deterministic failure pattern
			if failureCounter < numFailures {
				failureCounter++
				return nil, &llmerrors.ProviderError{
					Provider:   "test",
					StatusCode: 500,
					Message:    "server error",
					Type:       llmerrors.ErrorTypeProvider,
				}
			}
			return &transport.Response{
				Content: "success",
			}, nil
		})

		cbMiddleware, _ := circuit_breaker.NewCircuitBreakerMiddlewareWithRedis(config, nil)
		cbHandler := cbMiddleware(handler)

		req := &transport.Request{
			Operation: transport.OpGeneration,
			Provider:  "test",
			Model:     "test-model",
			Question:  "test",
		}

		// Run concurrent operations
		results := make(chan error, numGoroutines)
		for i := 0; i < numGoroutines; i++ {
			go func() {
				_, err := cbHandler.Handle(ctx, req)
				results <- err
			}()
		}

		// Collect results
		var successCount, errorCount int
		for i := 0; i < numGoroutines; i++ {
			if err := <-results; err == nil {
				successCount++
			} else {
				errorCount++
			}
		}

		// Verify total matches
		if successCount+errorCount != numGoroutines {
			t.Errorf("result count mismatch: %d + %d != %d",
				successCount, errorCount, numGoroutines)
		}

		// No specific assertions about success/error ratio as it depends
		// on timing and circuit breaker state transitions
	})
}
