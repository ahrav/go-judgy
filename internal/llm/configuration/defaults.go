package configuration

import (
	"time"
)

// HTTP and connection constants.
const (
	DefaultMaxIdleConns        = 100
	DefaultIdleTimeoutSeconds  = 90
	DefaultTLSTimeoutSeconds   = 10
	DefaultHTTPTimeoutSeconds  = 30
	ServerErrorStatusThreshold = 500
)

// Retry and circuit breaker constants.
const (
	DefaultMaxAttempts       = 3
	DefaultMaxElapsedTime    = 45 * time.Second
	DefaultInitialInterval   = 250 * time.Millisecond
	DefaultMaxInterval       = 5 * time.Second
	DefaultBackoffMultiplier = 2.0
	DefaultFailureThreshold  = 5
	DefaultSuccessThreshold  = 2
	DefaultOpenTimeout       = 30 * time.Second
	DefaultProbeTimeout      = 60 * time.Second // Timeout for Redis probe guard
	DefaultMaxBreakers       = 1000             // Maximum circuit breakers to prevent unbounded growth
)

// Rate limiting constants.
const (
	DefaultTokensPerSecond = 10
	DefaultBurstSize       = 20
	DefaultConnectTimeout  = 5 * time.Second
)

// Cache and pricing constants.
const (
	DefaultCacheTTL         = 24 * time.Hour
	DefaultCacheMaxAgeRatio = 7 // MaxAge = 7x TTL for staleness protection
	DefaultMetricsPort      = 9090
	PricingValidFor         = 24 * time.Hour
)

// Concurrency constants.
const (
	DefaultMaxConcurrency = 5 // Maximum concurrent requests for answer generation
)

// DefaultConfig returns production-ready configuration with sensible defaults.
// Provides balanced settings for resilience, performance, and cost control
// suitable for production workloads without additional configuration.
func DefaultConfig() *Config {
	return &Config{
		HTTPTimeout: DefaultHTTPTimeoutSeconds * time.Second,
		// ArtifactStore will be set by the caller since it depends on business package
		Retry: RetryConfig{
			MaxAttempts:     DefaultMaxAttempts,
			MaxElapsedTime:  DefaultMaxElapsedTime,
			InitialInterval: DefaultInitialInterval,
			MaxInterval:     DefaultMaxInterval,
			Multiplier:      DefaultBackoffMultiplier,
			UseJitter:       true,
		},
		CircuitBreaker: CircuitBreakerConfig{
			FailureThreshold: DefaultFailureThreshold,
			SuccessThreshold: DefaultSuccessThreshold,
			OpenTimeout:      DefaultOpenTimeout,
			HalfOpenProbes:   1,
			ProbeTimeout:     DefaultProbeTimeout,
			MaxBreakers:      DefaultMaxBreakers,
		},
		RateLimit: RateLimitConfig{
			Local: LocalRateLimitConfig{
				TokensPerSecond: DefaultTokensPerSecond,
				BurstSize:       DefaultBurstSize,
				Enabled:         true,
			},
			Global: GlobalRateLimitConfig{
				Enabled:           true,
				RequestsPerSecond: DefaultTokensPerSecond, // Use same default as local
				ConnectTimeout:    DefaultConnectTimeout,
			},
		},
		Cache: CacheConfig{
			Enabled: true,
			TTL:     DefaultCacheTTL,
			MaxAge:  DefaultCacheMaxAgeRatio * DefaultCacheTTL,
		},
		Pricing: PricingConfig{
			Enabled:    true,
			TTL:        1 * time.Hour,
			FailClosed: true,
		},
		Observability: ObservabilityConfig{
			MetricsEnabled: true,
			MetricsPort:    DefaultMetricsPort,
			LogLevel:       "info",
			LogFormat:      "json",
			RedactPrompts:  true,
		},
		Features: FeatureFlags{
			DisableGlobalRL:     false,
			DisableCBProbeGuard: false,
			DisableJSONRepair:   false,
		},
		MaxConcurrency: DefaultMaxConcurrency,
	}
}

// DefaultRepairConfig provides conservative repair settings for production use.
// Enables one-shot repair with all repair features to maximize compatibility
// with LLM response variations while maintaining safety boundaries.
func DefaultRepairConfig() *RepairConfig {
	return &RepairConfig{
		MaxAttempts:      1,
		EnableExtraction: true,
		EnableClamping:   true,
		EnableDefaults:   true,
	}
}
