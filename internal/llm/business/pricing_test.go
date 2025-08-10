package business

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ahrav/go-judgy/internal/llm/configuration"
	"github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

func TestPricingEntry_Key(t *testing.T) {
	tests := []struct {
		name     string
		entry    PricingEntry
		expected string
	}{
		{
			name: "without_region",
			entry: PricingEntry{
				Provider: "openai",
				Model:    "gpt-4",
			},
			expected: "openai/gpt-4",
		},
		{
			name: "with_region",
			entry: PricingEntry{
				Provider: "openai",
				Model:    "gpt-4",
				Region:   "us-east-1",
			},
			expected: "openai/gpt-4/us-east-1",
		},
		{
			name: "with_empty_region",
			entry: PricingEntry{
				Provider: "anthropic",
				Model:    "claude-3",
				Region:   "",
			},
			expected: "anthropic/claude-3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.entry.Key()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPricingEntry_IsExpired(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name      string
		validFrom time.Time
		expected  bool
	}{
		{
			name:      "expired",
			validFrom: now.Add(-time.Hour),
			expected:  true,
		},
		{
			name:      "not_expired",
			validFrom: now.Add(time.Hour),
			expected:  false,
		},
		{
			name:      "just_expired",
			validFrom: now.Add(-time.Nanosecond), // Just expired
			expected:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := PricingEntry{
				ValidUntil: tt.validFrom,
			}
			result := entry.IsExpired()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPricingEntry_Calculate(t *testing.T) {
	entry := PricingEntry{
		PromptCostPer1000: 1500, // 1.5 cents per 1000 prompt tokens
		OutputCostPer1000: 2000, // 2.0 cents per 1000 output tokens
	}

	tests := []struct {
		name     string
		usage    transport.NormalizedUsage
		expected int64
	}{
		{
			name: "zero_usage",
			usage: transport.NormalizedUsage{
				PromptTokens:     0,
				CompletionTokens: 0,
			},
			expected: 0,
		},
		{
			name: "prompt_tokens_only",
			usage: transport.NormalizedUsage{
				PromptTokens:     1000,
				CompletionTokens: 0,
			},
			expected: 1500, // 1000 * 1500 / 1000 = 1500 milli-cents
		},
		{
			name: "completion_tokens_only",
			usage: transport.NormalizedUsage{
				PromptTokens:     0,
				CompletionTokens: 1000,
			},
			expected: 2000, // 1000 * 2000 / 1000 = 2000 milli-cents
		},
		{
			name: "both_token_types",
			usage: transport.NormalizedUsage{
				PromptTokens:     500,
				CompletionTokens: 300,
			},
			expected: 1350, // (500 * 1500 + 300 * 2000) / 1000 = (750 + 600) = 1350 milli-cents
		},
		{
			name: "larger_usage",
			usage: transport.NormalizedUsage{
				PromptTokens:     5000,
				CompletionTokens: 2000,
			},
			expected: 11500, // (5000 * 1500 + 2000 * 2000) / 1000 = (7500 + 4000) = 11500 milli-cents
		},
		{
			name: "fractional_tokens",
			usage: transport.NormalizedUsage{
				PromptTokens:     100,
				CompletionTokens: 50,
			},
			expected: 250, // (100 * 1500 + 50 * 2000) / 1000 = (150 + 100) = 250 milli-cents
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := entry.Calculate(tt.usage)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCostDriftMetrics_RecordDrift(t *testing.T) {
	t.Run("nil_metrics", func(t *testing.T) {
		var metrics *CostDriftMetrics
		// Should not panic
		metrics.RecordDrift("provider", "model", 100, 95)
	})

	t.Run("record_single_drift", func(t *testing.T) {
		metrics := &CostDriftMetrics{
			observations: make(map[string]*DriftObservation),
		}

		metrics.RecordDrift("openai", "gpt-4", 100, 95)

		key := "openai/gpt-4"
		obs, exists := metrics.observations[key]
		require.True(t, exists)
		assert.Equal(t, "openai", obs.Provider)
		assert.Equal(t, "gpt-4", obs.Model)
		assert.Equal(t, int64(100), obs.EstimatedSum)
		assert.Equal(t, int64(95), obs.ActualSum)
		assert.Equal(t, int64(1), obs.ObservationCount)
		assert.Equal(t, int64(-5), obs.MaxDrift) // 95 - 100 = -5
		assert.False(t, obs.LastUpdated.IsZero())
	})

	t.Run("record_multiple_drifts", func(t *testing.T) {
		metrics := &CostDriftMetrics{
			observations: make(map[string]*DriftObservation),
		}

		metrics.RecordDrift("openai", "gpt-4", 100, 95)
		metrics.RecordDrift("openai", "gpt-4", 200, 210)

		key := "openai/gpt-4"
		obs := metrics.observations[key]
		assert.Equal(t, int64(300), obs.EstimatedSum) // 100 + 200
		assert.Equal(t, int64(305), obs.ActualSum)    // 95 + 210
		assert.Equal(t, int64(2), obs.ObservationCount)
		assert.Equal(t, int64(10), obs.MaxDrift) // max(|-5|, |10|) = 10
	})
}

func TestCostDriftMetrics_GetDriftPercentage(t *testing.T) {
	t.Run("nil_metrics", func(t *testing.T) {
		var metrics *CostDriftMetrics
		result := metrics.GetDriftPercentage("provider", "model")
		assert.Equal(t, float64(0), result)
	})

	t.Run("no_observations", func(t *testing.T) {
		metrics := &CostDriftMetrics{
			observations: make(map[string]*DriftObservation),
		}
		result := metrics.GetDriftPercentage("openai", "gpt-4")
		assert.Equal(t, float64(0), result)
	})

	t.Run("zero_estimated_sum", func(t *testing.T) {
		metrics := &CostDriftMetrics{
			observations: map[string]*DriftObservation{
				"openai/gpt-4": {
					EstimatedSum: 0,
					ActualSum:    100,
				},
			},
		}
		result := metrics.GetDriftPercentage("openai", "gpt-4")
		assert.Equal(t, float64(0), result)
	})

	t.Run("positive_drift", func(t *testing.T) {
		metrics := &CostDriftMetrics{
			observations: map[string]*DriftObservation{
				"openai/gpt-4": {
					EstimatedSum: 100,
					ActualSum:    110, // 10% higher
				},
			},
		}
		result := metrics.GetDriftPercentage("openai", "gpt-4")
		assert.InDelta(t, 10.0, result, 0.001) // 10% drift
	})

	t.Run("negative_drift", func(t *testing.T) {
		metrics := &CostDriftMetrics{
			observations: map[string]*DriftObservation{
				"openai/gpt-4": {
					EstimatedSum: 100,
					ActualSum:    85, // 15% lower
				},
			},
		}
		result := metrics.GetDriftPercentage("openai", "gpt-4")
		assert.InDelta(t, -15.0, result, 0.001) // -15% drift
	})
}

func TestNewInMemoryPricingRegistry(t *testing.T) {
	t.Run("fail_open", func(t *testing.T) {
		registry := NewInMemoryPricingRegistry(false)
		assert.NotNil(t, registry)
		assert.False(t, registry.failClosed)
		assert.NotNil(t, registry.entries)
		assert.NotNil(t, registry.driftMetrics)
		assert.Greater(t, len(registry.entries), 0) // Should have default entries
		assert.False(t, registry.lastUpdated.IsZero())
	})

	t.Run("fail_closed", func(t *testing.T) {
		registry := NewInMemoryPricingRegistry(true)
		assert.True(t, registry.failClosed)
	})
}

func TestInMemoryPricingRegistry_GetCost(t *testing.T) {
	registry := NewInMemoryPricingRegistry(false)
	ctx := context.Background()
	usage := transport.NormalizedUsage{
		PromptTokens:     1000,
		CompletionTokens: 500,
		TotalTokens:      1500,
	}

	t.Run("existing_provider_model", func(t *testing.T) {
		cost, err := registry.GetCost(ctx, "openai", "gpt-4", "", usage)
		require.NoError(t, err)
		assert.Greater(t, cost, int64(0))
	})

	t.Run("nonexistent_provider_fail_open", func(t *testing.T) {
		registry := NewInMemoryPricingRegistry(false)
		cost, err := registry.GetCost(ctx, "nonexistent", "model", "", usage)
		require.NoError(t, err)
		assert.Equal(t, int64(0), cost)
	})

	t.Run("nonexistent_provider_fail_closed", func(t *testing.T) {
		registry := NewInMemoryPricingRegistry(true)
		cost, err := registry.GetCost(ctx, "nonexistent", "model", "", usage)
		require.Error(t, err)
		assert.Equal(t, int64(0), cost)

		var pricingErr *errors.PricingError
		assert.ErrorAs(t, err, &pricingErr)
		assert.Equal(t, "nonexistent", pricingErr.Provider)
		assert.Equal(t, "model", pricingErr.Model)
		assert.Equal(t, errors.ErrorTypePricingUnavailable, pricingErr.Type)
	})

	t.Run("regional_fallback", func(t *testing.T) {
		// Add a regional entry
		registry.AddEntry(&PricingEntry{
			Provider:          "test-provider",
			Model:             "test-model",
			Region:            "us-east-1",
			PromptCostPer1000: 1000,
			OutputCostPer1000: 2000,
			ValidUntil:        time.Now().Add(time.Hour),
		})

		// Add a global entry (no region)
		registry.AddEntry(&PricingEntry{
			Provider:          "test-provider",
			Model:             "test-model",
			PromptCostPer1000: 500,
			OutputCostPer1000: 1000,
			ValidUntil:        time.Now().Add(time.Hour),
		})

		// Test regional lookup
		cost, err := registry.GetCost(ctx, "test-provider", "test-model", "us-east-1", usage)
		require.NoError(t, err)
		assert.Equal(t, int64(2000), cost) // (1000 * 1000 + 500 * 2000) / 1000 = 2000 milli-cents

		// Test fallback to global
		cost, err = registry.GetCost(ctx, "test-provider", "test-model", "us-west-2", usage)
		require.NoError(t, err)
		assert.Equal(t, int64(1000), cost) // (1000 * 500 + 500 * 1000) / 1000 = 1000 milli-cents
	})

	t.Run("expired_entry_fail_open", func(t *testing.T) {
		registry := NewInMemoryPricingRegistry(false)
		registry.AddEntry(&PricingEntry{
			Provider:          "expired-provider",
			Model:             "expired-model",
			PromptCostPer1000: 1000,
			OutputCostPer1000: 2000,
			ValidUntil:        time.Now().Add(-time.Hour), // Expired
		})

		cost, err := registry.GetCost(ctx, "expired-provider", "expired-model", "", usage)
		require.NoError(t, err)
		assert.Greater(t, cost, int64(0)) // Should still calculate with expired data
	})

	t.Run("expired_entry_fail_closed", func(t *testing.T) {
		registry := NewInMemoryPricingRegistry(true)
		registry.AddEntry(&PricingEntry{
			Provider:          "expired-provider",
			Model:             "expired-model",
			PromptCostPer1000: 1000,
			OutputCostPer1000: 2000,
			ValidUntil:        time.Now().Add(-time.Hour), // Expired
		})

		cost, err := registry.GetCost(ctx, "expired-provider", "expired-model", "", usage)
		require.Error(t, err)
		assert.Equal(t, int64(0), cost)

		var pricingErr *errors.PricingError
		assert.ErrorAs(t, err, &pricingErr)
		assert.Contains(t, pricingErr.Reason, "pricing data expired")
	})
}

func TestInMemoryPricingRegistry_IsAvailable(t *testing.T) {
	registry := NewInMemoryPricingRegistry(false)

	t.Run("available_entry", func(t *testing.T) {
		available := registry.IsAvailable("openai", "gpt-4", "")
		assert.True(t, available)
	})

	t.Run("unavailable_entry", func(t *testing.T) {
		available := registry.IsAvailable("nonexistent", "model", "")
		assert.False(t, available)
	})

	t.Run("expired_entry", func(t *testing.T) {
		registry.AddEntry(&PricingEntry{
			Provider:   "expired-provider",
			Model:      "expired-model",
			ValidUntil: time.Now().Add(-time.Hour), // Expired
		})

		available := registry.IsAvailable("expired-provider", "expired-model", "")
		assert.False(t, available)
	})

	t.Run("regional_fallback", func(t *testing.T) {
		registry.AddEntry(&PricingEntry{
			Provider:   "regional-provider",
			Model:      "regional-model",
			ValidUntil: time.Now().Add(time.Hour),
		})

		// Should find global entry when regional not available
		available := registry.IsAvailable("regional-provider", "regional-model", "us-east-1")
		assert.True(t, available)
	})
}

func TestInMemoryPricingRegistry_Refresh(t *testing.T) {
	t.Run("successful_refresh", func(t *testing.T) {
		registry := NewInMemoryPricingRegistry(false)
		ctx := context.Background()

		// Force a refresh by setting an old lastUpdated time
		registry.lastUpdated = time.Now().Add(-2 * configuration.PricingValidFor)

		err := registry.Refresh(ctx)
		assert.NoError(t, err)

		// Check that lastUpdated was refreshed
		assert.True(t, time.Since(registry.lastUpdated) < time.Minute)
	})

	t.Run("no_refresh_needed", func(t *testing.T) {
		registry := NewInMemoryPricingRegistry(false)
		ctx := context.Background()

		// Set recent lastUpdated
		registry.lastUpdated = time.Now()

		err := registry.Refresh(ctx)
		assert.NoError(t, err)
	})
}

func TestInMemoryPricingRegistry_LastUpdated(t *testing.T) {
	registry := NewInMemoryPricingRegistry(false)
	lastUpdated := registry.LastUpdated()
	assert.False(t, lastUpdated.IsZero())
	assert.True(t, time.Since(lastUpdated) < time.Minute)
}

func TestInMemoryPricingRegistry_AddEntry(t *testing.T) {
	registry := NewInMemoryPricingRegistry(false)
	oldLastUpdated := registry.LastUpdated()

	// Wait a small amount to ensure timestamp difference
	time.Sleep(10 * time.Millisecond)

	entry := &PricingEntry{
		Provider:          "new-provider",
		Model:             "new-model",
		PromptCostPer1000: 1000,
		OutputCostPer1000: 2000,
		ValidUntil:        time.Now().Add(time.Hour),
	}

	registry.AddEntry(entry)

	// Check that entry was added
	assert.True(t, registry.IsAvailable("new-provider", "new-model", ""))

	// Check that lastUpdated was updated
	assert.True(t, registry.LastUpdated().After(oldLastUpdated))
}

func TestInMemoryPricingRegistry_CostDrift(t *testing.T) {
	registry := NewInMemoryPricingRegistry(false)

	t.Run("record_and_get_drift", func(t *testing.T) {
		registry.RecordCostDrift("openai", "gpt-4", 100, 105)

		drift := registry.GetDriftMetrics("openai", "gpt-4")
		assert.InDelta(t, 5.0, drift, 0.001) // 5% positive drift
	})

	t.Run("no_drift_data", func(t *testing.T) {
		drift := registry.GetDriftMetrics("nonexistent", "model")
		assert.Equal(t, float64(0), drift)
	})
}

func TestNewPricingMiddlewareWithRegistry(t *testing.T) {
	registry := NewInMemoryPricingRegistry(false)
	middleware := NewPricingMiddlewareWithRegistry(registry)

	handler := transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
		return &transport.Response{
			Content: "test response",
			Usage: transport.NormalizedUsage{
				PromptTokens:     1000,
				CompletionTokens: 500,
				TotalTokens:      1500,
			},
		}, nil
	})

	wrappedHandler := middleware(handler)

	t.Run("successful_pricing", func(t *testing.T) {
		req := &transport.Request{
			Provider: "openai",
			Model:    "gpt-4",
		}

		resp, err := wrappedHandler.Handle(context.Background(), req)
		require.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Greater(t, resp.EstimatedCostMilliCents, int64(0))
	})

	t.Run("handler_error_passthrough", func(t *testing.T) {
		errorHandler := transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
			return nil, fmt.Errorf("handler error")
		})

		wrappedErrorHandler := middleware(errorHandler)

		req := &transport.Request{
			Provider: "openai",
			Model:    "gpt-4",
		}

		resp, err := wrappedErrorHandler.Handle(context.Background(), req)
		require.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "handler error")
	})

	t.Run("pricing_error_fail_closed", func(t *testing.T) {
		failClosedRegistry := NewInMemoryPricingRegistry(true)
		failClosedMiddleware := NewPricingMiddlewareWithRegistry(failClosedRegistry)
		wrappedHandler := failClosedMiddleware(handler)

		req := &transport.Request{
			Provider: "nonexistent",
			Model:    "model",
		}

		resp, err := wrappedHandler.Handle(context.Background(), req)
		require.Error(t, err)
		assert.Nil(t, resp)

		var pricingErr *errors.PricingError
		assert.ErrorAs(t, err, &pricingErr)
	})
}

func TestBuildPricingKey(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		model    string
		region   string
		expected string
	}{
		{
			name:     "without_region",
			provider: "openai",
			model:    "gpt-4",
			region:   "",
			expected: "openai/gpt-4",
		},
		{
			name:     "with_region",
			provider: "openai",
			model:    "gpt-4",
			region:   "us-east-1",
			expected: "openai/gpt-4/us-east-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildPricingKey(tt.provider, tt.model, tt.region)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPricingConstants(t *testing.T) {
	// Test that constants have expected values
	assert.Equal(t, 1000, MilliCentsToFactor)
	assert.Equal(t, 100, HundredPercent)
	assert.Equal(t, 5, RefreshBufferMinutes)

	// Test pricing constants are reasonable (not zero)
	assert.Greater(t, GPT4oPromptCost, 0)
	assert.Greater(t, GPT4oOutputCost, 0)
	assert.Greater(t, GPT4oMiniPromptCost, 0)
	assert.Greater(t, GPT4oMiniOutputCost, 0)
	assert.Greater(t, Claude35SonnetPromptCost, 0)
	assert.Greater(t, Claude35SonnetOutputCost, 0)
	assert.Greater(t, Claude35HaikuPromptCost, 0)
	assert.Greater(t, Claude35HaikuOutputCost, 0)
	assert.Greater(t, Gemini15FlashPromptCost, 0)
	assert.Greater(t, Gemini15FlashOutputCost, 0)

	// Test that output costs are generally higher than prompt costs
	assert.Greater(t, GPT4oOutputCost, GPT4oPromptCost)
	assert.Greater(t, GPT4oMiniOutputCost, GPT4oMiniPromptCost)
	assert.Greater(t, Claude35SonnetOutputCost, Claude35SonnetPromptCost)
	assert.Greater(t, Claude35HaikuOutputCost, Claude35HaikuPromptCost)
	assert.Greater(t, Gemini15FlashOutputCost, Gemini15FlashPromptCost)
}
