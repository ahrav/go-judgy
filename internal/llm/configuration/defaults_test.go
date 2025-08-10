package configuration

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConstants(t *testing.T) {
	// Test HTTP and connection constants
	assert.Equal(t, 100, DefaultMaxIdleConns)
	assert.Equal(t, 90, DefaultIdleTimeoutSeconds)
	assert.Equal(t, 10, DefaultTLSTimeoutSeconds)
	assert.Equal(t, 30, DefaultHTTPTimeoutSeconds)
	assert.Equal(t, 500, ServerErrorStatusThreshold)

	// Test retry and circuit breaker constants
	assert.Equal(t, 3, DefaultMaxAttempts)
	assert.Equal(t, 45*time.Second, DefaultMaxElapsedTime)
	assert.Equal(t, 250*time.Millisecond, DefaultInitialInterval)
	assert.Equal(t, 5*time.Second, DefaultMaxInterval)
	assert.Equal(t, 2.0, DefaultBackoffMultiplier)
	assert.Equal(t, 5, DefaultFailureThreshold)
	assert.Equal(t, 2, DefaultSuccessThreshold)
	assert.Equal(t, 30*time.Second, DefaultOpenTimeout)
	assert.Equal(t, 60*time.Second, DefaultProbeTimeout)
	assert.Equal(t, 1000, DefaultMaxBreakers)

	// Test rate limiting constants
	assert.Equal(t, 10, DefaultTokensPerSecond)
	assert.Equal(t, 20, DefaultBurstSize)
	assert.Equal(t, 5*time.Second, DefaultConnectTimeout)

	// Test cache and pricing constants
	assert.Equal(t, 24*time.Hour, DefaultCacheTTL)
	assert.Equal(t, 7, DefaultCacheMaxAgeRatio)
	assert.Equal(t, 9090, DefaultMetricsPort)
	assert.Equal(t, 24*time.Hour, PricingValidFor)
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()
	require.NotNil(t, config)

	// Test HTTP timeout
	assert.Equal(t, DefaultHTTPTimeoutSeconds*time.Second, config.HTTPTimeout)

	// Test retry configuration
	assert.Equal(t, DefaultMaxAttempts, config.Retry.MaxAttempts)
	assert.Equal(t, DefaultMaxElapsedTime, config.Retry.MaxElapsedTime)
	assert.Equal(t, DefaultInitialInterval, config.Retry.InitialInterval)
	assert.Equal(t, DefaultMaxInterval, config.Retry.MaxInterval)
	assert.Equal(t, DefaultBackoffMultiplier, config.Retry.Multiplier)
	assert.True(t, config.Retry.UseJitter)

	// Test circuit breaker configuration
	assert.Equal(t, DefaultFailureThreshold, config.CircuitBreaker.FailureThreshold)
	assert.Equal(t, DefaultSuccessThreshold, config.CircuitBreaker.SuccessThreshold)
	assert.Equal(t, DefaultOpenTimeout, config.CircuitBreaker.OpenTimeout)
	assert.Equal(t, 1, config.CircuitBreaker.HalfOpenProbes)
	assert.Equal(t, DefaultProbeTimeout, config.CircuitBreaker.ProbeTimeout)
	assert.Equal(t, DefaultMaxBreakers, config.CircuitBreaker.MaxBreakers)

	// Test rate limit configuration
	assert.Equal(t, float64(DefaultTokensPerSecond), config.RateLimit.Local.TokensPerSecond)
	assert.Equal(t, DefaultBurstSize, config.RateLimit.Local.BurstSize)
	assert.True(t, config.RateLimit.Local.Enabled)
	assert.True(t, config.RateLimit.Global.Enabled)
	assert.Equal(t, int(DefaultTokensPerSecond), config.RateLimit.Global.RequestsPerSecond)
	assert.Equal(t, DefaultConnectTimeout, config.RateLimit.Global.ConnectTimeout)

	// Test cache configuration
	assert.True(t, config.Cache.Enabled)
	assert.Equal(t, DefaultCacheTTL, config.Cache.TTL)
	assert.Equal(t, DefaultCacheMaxAgeRatio*DefaultCacheTTL, config.Cache.MaxAge)

	// Test pricing configuration
	assert.True(t, config.Pricing.Enabled)
	assert.Equal(t, 1*time.Hour, config.Pricing.TTL)
	assert.True(t, config.Pricing.FailClosed)

	// Test observability configuration
	assert.True(t, config.Observability.MetricsEnabled)
	assert.Equal(t, DefaultMetricsPort, config.Observability.MetricsPort)
	assert.Equal(t, "info", config.Observability.LogLevel)
	assert.Equal(t, "json", config.Observability.LogFormat)
	assert.True(t, config.Observability.RedactPrompts)

	// Test feature flags
	assert.False(t, config.Features.DisableGlobalRL)
	assert.False(t, config.Features.DisableCBProbeGuard)
	assert.False(t, config.Features.DisableJSONRepair)

	// Test that ArtifactStore is not set (should be set by caller)
	assert.Nil(t, config.ArtifactStore)
}

func TestDefaultRepairConfig(t *testing.T) {
	config := DefaultRepairConfig()
	require.NotNil(t, config)

	// Test default repair configuration values
	assert.Equal(t, 1, config.MaxAttempts)
	assert.True(t, config.EnableExtraction)
	assert.True(t, config.EnableClamping)
	assert.True(t, config.EnableDefaults)
}

func TestDefaultConfigConsistency(t *testing.T) {
	// Test that default config creates a consistent configuration
	config := DefaultConfig()

	// Test that all required fields are set with sensible values
	assert.Greater(t, config.HTTPTimeout, time.Duration(0))
	assert.Greater(t, config.Retry.MaxAttempts, 0)
	assert.Greater(t, config.Retry.MaxElapsedTime, time.Duration(0))
	assert.Greater(t, config.Retry.InitialInterval, time.Duration(0))
	assert.Greater(t, config.Retry.MaxInterval, time.Duration(0))
	assert.Greater(t, config.Retry.Multiplier, float64(0))

	assert.Greater(t, config.CircuitBreaker.FailureThreshold, 0)
	assert.Greater(t, config.CircuitBreaker.SuccessThreshold, 0)
	assert.Greater(t, config.CircuitBreaker.OpenTimeout, time.Duration(0))
	assert.Greater(t, config.CircuitBreaker.HalfOpenProbes, 0)
	assert.Greater(t, config.CircuitBreaker.ProbeTimeout, time.Duration(0))
	assert.Greater(t, config.CircuitBreaker.MaxBreakers, 0)

	assert.Greater(t, config.RateLimit.Local.TokensPerSecond, float64(0))
	assert.Greater(t, config.RateLimit.Local.BurstSize, 0)
	assert.Greater(t, config.RateLimit.Global.RequestsPerSecond, 0)
	assert.Greater(t, config.RateLimit.Global.ConnectTimeout, time.Duration(0))

	assert.Greater(t, config.Cache.TTL, time.Duration(0))
	assert.Greater(t, config.Cache.MaxAge, time.Duration(0))
	assert.Greater(t, config.Cache.MaxAge, config.Cache.TTL) // MaxAge should be longer than TTL

	assert.Greater(t, config.Pricing.TTL, time.Duration(0))

	assert.Greater(t, config.Observability.MetricsPort, 0)
	assert.NotEmpty(t, config.Observability.LogLevel)
	assert.NotEmpty(t, config.Observability.LogFormat)
}

func TestDefaultConfigImmutability(t *testing.T) {
	// Test that calling DefaultConfig() multiple times returns separate instances
	config1 := DefaultConfig()
	config2 := DefaultConfig()

	// Should be different pointers
	assert.NotSame(t, config1, config2)

	// But should have equal values
	assert.Equal(t, config1.HTTPTimeout, config2.HTTPTimeout)
	assert.Equal(t, config1.Retry, config2.Retry)
	assert.Equal(t, config1.CircuitBreaker, config2.CircuitBreaker)
	// Compare RateLimit struct fields individually to avoid copying atomic.Bool
	assert.Equal(t, config1.RateLimit.Local, config2.RateLimit.Local)
	assert.Equal(t, config1.RateLimit.Global.Enabled, config2.RateLimit.Global.Enabled)
	assert.Equal(t, config1.RateLimit.Global.RequestsPerSecond, config2.RateLimit.Global.RequestsPerSecond)
	assert.Equal(t, config1.RateLimit.Global.RedisAddr, config2.RateLimit.Global.RedisAddr)
	assert.Equal(t, config1.RateLimit.Global.RedisDB, config2.RateLimit.Global.RedisDB)
	assert.Equal(t, config1.RateLimit.Global.ConnectTimeout, config2.RateLimit.Global.ConnectTimeout)
	assert.Equal(t, config1.Cache, config2.Cache)
	assert.Equal(t, config1.Pricing, config2.Pricing)
	assert.Equal(t, config1.Observability, config2.Observability)
	assert.Equal(t, config1.Features, config2.Features)

	// Modifying one should not affect the other
	config1.HTTPTimeout = 60 * time.Second
	assert.NotEqual(t, config1.HTTPTimeout, config2.HTTPTimeout)
}

func TestDefaultRepairConfigImmutability(t *testing.T) {
	// Test that calling DefaultRepairConfig() multiple times returns separate instances
	config1 := DefaultRepairConfig()
	config2 := DefaultRepairConfig()

	// Should be different pointers
	assert.NotSame(t, config1, config2)

	// But should have equal values
	assert.Equal(t, config1.MaxAttempts, config2.MaxAttempts)
	assert.Equal(t, config1.EnableExtraction, config2.EnableExtraction)
	assert.Equal(t, config1.EnableClamping, config2.EnableClamping)
	assert.Equal(t, config1.EnableDefaults, config2.EnableDefaults)

	// Modifying one should not affect the other
	config1.MaxAttempts = 5
	assert.NotEqual(t, config1.MaxAttempts, config2.MaxAttempts)
}

func TestDefaultConfigRateLimitConsistency(t *testing.T) {
	config := DefaultConfig()

	// Test that local and global rate limits use the same default value for consistency
	assert.Equal(t, config.RateLimit.Local.TokensPerSecond, float64(config.RateLimit.Global.RequestsPerSecond))

	// Test that burst size is appropriately larger than tokens per second
	assert.Greater(t, float64(config.RateLimit.Local.BurstSize), config.RateLimit.Local.TokensPerSecond)
}

func TestDefaultConfigCacheCalculation(t *testing.T) {
	config := DefaultConfig()

	// Test that MaxAge is calculated correctly from TTL and ratio
	expectedMaxAge := DefaultCacheMaxAgeRatio * DefaultCacheTTL
	assert.Equal(t, expectedMaxAge, config.Cache.MaxAge)

	// Test that the calculation results in MaxAge being significantly longer than TTL
	assert.Greater(t, config.Cache.MaxAge, config.Cache.TTL)

	// Test specific duration values
	assert.Equal(t, 24*time.Hour, config.Cache.TTL)
	assert.Equal(t, 7*24*time.Hour, config.Cache.MaxAge) // 7 days
}

func TestDefaultConfigServerErrorThreshold(t *testing.T) {
	// Test that the server error threshold matches the constant value
	assert.Equal(t, 500, ServerErrorStatusThreshold)

	// Test that it's the expected boundary for 5xx errors
	assert.True(t, ServerErrorStatusThreshold >= 500)
	assert.True(t, ServerErrorStatusThreshold < 600)
}
