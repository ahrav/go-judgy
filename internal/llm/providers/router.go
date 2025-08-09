package providers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/ahrav/go-judgy/internal/llm/configuration"
	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// Router selects the appropriate provider adapter for request routing.
// Provides centralized adapter selection based on provider and model configuration,
// enabling dynamic provider switching and adapter lifecycle management.
type Router interface {
	// Pick selects the adapter for the specified provider and model combination.
	// Returns error if provider is unknown, unsupported, or model is unavailable.
	// for the requested provider.
	Pick(provider, model string) (ProviderAdapter, error)
}

// ProviderAdapter abstracts provider-specific HTTP communication patterns.
// Each provider (OpenAI, Anthropic, Google) implements this interface to handle
// their unique API formats, authentication schemes, and response structures.
type ProviderAdapter interface {
	// Build constructs provider-specific HTTP request from normalized LLM request.
	// Sets authentication headers, API endpoints, request bodies, and timeouts.
	// according to provider requirements and model specifications.
	Build(ctx context.Context, req *transport.Request) (*http.Request, error)

	// Parse extracts normalized data from provider's HTTP response.
	// Handles provider-specific response formats, header extraction, usage metrics,.
	// and error conditions to produce consistent Response structure.
	Parse(httpResp *http.Response) (*transport.Response, error)

	// Name returns canonical provider identifier for routing and metrics.
	// Valid values: "openai", "anthropic", "google" matching configuration keys.
	Name() string
}

// Supported LLM provider identifiers.
// These constants must match the provider names used in configuration.
const (
	ProviderOpenAI    = "openai"    // OpenAI GPT models
	ProviderAnthropic = "anthropic" // Anthropic Claude models
	ProviderGoogle    = "google"    // Google Gemini models
)

// NewRouter creates a router with configured provider adapters.
func NewRouter(configs map[string]configuration.ProviderConfig) (Router, error) {
	adapters := make(map[string]ProviderAdapter)

	for name, cfg := range configs {
		var adapter ProviderAdapter
		switch name {
		case ProviderOpenAI:
			adapter = NewOpenAIAdapter(cfg)
		case ProviderAnthropic:
			adapter = NewAnthropicAdapter(cfg)
		case ProviderGoogle:
			adapter = NewGoogleAdapter(cfg)
		default:
			return nil, fmt.Errorf("%w: %s", llmerrors.ErrUnknownProvider, name)
		}
		adapters[name] = adapter
	}

	return &router{adapters: adapters}, nil
}

// router implements Router interface with provider adapter registry.
// It maintains a map of configured provider adapters for request routing.
type router struct {
	adapters map[string]ProviderAdapter
}

// Pick selects the appropriate provider adapter for the given provider name.
// Returns an error if the provider is not configured or unknown.
func (r *router) Pick(provider, _ string) (ProviderAdapter, error) {
	adapter, ok := r.adapters[provider]
	if !ok {
		return nil, fmt.Errorf("%w: %s", llmerrors.ErrUnknownProvider, provider)
	}
	return adapter, nil
}
