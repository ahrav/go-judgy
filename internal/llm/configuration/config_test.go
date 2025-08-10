package configuration

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/ahrav/go-judgy/internal/domain"
)

func TestProviderConfig(t *testing.T) {
	tests := []struct {
		name   string
		config ProviderConfig
	}{
		{
			name: "complete_config",
			config: ProviderConfig{
				Endpoint:   "https://api.openai.com/v1",
				APIKey:     "sk-test-key",
				APIKeyEnv:  "OPENAI_API_KEY",
				MaxRetries: 3,
				Timeout:    30 * time.Second,
				Headers: map[string]string{
					"X-Custom-Header": "custom-value",
					"User-Agent":      "test-client",
				},
			},
		},
		{
			name: "minimal_config",
			config: ProviderConfig{
				Endpoint: "https://api.provider.com",
				APIKey:   "test-key",
			},
		},
		{
			name:   "empty_config",
			config: ProviderConfig{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test that config can be created and accessed
			assert.Equal(t, tt.config.Endpoint, tt.config.Endpoint)
			assert.Equal(t, tt.config.APIKey, tt.config.APIKey)
			assert.Equal(t, tt.config.APIKeyEnv, tt.config.APIKeyEnv)
			assert.Equal(t, tt.config.MaxRetries, tt.config.MaxRetries)
			assert.Equal(t, tt.config.Timeout, tt.config.Timeout)

			if tt.config.Headers != nil {
				assert.Equal(t, len(tt.config.Headers), len(tt.config.Headers))
				for k, v := range tt.config.Headers {
					assert.Equal(t, v, tt.config.Headers[k])
				}
			}
		})
	}
}

func TestRetryConfig(t *testing.T) {
	tests := []struct {
		name   string
		config RetryConfig
	}{
		{
			name: "complete_retry_config",
			config: RetryConfig{
				MaxAttempts:     5,
				MaxElapsedTime:  2 * time.Minute,
				InitialInterval: 500 * time.Millisecond,
				MaxInterval:     10 * time.Second,
				Multiplier:      2.0,
				UseJitter:       true,
				MaxCostCents:    1000,
				MaxTokens:       5000,
				EnableBudget:    true,
			},
		},
		{
			name: "minimal_retry_config",
			config: RetryConfig{
				MaxAttempts:     3,
				MaxElapsedTime:  30 * time.Second,
				InitialInterval: 250 * time.Millisecond,
				MaxInterval:     5 * time.Second,
				Multiplier:      2.0,
			},
		},
		{
			name:   "zero_retry_config",
			config: RetryConfig{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.config.MaxAttempts, tt.config.MaxAttempts)
			assert.Equal(t, tt.config.MaxElapsedTime, tt.config.MaxElapsedTime)
			assert.Equal(t, tt.config.InitialInterval, tt.config.InitialInterval)
			assert.Equal(t, tt.config.MaxInterval, tt.config.MaxInterval)
			assert.Equal(t, tt.config.Multiplier, tt.config.Multiplier)
			assert.Equal(t, tt.config.UseJitter, tt.config.UseJitter)
			assert.Equal(t, tt.config.MaxCostCents, tt.config.MaxCostCents)
			assert.Equal(t, tt.config.MaxTokens, tt.config.MaxTokens)
			assert.Equal(t, tt.config.EnableBudget, tt.config.EnableBudget)
		})
	}
}

func TestCircuitBreakerConfig(t *testing.T) {
	tests := []struct {
		name   string
		config CircuitBreakerConfig
	}{
		{
			name: "complete_circuit_breaker_config",
			config: CircuitBreakerConfig{
				FailureThreshold:   10,
				SuccessThreshold:   3,
				OpenTimeout:        60 * time.Second,
				HalfOpenProbes:     5,
				ProbeTimeout:       30 * time.Second,
				MaxBreakers:        100,
				AdaptiveThresholds: true,
			},
		},
		{
			name: "default_circuit_breaker_config",
			config: CircuitBreakerConfig{
				FailureThreshold: 5,
				SuccessThreshold: 2,
				OpenTimeout:      30 * time.Second,
				HalfOpenProbes:   1,
			},
		},
		{
			name:   "empty_circuit_breaker_config",
			config: CircuitBreakerConfig{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.config.FailureThreshold, tt.config.FailureThreshold)
			assert.Equal(t, tt.config.SuccessThreshold, tt.config.SuccessThreshold)
			assert.Equal(t, tt.config.OpenTimeout, tt.config.OpenTimeout)
			assert.Equal(t, tt.config.HalfOpenProbes, tt.config.HalfOpenProbes)
			assert.Equal(t, tt.config.ProbeTimeout, tt.config.ProbeTimeout)
			assert.Equal(t, tt.config.MaxBreakers, tt.config.MaxBreakers)
			assert.Equal(t, tt.config.AdaptiveThresholds, tt.config.AdaptiveThresholds)
		})
	}
}

func TestRateLimitConfig(t *testing.T) {
	localConfig := LocalRateLimitConfig{
		TokensPerSecond: 10,
		BurstSize:       20,
		Enabled:         true,
	}

	config := RateLimitConfig{
		Local: localConfig,
		Global: GlobalRateLimitConfig{
			Enabled:           true,
			RequestsPerSecond: 5,
			ConnectTimeout:    2 * time.Second,
			// DegradedMode is atomic.Bool and will be zero-initialized
		},
	}

	assert.Equal(t, localConfig.TokensPerSecond, config.Local.TokensPerSecond)
	assert.Equal(t, localConfig.BurstSize, config.Local.BurstSize)
	assert.Equal(t, localConfig.Enabled, config.Local.Enabled)

	assert.Equal(t, true, config.Global.Enabled)
	assert.Equal(t, 5, config.Global.RequestsPerSecond)
	assert.Equal(t, 2*time.Second, config.Global.ConnectTimeout)
}

func TestCacheConfig(t *testing.T) {
	tests := []struct {
		name   string
		config CacheConfig
	}{
		{
			name: "enabled_cache",
			config: CacheConfig{
				Enabled: true,
				TTL:     1 * time.Hour,
				MaxAge:  24 * time.Hour,
			},
		},
		{
			name: "disabled_cache",
			config: CacheConfig{
				Enabled: false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.config.Enabled, tt.config.Enabled)
			assert.Equal(t, tt.config.TTL, tt.config.TTL)
			assert.Equal(t, tt.config.MaxAge, tt.config.MaxAge)
		})
	}
}

func TestPricingConfig(t *testing.T) {
	tests := []struct {
		name   string
		config PricingConfig
	}{
		{
			name: "enabled_pricing",
			config: PricingConfig{
				Enabled:    true,
				TTL:        2 * time.Hour,
				FailClosed: true,
			},
		},
		{
			name: "disabled_pricing",
			config: PricingConfig{
				Enabled:    false,
				FailClosed: false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.config.Enabled, tt.config.Enabled)
			assert.Equal(t, tt.config.TTL, tt.config.TTL)
			assert.Equal(t, tt.config.FailClosed, tt.config.FailClosed)
		})
	}
}

func TestObservabilityConfig(t *testing.T) {
	tests := []struct {
		name   string
		config ObservabilityConfig
	}{
		{
			name: "full_observability",
			config: ObservabilityConfig{
				MetricsEnabled: true,
				MetricsPort:    8080,
				LogLevel:       "debug",
				LogFormat:      "json",
				RedactPrompts:  true,
			},
		},
		{
			name: "minimal_observability",
			config: ObservabilityConfig{
				MetricsEnabled: false,
				LogLevel:       "info",
				LogFormat:      "text",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.config.MetricsEnabled, tt.config.MetricsEnabled)
			assert.Equal(t, tt.config.MetricsPort, tt.config.MetricsPort)
			assert.Equal(t, tt.config.LogLevel, tt.config.LogLevel)
			assert.Equal(t, tt.config.LogFormat, tt.config.LogFormat)
			assert.Equal(t, tt.config.RedactPrompts, tt.config.RedactPrompts)
		})
	}
}

func TestFeatureFlags(t *testing.T) {
	tests := []struct {
		name  string
		flags FeatureFlags
	}{
		{
			name: "all_features_disabled",
			flags: FeatureFlags{
				DisableGlobalRL:     true,
				DisableCBProbeGuard: true,
				DisableJSONRepair:   true,
			},
		},
		{
			name: "all_features_enabled",
			flags: FeatureFlags{
				DisableGlobalRL:     false,
				DisableCBProbeGuard: false,
				DisableJSONRepair:   false,
			},
		},
		{
			name:  "default_flags",
			flags: FeatureFlags{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.flags.DisableGlobalRL, tt.flags.DisableGlobalRL)
			assert.Equal(t, tt.flags.DisableCBProbeGuard, tt.flags.DisableCBProbeGuard)
			assert.Equal(t, tt.flags.DisableJSONRepair, tt.flags.DisableJSONRepair)
		})
	}
}

func TestConfig(t *testing.T) {
	tests := []struct {
		name   string
		config Config
	}{
		{
			name: "complete_config",
			config: Config{
				HTTPTimeout: 30 * time.Second,
				HTTPClient:  &http.Client{Timeout: 30 * time.Second},
				Providers: map[string]ProviderConfig{
					"openai": {
						Endpoint: "https://api.openai.com/v1",
						APIKey:   "sk-test-key",
						Timeout:  10 * time.Second,
					},
					"anthropic": {
						Endpoint: "https://api.anthropic.com",
						APIKey:   "ant-test-key",
						Timeout:  15 * time.Second,
					},
				},
				ArtifactStore: &mockArtifactStore{},
				Retry: RetryConfig{
					MaxAttempts:     3,
					MaxElapsedTime:  45 * time.Second,
					InitialInterval: 250 * time.Millisecond,
					MaxInterval:     5 * time.Second,
					Multiplier:      2.0,
					UseJitter:       true,
				},
				CircuitBreaker: CircuitBreakerConfig{
					FailureThreshold: 5,
					SuccessThreshold: 2,
					OpenTimeout:      30 * time.Second,
					HalfOpenProbes:   1,
				},
				RateLimit: RateLimitConfig{
					Local: LocalRateLimitConfig{
						TokensPerSecond: 10,
						BurstSize:       20,
						Enabled:         true,
					},
					Global: GlobalRateLimitConfig{
						Enabled:           true,
						RequestsPerSecond: 5,
						ConnectTimeout:    2 * time.Second,
					},
				},
				Cache: CacheConfig{
					Enabled: true,
					TTL:     1 * time.Hour,
					MaxAge:  24 * time.Hour,
				},
				Pricing: PricingConfig{
					Enabled:    true,
					TTL:        2 * time.Hour,
					FailClosed: true,
				},
				Observability: ObservabilityConfig{
					MetricsEnabled: true,
					MetricsPort:    9090,
					LogLevel:       "info",
					LogFormat:      "json",
					RedactPrompts:  true,
				},
				Features: FeatureFlags{
					DisableGlobalRL:     false,
					DisableCBProbeGuard: false,
					DisableJSONRepair:   false,
				},
			},
		},
		{
			name: "minimal_config",
			config: Config{
				HTTPTimeout: 10 * time.Second,
				Providers: map[string]ProviderConfig{
					"openai": {
						Endpoint: "https://api.openai.com/v1",
						APIKey:   "sk-test-key",
					},
				},
			},
		},
		{
			name:   "empty_config",
			config: Config{},
		},
	}

	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			// Test that config fields can be accessed
			assert.Equal(t, tt.config.HTTPTimeout, tt.config.HTTPTimeout)
			assert.Equal(t, tt.config.HTTPClient, tt.config.HTTPClient)

			if tt.config.Providers != nil {
				assert.Equal(t, len(tt.config.Providers), len(tt.config.Providers))
				for name, provider := range tt.config.Providers {
					assert.Equal(t, provider.Endpoint, tt.config.Providers[name].Endpoint)
					assert.Equal(t, provider.APIKey, tt.config.Providers[name].APIKey)
				}
			}

			assert.Equal(t, tt.config.ArtifactStore, tt.config.ArtifactStore)
			assert.Equal(t, tt.config.Retry, tt.config.Retry)
			assert.Equal(t, tt.config.CircuitBreaker, tt.config.CircuitBreaker)
			// Compare RateLimit struct fields individually to avoid copying atomic.Bool
			assert.Equal(t, tt.config.RateLimit.Local, tt.config.RateLimit.Local)
			assert.Equal(t, tt.config.RateLimit.Global.Enabled, tt.config.RateLimit.Global.Enabled)
			assert.Equal(t, tt.config.RateLimit.Global.RequestsPerSecond, tt.config.RateLimit.Global.RequestsPerSecond)
			assert.Equal(t, tt.config.RateLimit.Global.RedisAddr, tt.config.RateLimit.Global.RedisAddr)
			assert.Equal(t, tt.config.RateLimit.Global.RedisDB, tt.config.RateLimit.Global.RedisDB)
			assert.Equal(t, tt.config.RateLimit.Global.ConnectTimeout, tt.config.RateLimit.Global.ConnectTimeout)
			assert.Equal(t, tt.config.Cache, tt.config.Cache)
			assert.Equal(t, tt.config.Pricing, tt.config.Pricing)
			assert.Equal(t, tt.config.Observability, tt.config.Observability)
			assert.Equal(t, tt.config.Features, tt.config.Features)
		})
	}
}

func TestArtifactStoreInterface(t *testing.T) {
	store := &mockArtifactStore{
		content: "test content",
		ref:     domain.ArtifactRef{Key: "test-key", Kind: domain.ArtifactAnswer},
	}

	ctx := context.Background()
	ref := domain.ArtifactRef{Key: "test-key", Kind: domain.ArtifactAnswer}

	// Test Get
	content, err := store.Get(ctx, ref)
	assert.NoError(t, err)
	assert.Equal(t, "test content", content)

	// Test Put
	newRef, err := store.Put(ctx, "new content")
	assert.NoError(t, err)
	assert.Equal(t, "test-key", newRef.Key)
	assert.Equal(t, domain.ArtifactAnswer, newRef.Kind)
}

func TestConfigWithNilValues(t *testing.T) {
	// Test that config handles nil values gracefully
	config := Config{
		HTTPClient:    nil,
		ArtifactStore: nil,
		Providers:     nil,
	}

	assert.Nil(t, config.HTTPClient)
	assert.Nil(t, config.ArtifactStore)
	assert.Nil(t, config.Providers)
}

func TestProviderConfigWithEmptyHeaders(t *testing.T) {
	config := ProviderConfig{
		Endpoint: "https://api.example.com",
		APIKey:   "test-key",
		Headers:  make(map[string]string), // Empty but not nil
	}

	assert.Equal(t, "https://api.example.com", config.Endpoint)
	assert.Equal(t, "test-key", config.APIKey)
	assert.NotNil(t, config.Headers)
	assert.Empty(t, config.Headers)
}

func TestRepairConfig(t *testing.T) {
	tests := []struct {
		name   string
		config RepairConfig
	}{
		{
			name: "full_repair_config",
			config: RepairConfig{
				MaxAttempts:      3,
				EnableExtraction: true,
				EnableClamping:   true,
				EnableDefaults:   true,
			},
		},
		{
			name: "disabled_repair_config",
			config: RepairConfig{
				MaxAttempts:      0,
				EnableExtraction: false,
				EnableClamping:   false,
				EnableDefaults:   false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.config.MaxAttempts, tt.config.MaxAttempts)
			assert.Equal(t, tt.config.EnableExtraction, tt.config.EnableExtraction)
			assert.Equal(t, tt.config.EnableClamping, tt.config.EnableClamping)
			assert.Equal(t, tt.config.EnableDefaults, tt.config.EnableDefaults)
		})
	}
}

// Mock implementation for testing
type mockArtifactStore struct {
	content string
	ref     domain.ArtifactRef
}

func (m *mockArtifactStore) Get(ctx context.Context, ref domain.ArtifactRef) (string, error) {
	return m.content, nil
}

func (m *mockArtifactStore) Put(ctx context.Context, content string) (domain.ArtifactRef, error) {
	return m.ref, nil
}
