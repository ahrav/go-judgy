package transport

import (
	"github.com/ahrav/go-judgy/internal/llm/configuration"
)

// Middleware factory functions for LLM request processing pipeline.
// These functions will be updated to use the appropriate packages once
// the resilience and business packages are created.

// NewCacheMiddleware creates a response caching middleware for LLM operations.
// Reduces API costs and latency by caching successful responses with
// idempotency key-based lookup. Implementation will be in resilience package.
func NewCacheMiddleware(cfg configuration.CacheConfig) (Middleware, error) {
	// TODO: Update to use resilience.NewCacheMiddlewareWithRedis once created
	panic("NewCacheMiddleware not yet implemented - will be updated in resilience package")
}

// NewRateLimitMiddleware creates a rate limiting middleware for API protection.
// Enforces request rate limits per provider/model to prevent quota exhaustion
// and API abuse. Implementation will be in resilience package.
func NewRateLimitMiddleware(cfg *configuration.RateLimitConfig) (Middleware, error) {
	// TODO: Update to use resilience.NewRateLimitMiddlewareWithRedis once created
	panic("NewRateLimitMiddleware not yet implemented - will be updated in resilience package")
}

// NewCircuitBreakerMiddleware creates a circuit breaker for provider protection.
// Implements fail-fast behavior during provider outages to prevent cascading
// failures and reduce unnecessary API calls. Implementation will be in resilience package.
func NewCircuitBreakerMiddleware(cfg configuration.CircuitBreakerConfig) Middleware {
	// TODO: Update to use resilience.NewCircuitBreakerMiddlewareWithRedis once created
	panic("NewCircuitBreakerMiddleware not yet implemented - will be updated in resilience package")
}

// NewRetryMiddleware creates a retry middleware for transient failure handling.
// Implements intelligent retry logic with exponential backoff for rate limits,
// timeouts, and provider errors. Implementation will be in resilience package.
func NewRetryMiddleware(cfg configuration.RetryConfig) Middleware {
	// TODO: Update to use resilience.NewRetryMiddlewareWithConfig once created
	panic("NewRetryMiddleware not yet implemented - will be updated in resilience package")
}

// NewPricingMiddleware creates cost tracking middleware with fail-closed pricing.
// Attaches cost estimates to successful LLM responses using configured
// pricing registry for budget monitoring and cost optimization.
func NewPricingMiddleware(cfg configuration.PricingConfig) (Middleware, error) {
	// TODO: Update to use business.NewPricingMiddlewareWithRegistry once created
	panic("NewPricingMiddleware not yet implemented - will be updated in business package")
}

// NewObservabilityMiddleware creates structured logging middleware for LLM requests.
// Provides request/response logging, error tracking, and metrics collection
// with configurable detail levels for production observability.
func NewObservabilityMiddleware(cfg configuration.ObservabilityConfig) Middleware {
	// TODO: Update to use resilience.NewLoggingMiddleware once created
	panic("NewObservabilityMiddleware not yet implemented - will be updated in resilience package")
}
