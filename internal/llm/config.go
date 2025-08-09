// Package llm provides a unified, resilient HTTP client for Large Language Model providers.
package llm

import (
	"net/http"
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
)

// Rate limiting constants.
const (
	DefaultTokensPerSecond = 10
	DefaultBurstSize       = 20
	DefaultConnectTimeout  = 5 * time.Second
)

// Cache and pricing constants.
const (
	DefaultCacheTTL    = 24 * time.Hour
	DefaultMetricsPort = 9090
	PricingValidFor    = 24 * time.Hour
)

// Config holds comprehensive configuration for the LLM client.
// Includes provider settings, resilience parameters, observability options,
// and feature flags for production-ready LLM operations.
type Config struct {
	// HTTP client configuration
	HTTPTimeout time.Duration `json:"http_timeout"`
	HTTPClient  *http.Client  `json:"-"`

	// Provider configurations
	Providers map[string]ProviderConfig `json:"providers"`

	// Artifact store for content retrieval
	ArtifactStore ArtifactStore `json:"-"`

	// Retry configuration
	Retry RetryConfig `json:"retry"`

	// Circuit breaker configuration
	CircuitBreaker CircuitBreakerConfig `json:"circuit_breaker"`

	// Rate limiting configuration
	RateLimit RateLimitConfig `json:"rate_limit"`

	// Cache configuration
	Cache CacheConfig `json:"cache"`

	// Pricing configuration
	Pricing PricingConfig `json:"pricing"`

	// Observability configuration
	Observability ObservabilityConfig `json:"observability"`

	// Feature flags
	Features FeatureFlags `json:"features"`
}

// ProviderConfig holds provider-specific configuration and authentication.
// Includes API endpoints, credentials, timeouts, and custom headers
// for each supported LLM provider.
type ProviderConfig struct {
	Endpoint   string            `json:"endpoint"`
	APIKey     string            `json:"-"` // Sensitive, not serialized
	APIKeyEnv  string            `json:"api_key_env"`
	MaxRetries int               `json:"max_retries"`
	Timeout    time.Duration     `json:"timeout"`
	Headers    map[string]string `json:"headers"`
}

// RetryConfig controls exponential backoff and retry behavior.
// Defines maximum attempts, time limits, backoff intervals, and jitter
// for resilient handling of transient failures.
type RetryConfig struct {
	MaxAttempts     int           `json:"max_attempts"`
	MaxElapsedTime  time.Duration `json:"max_elapsed_time"`
	InitialInterval time.Duration `json:"initial_interval"`
	MaxInterval     time.Duration `json:"max_interval"`
	Multiplier      float64       `json:"multiplier"`
	UseJitter       bool          `json:"use_jitter"`
}

// CircuitBreakerConfig controls circuit breaker state transitions.
// Defines failure thresholds, success requirements, and timing parameters
// for automatic provider failure detection and recovery.
type CircuitBreakerConfig struct {
	FailureThreshold int           `json:"failure_threshold"`
	SuccessThreshold int           `json:"success_threshold"`
	OpenTimeout      time.Duration `json:"open_timeout"`
	HalfOpenProbes   int           `json:"half_open_probes"`
}

// RateLimitConfig controls local and global rate limiting strategies.
// Combines in-memory token buckets with Redis-based GCRA algorithm
// for distributed rate limiting with graceful degradation.
type RateLimitConfig struct {
	// Local token bucket configuration
	Local LocalRateLimitConfig `json:"local"`

	// Global Redis-based configuration
	Global GlobalRateLimitConfig `json:"global"`
}

// LocalRateLimitConfig for in-memory token buckets.
type LocalRateLimitConfig struct {
	TokensPerSecond float64 `json:"tokens_per_second"`
	BurstSize       int     `json:"burst_size"`
	Enabled         bool    `json:"enabled"`
}

// GlobalRateLimitConfig for Redis-based GCRA.
type GlobalRateLimitConfig struct {
	Enabled        bool          `json:"enabled"`
	RedisAddr      string        `json:"redis_addr"`
	RedisPassword  string        `json:"-"` // Sensitive
	RedisDB        int           `json:"redis_db"`
	DegradedMode   bool          `json:"degraded_mode"` // Runtime state
	ConnectTimeout time.Duration `json:"connect_timeout"`
}

// CacheConfig controls Redis-based idempotency caching behavior.
// Manages cache TTL, success-only storage policy, and connection parameters
// for deduplicating equivalent requests across service instances.
type CacheConfig struct {
	Enabled       bool          `json:"enabled"`
	TTL           time.Duration `json:"ttl"`
	SuccessOnly   bool          `json:"success_only"`
	RedisAddr     string        `json:"redis_addr"`
	RedisPassword string        `json:"-"` // Sensitive
	RedisDB       int           `json:"redis_db"`
}

// PricingConfig controls cost calculation and budget enforcement.
// Manages pricing data refresh, fail-closed behavior, and budget limits
// to prevent unbounded cost exposure in production environments.
type PricingConfig struct {
	Enabled      bool          `json:"enabled"`
	TTL          time.Duration `json:"ttl"`
	FailClosed   bool          `json:"fail_closed"`
	RefreshURL   string        `json:"refresh_url"`
	RefreshToken string        `json:"-"` // Sensitive
}

// ObservabilityConfig controls comprehensive observability features.
// Manages Prometheus metrics, structured logging, request tracing,
// and PII redaction for production monitoring and debugging.
type ObservabilityConfig struct {
	MetricsEnabled bool   `json:"metrics_enabled"`
	MetricsPort    int    `json:"metrics_port"`
	LogLevel       string `json:"log_level"`
	LogFormat      string `json:"log_format"`
	RedactPrompts  bool   `json:"redact_prompts"`
}

// FeatureFlags control optional features and experimental behaviors.
// Enables/disables advanced features like global rate limiting,
// circuit breaker optimizations, and JSON repair functionality.
type FeatureFlags struct {
	DisableGlobalRL     bool `json:"disable_global_rl"`
	DisableCBProbeGuard bool `json:"disable_cb_probe_guard"`
	DisableJSONRepair   bool `json:"disable_json_repair"`
}

// DefaultConfig returns production-ready configuration with sensible defaults.
// Provides balanced settings for resilience, performance, and cost control
// suitable for production workloads without additional configuration.
func DefaultConfig() *Config {
	return &Config{
		HTTPTimeout:   DefaultHTTPTimeoutSeconds * time.Second,
		ArtifactStore: NewInMemoryArtifactStore(),
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
		},
		RateLimit: RateLimitConfig{
			Local: LocalRateLimitConfig{
				TokensPerSecond: DefaultTokensPerSecond,
				BurstSize:       DefaultBurstSize,
				Enabled:         true,
			},
			Global: GlobalRateLimitConfig{
				Enabled:        true,
				ConnectTimeout: DefaultConnectTimeout,
			},
		},
		Cache: CacheConfig{
			Enabled:     true,
			TTL:         DefaultCacheTTL,
			SuccessOnly: true,
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
	}
}
