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
)

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
	config        *Config
	router        Router
	handler       Handler
	artifactStore ArtifactStore
}

// NewClient creates a production-ready LLM client with comprehensive resilience.
// Builds complete middleware pipeline including caching, circuit breaking,
// rate limiting, retry logic, pricing, and observability for robust operation.
func NewClient(cfg *Config) (Client, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	artifactStore := cfg.ArtifactStore
	if artifactStore == nil {
		artifactStore = NewInMemoryArtifactStore()
	}

	router, err := NewRouter(cfg.Providers)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize router: %w", err)
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		transport := &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          DefaultMaxIdleConns,
			IdleConnTimeout:       DefaultIdleTimeoutSeconds * time.Second,
			TLSHandshakeTimeout:   DefaultTLSTimeoutSeconds * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}
		httpClient = &http.Client{
			Transport: transport,
			Timeout:   cfg.HTTPTimeout,
		}
	}

	coreHandler := &httpHandler{
		client: httpClient,
		router: router,
	}

	var middlewares []Middleware

	if cfg.Cache.Enabled {
		cacheMiddleware, err := NewCacheMiddleware(cfg.Cache)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize cache: %w", err)
		}
		middlewares = append(middlewares, cacheMiddleware)
	}

	cbMiddleware := NewCircuitBreakerMiddleware(cfg.CircuitBreaker)
	middlewares = append(middlewares, cbMiddleware)

	if cfg.RateLimit.Local.Enabled {
		rlMiddleware, err := NewRateLimitMiddleware(cfg.RateLimit)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize rate limiter: %w", err)
		}
		middlewares = append(middlewares, rlMiddleware)
	}

	retryMiddleware := NewRetryMiddleware(cfg.Retry)
	middlewares = append(middlewares, retryMiddleware)

	if cfg.Pricing.Enabled {
		pricingMiddleware, err := NewPricingMiddleware(cfg.Pricing)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize pricing: %w", err)
		}
		middlewares = append(middlewares, pricingMiddleware)
	}

	if cfg.Observability.MetricsEnabled {
		obsMiddleware := NewObservabilityMiddleware(cfg.Observability)
		middlewares = append(middlewares, obsMiddleware)
	}

	handler := Chain(coreHandler, middlewares...)

	return &client{
		config:        cfg,
		router:        router,
		handler:       handler,
		artifactStore: artifactStore,
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
		req := &Request{
			Operation:     OpGeneration,
			Provider:      in.Config.Provider,
			Model:         in.Config.Model,
			TenantID:      extractTenantID(ctx),
			Question:      in.Question,
			MaxTokens:     in.Config.MaxAnswerTokens,
			Temperature:   in.Config.Temperature,
			Timeout:       time.Duration(in.Config.Timeout) * time.Second,
			TraceID:       extractTraceID(ctx),
			ArtifactStore: c.artifactStore,
		}

		key, err := GenerateIdemKey(req)
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

		answer := responseToAnswer(resp, req)
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
		req := &Request{
			Operation:     OpScoring,
			Provider:      in.Config.Provider,
			Model:         in.Config.Model,
			TenantID:      extractTenantID(ctx),
			Question:      in.Question,
			Answers:       []domain.Answer{answer},
			MaxTokens:     DefaultMaxTokens,          // Scoring typically needs less tokens
			Temperature:   DefaultScoringTemperature, // Low temperature for consistent scoring
			Timeout:       time.Duration(in.Config.Timeout) * time.Second,
			TraceID:       extractTraceID(ctx),
			ArtifactStore: c.artifactStore,
		}

		key, err := GenerateIdemKey(req)
		if err != nil {
			score := createInvalidScore(answer.ID, fmt.Errorf("failed to generate idempotency key: %w", err))
			output.Scores = append(output.Scores, *score)
			continue
		}
		req.IdempotencyKey = key.String()

		resp, err := c.handler.Handle(ctx, req)
		if err != nil {
			score := createInvalidScore(answer.ID, err)
			output.Scores = append(output.Scores, *score)
			continue
		}

		score, err := responseToScore(resp, answer.ID, req, c.config.Features.DisableJSONRepair)
		if err != nil {
			score = createInvalidScore(answer.ID, err)
		}

		output.Scores = append(output.Scores, *score)

		output.TokensUsed += resp.Usage.TotalTokens
		output.CallsMade++
	}

	return output, nil
}
