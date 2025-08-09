package llm

// Middleware factory functions for LLM request processing pipeline.
// This file provides middleware stubs and implemented factories to support
// caching, rate limiting, circuit breaking, retry logic, pricing, and observability.

// NewCacheMiddleware creates a response caching middleware for LLM operations.
// Reduces API costs and latency by caching successful responses with
// idempotency key-based lookup. Full implementation in cache.go.
func NewCacheMiddleware(cfg CacheConfig) (Middleware, error) {
	return NewCacheMiddlewareWithRedis(cfg, nil)
}

// NewRateLimitMiddleware creates a rate limiting middleware for API protection.
// Enforces request rate limits per provider/model to prevent quota exhaustion
// and API abuse. Full implementation in ratelimit.go.
func NewRateLimitMiddleware(cfg RateLimitConfig) (Middleware, error) {
	return NewRateLimitMiddlewareWithRedis(cfg, nil)
}

// NewCircuitBreakerMiddleware creates a circuit breaker for provider protection.
// Implements fail-fast behavior during provider outages to prevent cascading
// failures and reduce unnecessary API calls. Full implementation in circuit_breaker.go.
func NewCircuitBreakerMiddleware(cfg CircuitBreakerConfig) Middleware {
	return NewCircuitBreakerMiddlewareWithRedis(cfg, nil)
}

// NewRetryMiddleware creates a retry middleware for transient failure handling.
// Implements intelligent retry logic with exponential backoff for rate limits,
// timeouts, and provider errors. Full implementation in retry.go.
func NewRetryMiddleware(cfg RetryConfig) Middleware {
	return NewRetryMiddlewareWithConfig(cfg)
}

// NewPricingMiddleware creates cost tracking middleware with fail-closed pricing.
// Attaches cost estimates to successful LLM responses using configured
// pricing registry for budget monitoring and cost optimization.
func NewPricingMiddleware(cfg PricingConfig) (Middleware, error) {
	// Create pricing registry with fail-closed behavior.
	registry := NewInMemoryPricingRegistry(cfg.FailClosed)

	return NewPricingMiddlewareWithRegistry(registry), nil
}

// NewObservabilityMiddleware creates structured logging middleware for LLM requests.
// Provides request/response logging, error tracking, and metrics collection
// with configurable detail levels for production observability.
func NewObservabilityMiddleware(config ObservabilityConfig) Middleware {
	// Use default slog logger and no-op metrics.
	// In production, metrics could be replaced with Prometheus, DataDog, etc.
	return NewLoggingMiddleware(config, nil, nil)
}
