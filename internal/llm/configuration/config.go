package configuration

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/ahrav/go-judgy/internal/domain"
)

// ArtifactStore provides artifact storage interface for configuration.
// This interface will be implemented by business package artifacts.
type ArtifactStore interface {
	Get(ctx context.Context, ref domain.ArtifactRef) (string, error)
	Put(ctx context.Context, content string) (domain.ArtifactRef, error)
}

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

// RetryConfig controls retry behavior for failed LLM operations.
// Implements exponential backoff with jitter for optimal retry timing
// while providing budget controls to prevent unbounded cost exposure.
type RetryConfig struct {
	MaxAttempts     int           `json:"max_attempts"`     // Maximum retry attempts (0 = single attempt)
	MaxElapsedTime  time.Duration `json:"max_elapsed_time"` // Total time budget for all attempts
	InitialInterval time.Duration `json:"initial_interval"` // Starting backoff duration
	MaxInterval     time.Duration `json:"max_interval"`     // Maximum backoff duration
	Multiplier      float64       `json:"multiplier"`       // Exponential backoff multiplier
	UseJitter       bool          `json:"use_jitter"`       // Enable full jitter randomization
	MaxCostCents    int64         `json:"max_cost_cents"`   // Per-request cost limit in cents
	MaxTokens       int64         `json:"max_tokens"`       // Per-request token limit
	EnableBudget    bool          `json:"enable_budget"`    // Activate budget enforcement
}

// CircuitBreakerConfig controls circuit breaker behavior for provider protection.
// Implements fail-fast patterns to prevent cascading failures during provider
// outages with configurable thresholds and recovery strategies.
type CircuitBreakerConfig struct {
	FailureThreshold   int           `json:"failure_threshold"`
	SuccessThreshold   int           `json:"success_threshold"`
	OpenTimeout        time.Duration `json:"open_timeout"`
	HalfOpenProbes     int           `json:"half_open_probes"`
	ProbeTimeout       time.Duration `json:"probe_timeout"`       // Timeout for half-open probe Redis guard
	MaxBreakers        int           `json:"max_breakers"`        // Maximum number of circuit breakers to prevent unbounded growth
	AdaptiveThresholds bool          `json:"adaptive_thresholds"` // Enable adaptive threshold adjustment based on error rates
}

// RateLimitConfig controls local and global rate limiting strategies.
// Combines in-memory token buckets with Redis-based fixed window algorithm
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

// GlobalRateLimitConfig for Redis-based fixed window rate limiting.
type GlobalRateLimitConfig struct {
	Enabled           bool          `json:"enabled"`
	RequestsPerSecond int           `json:"requests_per_second"` // Global rate limit
	RedisAddr         string        `json:"redis_addr"`
	RedisPassword     string        `json:"-"` // Sensitive
	RedisDB           int           `json:"redis_db"`
	DegradedMode      atomic.Bool   `json:"-"` // Runtime state - not serialized, thread-safe
	ConnectTimeout    time.Duration `json:"connect_timeout"`
}

// CacheConfig controls Redis-based response caching for cost optimization.
// Manages cache TTL, staleness protection, and connection parameters
// for consistent cross-instance response caching with graceful degradation.
type CacheConfig struct {
	Enabled       bool          `json:"enabled"`
	TTL           time.Duration `json:"ttl"`
	MaxAge        time.Duration `json:"max_age"` // Maximum age for staleness protection.
	RedisAddr     string        `json:"redis_addr"`
	RedisPassword string        `json:"-"` // Sensitive field excluded from JSON.
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

// RepairConfig controls JSON repair behavior for malformed LLM responses.
// Enables progressive repair strategies including extraction, clamping,
// and default value filling to maximize response compatibility.
type RepairConfig struct {
	MaxAttempts      int  // Maximum repair attempts.
	EnableExtraction bool // Enable extraction from markdown.
	EnableClamping   bool // Enable value clamping.
	EnableDefaults   bool // Enable default value filling.
}
