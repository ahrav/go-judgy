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
	"github.com/ahrav/go-judgy/internal/llm/circuit_breaker"
	"github.com/ahrav/go-judgy/internal/llm/configuration"
	"github.com/ahrav/go-judgy/internal/llm/providers"
	"github.com/ahrav/go-judgy/internal/llm/ratelimit"
	"github.com/ahrav/go-judgy/internal/llm/resilience"
	"github.com/ahrav/go-judgy/internal/llm/retry"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// artifactStoreAdapter adapts business.ArtifactStore to transport.ArtifactStore.
// It provides artifact storage capabilities for the transport layer while
// generating timestamp-based artifact keys for unique identification.
type artifactStoreAdapter struct {
	store business.ArtifactStore
}

func newArtifactStoreAdapter(store business.ArtifactStore) transport.ArtifactStore {
	return &artifactStoreAdapter{store: store}
}

func (a *artifactStoreAdapter) Get(ctx context.Context, ref domain.ArtifactRef) (string, error) {
	return a.store.Get(ctx, ref)
}

// Put stores content in the artifact store with a timestamp-based unique key.
// The generated key follows the format "artifacts/{nanosecond-timestamp}" to ensure
// uniqueness across concurrent operations.
func (a *artifactStoreAdapter) Put(ctx context.Context, content string) (domain.ArtifactRef, error) {
	id := fmt.Sprintf("artifacts/%d", time.Now().UnixNano())
	return a.store.Put(ctx, content, domain.ArtifactAnswer, id)
}

// configArtifactStoreAdapter adapts configuration.ArtifactStore to business.ArtifactStore.
// It bridges the gap between configuration-level artifact storage and business logic
// requirements, handling artifact metadata such as kind and key assignment.
type configArtifactStoreAdapter struct {
	store configuration.ArtifactStore
}

func newConfigArtifactStoreAdapter(store configuration.ArtifactStore) business.ArtifactStore {
	return &configArtifactStoreAdapter{store: store}
}

func (a *configArtifactStoreAdapter) Get(ctx context.Context, ref domain.ArtifactRef) (string, error) {
	return a.store.Get(ctx, ref)
}

func (a *configArtifactStoreAdapter) Put(ctx context.Context, content string, kind domain.ArtifactKind, key string) (domain.ArtifactRef, error) {
	ref, err := a.store.Put(ctx, content)
	if err != nil {
		return ref, err
	}
	ref.Kind = kind
	if key != "" {
		ref.Key = key
	}
	return ref, nil
}

// Exists checks if an artifact exists in the store using type assertion fallback.
// It first attempts to use the underlying store's Exists method if available,
// otherwise falls back to a Get operation to determine existence.
func (a *configArtifactStoreAdapter) Exists(ctx context.Context, ref domain.ArtifactRef) (bool, error) {
	if existsStore, ok := a.store.(interface {
		Exists(context.Context, domain.ArtifactRef) (bool, error)
	}); ok {
		return existsStore.Exists(ctx, ref)
	}
	_, err := a.store.Get(ctx, ref)
	if err != nil {
		return false, nil // Assume not found if Get fails.
	}
	return true, nil
}

// businessScoreValidator adapts business validation functions to transport.ScoreValidator interface.
// It provides JSON validation and repair capabilities for LLM scoring responses,
// converting between business and transport layer score data structures.
type businessScoreValidator struct{}

func newBusinessScoreValidator() transport.ScoreValidator {
	return &businessScoreValidator{}
}

// ValidateAndRepairScore validates and optionally repairs malformed JSON score content.
// It delegates to business layer validation logic and converts the result to transport format.
// When enableRepair is true, attempts to fix common JSON formatting issues in LLM responses.
func (v *businessScoreValidator) ValidateAndRepairScore(content string, enableRepair bool) (*transport.ScoreData, error) {
	scoreData, err := business.ValidateAndRepairScore(content, enableRepair)
	if err != nil {
		return nil, err
	}

	return &transport.ScoreData{
		Value:      scoreData.Value,
		Confidence: scoreData.Confidence,
		Reasoning:  scoreData.Reasoning,
		Dimensions: scoreData.Dimensions,
	}, nil
}

// routerAdapter adapts providers.Router to transport.Router.
// It enables the transport layer to access provider routing capabilities
// while maintaining clean separation between routing and transport concerns.
type routerAdapter struct {
	router providers.Router
}

func newRouterAdapter(router providers.Router) transport.Router {
	return &routerAdapter{router: router}
}

func (r *routerAdapter) Pick(provider, model string) (transport.ProviderAdapter, error) {
	providerAdapter, err := r.router.Pick(provider, model)
	if err != nil {
		return nil, err
	}

	return &providerAdapterWrapper{adapter: providerAdapter}, nil
}

// providerAdapterWrapper wraps providers.ProviderAdapter to implement transport.ProviderAdapter.
// It provides a bridge between provider-specific adapters and transport layer interfaces,
// delegating HTTP request building and response parsing to the underlying provider.
type providerAdapterWrapper struct {
	adapter providers.ProviderAdapter
}

func (w *providerAdapterWrapper) Build(ctx context.Context, req *transport.Request) (*http.Request, error) {
	return w.adapter.Build(ctx, req)
}

func (w *providerAdapterWrapper) Parse(httpResp *http.Response) (*transport.Response, error) {
	return w.adapter.Parse(httpResp)
}

func (w *providerAdapterWrapper) Name() string {
	return w.adapter.Name()
}

// Token and scoring constants.
const (
	DefaultMaxTokens          = 1000
	DefaultScoringTemperature = 0.1
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

	coreHandler := transport.NewHTTPHandler(httpClient, newRouterAdapter(router), nil)

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
		cacheMiddleware, err := cache.NewCacheMiddlewareWithRedis(cfg.Cache, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize cache: %w", err)
		}
		callMiddlewares = append(callMiddlewares, cacheMiddleware)
	}

	resilienceConfig := circuit_breaker.CircuitBreakerConfig{
		FailureThreshold:   cfg.CircuitBreaker.FailureThreshold,
		SuccessThreshold:   cfg.CircuitBreaker.SuccessThreshold,
		OpenTimeout:        cfg.CircuitBreaker.OpenTimeout,
		HalfOpenProbes:     cfg.CircuitBreaker.HalfOpenProbes,
		ProbeTimeout:       cfg.CircuitBreaker.ProbeTimeout,
		MaxBreakers:        cfg.CircuitBreaker.MaxBreakers,
		AdaptiveThresholds: cfg.CircuitBreaker.AdaptiveThresholds,
	}
	cbMiddleware, err := circuit_breaker.NewCircuitBreakerMiddlewareWithRedis(resilienceConfig, nil)
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

// Generate implements Client.Generate with comprehensive resilience patterns.
// Processes requests through the complete middleware pipeline including caching,
// circuit breaking, rate limiting, and retry logic for robust answer generation.
func (c *client) Generate(
	ctx context.Context, in domain.GenerateAnswersInput,
) (*domain.GenerateAnswersOutput, error) {
	output := &domain.GenerateAnswersOutput{
		Answers: make([]domain.Answer, 0, in.NumAnswers),
	}

	for i := 0; i < in.NumAnswers; i++ {
		req := &transport.Request{
			Operation:     transport.OpGeneration,
			Provider:      in.Config.Provider,
			Model:         in.Config.Model,
			TenantID:      transport.ExtractTenantID(ctx),
			Question:      in.Question,
			MaxTokens:     in.Config.MaxAnswerTokens,
			Temperature:   in.Config.Temperature,
			Timeout:       time.Duration(in.Config.Timeout) * time.Second,
			TraceID:       transport.ExtractTraceID(ctx),
			ArtifactStore: newArtifactStoreAdapter(c.artifactStore),
		}

		key, err := transport.GenerateIdemKey(req)
		if err != nil {
			output.Error = fmt.Sprintf("failed to generate idempotency key for answer %d: %v", i+1, err)
			continue
		}
		req.IdempotencyKey = key.String()

		resp, err := c.handler.Handle(ctx, req)
		if err != nil {
			output.Error = fmt.Sprintf("failed to generate answer %d: %v", i+1, err)
			continue
		}

		answer := transport.ResponseToAnswer(resp, req)
		output.Answers = append(output.Answers, *answer)

		output.TokensUsed += resp.Usage.TotalTokens
		output.CallsMade++
	}

	if len(output.Answers) == 0 && output.Error == "" {
		output.Error = "no answers generated"
	}

	return output, nil
}

// Score implements Client.Score with JSON validation and repair capabilities.
// Processes scoring requests through the middleware pipeline with automatic
// JSON validation, repair, and structured error handling for judge responses.
func (c *client) Score(ctx context.Context, in domain.ScoreAnswersInput) (*domain.ScoreAnswersOutput, error) {
	output := &domain.ScoreAnswersOutput{
		Scores: make([]domain.Score, 0, len(in.Answers)),
	}

	for _, answer := range in.Answers {
		req := &transport.Request{
			Operation:     transport.OpScoring,
			Provider:      in.Config.Provider,
			Model:         in.Config.Model,
			TenantID:      transport.ExtractTenantID(ctx),
			Question:      in.Question,
			Answers:       []domain.Answer{answer},
			MaxTokens:     DefaultMaxTokens,          // Scoring typically needs less tokens
			Temperature:   DefaultScoringTemperature, // Low temperature for consistent scoring
			Timeout:       time.Duration(in.Config.Timeout) * time.Second,
			TraceID:       transport.ExtractTraceID(ctx),
			ArtifactStore: newArtifactStoreAdapter(c.artifactStore),
		}

		key, err := transport.GenerateIdemKey(req)
		if err != nil {
			score := transport.CreateInvalidScore(answer.ID, fmt.Errorf("failed to generate idempotency key: %w", err))
			output.Scores = append(output.Scores, *score)
			continue
		}
		req.IdempotencyKey = key.String()

		resp, err := c.handler.Handle(ctx, req)
		if err != nil {
			score := transport.CreateInvalidScore(answer.ID, err)
			output.Scores = append(output.Scores, *score)
			continue
		}

		validator := newBusinessScoreValidator()
		score, err := transport.ResponseToScore(resp, answer.ID, req, validator, c.config.Features.DisableJSONRepair)
		if err != nil {
			score = transport.CreateInvalidScore(answer.ID, err)
		}

		output.Scores = append(output.Scores, *score)

		output.TokensUsed += resp.Usage.TotalTokens
		output.CallsMade++
	}

	return output, nil
}
