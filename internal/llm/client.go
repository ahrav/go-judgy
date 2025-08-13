// Package llm provides a unified, resilient HTTP client for Large Language Model providers.
// It implements comprehensive production patterns including idempotency, caching, rate limiting,
// circuit breaking, retry logic, and cost tracking for OpenAI, Anthropic, and Google providers.
//
// Architecture:
//   - Provider-agnostic interface with adapter pattern for each provider
//   - Middleware chain for composable resilience and observability
//   - Request/response only (no streaming in this implementation)
//   - Success-only caching with idempotency support
//   - Fail-closed pricing to prevent unbounded costs
//   - Graceful degradation when Redis is unavailable
package llm

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/business"
	"github.com/ahrav/go-judgy/internal/llm/cache"
	"github.com/ahrav/go-judgy/internal/llm/circuitbreaker"
	"github.com/ahrav/go-judgy/internal/llm/configuration"
	"github.com/ahrav/go-judgy/internal/llm/providers"
	"github.com/ahrav/go-judgy/internal/llm/ratelimit"
	"github.com/ahrav/go-judgy/internal/llm/resilience"
	"github.com/ahrav/go-judgy/internal/llm/retry"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// Client provides high-level LLM operations with comprehensive resilience patterns.
// Maps domain types to provider-specific requests while handling caching, circuit breaking,
// rate limiting, retry logic, cost tracking, and observability for production reliability.
type Client interface {
	// Generate produces answers for questions using configured LLM providers.
	// Processes requests through complete middleware pipeline with idempotency,
	// caching, rate limiting, circuit breaking, retry logic, and cost tracking.
	Generate(ctx context.Context, in domain.GenerateAnswersInput) (*domain.GenerateAnswersOutput, error)

	// Score evaluates answers using judge models with JSON validation and repair.
	// Applies same resilience patterns as Generate with additional structured response
	// validation, automatic JSON repair, and comprehensive error classification.
	// Executes injected validator from ScoreAnswersInput if provided.
	Score(ctx context.Context, in domain.ScoreAnswersInput) (*domain.ScoreAnswersOutput, error)
}

// client implements the Client interface with full middleware pipeline.
type client struct {
	config        *configuration.Config
	router        providers.Router
	handler       transport.Handler
	artifactStore business.ArtifactStore
}

// NewClient creates a production-ready LLM client with comprehensive resilience.
// Builds complete middleware pipeline including caching, circuit breaking,
// rate limiting, retry logic, pricing, and observability for robust operation.
func NewClient(cfg *configuration.Config) (Client, error) {
	if cfg == nil {
		cfg = configuration.DefaultConfig()
	}

	var businessArtifactStore business.ArtifactStore
	if cfg.ArtifactStore == nil {
		businessArtifactStore = business.NewInMemoryArtifactStore()
	} else {
		businessArtifactStore = newConfigArtifactStoreAdapter(cfg.ArtifactStore)
	}

	router, err := providers.NewRouter(cfg.Providers)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize router: %w", err)
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpTransport := &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          configuration.DefaultMaxIdleConns,
			IdleConnTimeout:       configuration.DefaultIdleTimeoutSeconds * time.Second,
			TLSHandshakeTimeout:   configuration.DefaultTLSTimeoutSeconds * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}
		httpClient = &http.Client{
			Transport: httpTransport,
			Timeout:   cfg.HTTPTimeout,
		}
	}

	coreHandler := transport.NewHTTPHandler(httpClient, newRouterAdapter(router), newBusinessValidatorAdapter())

	// Build attempt-level middleware stack (applied per retry attempt)
	var attemptMiddlewares []transport.Middleware

	if cfg.RateLimit.Local.Enabled {
		rlMiddleware, err := ratelimit.NewRateLimitMiddlewareWithRedis(&cfg.RateLimit, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize rate limiter: %w", err)
		}
		attemptMiddlewares = append(attemptMiddlewares, rlMiddleware)
	}

	if cfg.Pricing.Enabled {
		pricingMiddleware := business.NewPricingMiddlewareWithRegistry(business.NewInMemoryPricingRegistry(true))
		attemptMiddlewares = append(attemptMiddlewares, pricingMiddleware)
	}

	attemptHandler := transport.Chain(coreHandler, attemptMiddlewares...)

	// Wrap attempt handler with retry logic
	retryMiddleware, err := retry.NewRetryMiddlewareWithConfig(cfg.Retry)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize retry middleware: %w", err)
	}
	retryHandler := retryMiddleware(attemptHandler)

	// Build call-level middleware stack (applied per logical call)
	var callMiddlewares []transport.Middleware

	if cfg.Observability.MetricsEnabled {
		obsMiddleware, err := resilience.NewObservabilityMiddleware()
		if err != nil {
			return nil, fmt.Errorf("failed to initialize observability middleware: %w", err)
		}
		callMiddlewares = append(callMiddlewares, obsMiddleware)
	}

	if cfg.Cache.Enabled {
		cacheMiddleware, err := cache.NewCacheMiddlewareWithRedis(context.Background(), cfg.Cache, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize cache: %w", err)
		}
		callMiddlewares = append(callMiddlewares, cacheMiddleware)
	}

	resilienceConfig := circuitbreaker.Config{
		FailureThreshold:   cfg.CircuitBreaker.FailureThreshold,
		SuccessThreshold:   cfg.CircuitBreaker.SuccessThreshold,
		OpenTimeout:        cfg.CircuitBreaker.OpenTimeout,
		HalfOpenProbes:     cfg.CircuitBreaker.HalfOpenProbes,
		ProbeTimeout:       cfg.CircuitBreaker.ProbeTimeout,
		MaxBreakers:        cfg.CircuitBreaker.MaxBreakers,
		AdaptiveThresholds: cfg.CircuitBreaker.AdaptiveThresholds,
	}
	cbMiddleware, err := circuitbreaker.NewCircuitBreakerMiddlewareWithRedis(resilienceConfig, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize circuit breaker: %w", err)
	}
	callMiddlewares = append(callMiddlewares, cbMiddleware)

	handler := transport.Chain(retryHandler, callMiddlewares...)

	return &client{
		config:        cfg,
		router:        router,
		handler:       handler,
		artifactStore: businessArtifactStore,
	}, nil
}
