package llm

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/business"
	"github.com/ahrav/go-judgy/internal/llm/configuration"
	"github.com/ahrav/go-judgy/internal/llm/providers"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// artifactStoreAdapter adapts business.ArtifactStore to transport.ArtifactStore.
// It provides artifact storage capabilities for the transport layer while
// generating timestamp-based artifact keys for unique identification.
// Ensures consistent artifact handling across different storage backends.
type artifactStoreAdapter struct{ store business.ArtifactStore }

func newArtifactStoreAdapter(store business.ArtifactStore) transport.ArtifactStore {
	return &artifactStoreAdapter{store: store}
}

// Get retrieves content from the artifact store by reference.
// Returns the stored content string or an error if retrieval fails.
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
// Provides a clean interface for business layer artifact operations.
type configArtifactStoreAdapter struct{ store configuration.ArtifactStore }

func newConfigArtifactStoreAdapter(store configuration.ArtifactStore) business.ArtifactStore {
	return &configArtifactStoreAdapter{store: store}
}

// Get retrieves content from the config artifact store by reference.
// Delegates to the underlying configuration store implementation.
func (a *configArtifactStoreAdapter) Get(ctx context.Context, ref domain.ArtifactRef) (string, error) {
	return a.store.Get(ctx, ref)
}

// Put stores content in the config artifact store.
// Sets the artifact kind and key if provided, maintaining metadata consistency.
func (a *configArtifactStoreAdapter) Put(
	ctx context.Context,
	content string,
	kind domain.ArtifactKind,
	key string,
) (domain.ArtifactRef, error) {
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
		return false, err // Return the error instead of assuming not found
	}
	return true, nil
}

// Delete removes an artifact from the store using type assertion fallback.
// It first attempts to use the underlying store's Delete method if available,
// otherwise returns an error indicating deletion is not supported.
func (a *configArtifactStoreAdapter) Delete(ctx context.Context, ref domain.ArtifactRef) error {
	if deleteStore, ok := a.store.(interface {
		Delete(context.Context, domain.ArtifactRef) error
	}); ok {
		return deleteStore.Delete(ctx, ref)
	}
	return fmt.Errorf("delete operation not supported by underlying store")
}

// routerAdapter adapts providers.Router to transport.Router.
// It enables the transport layer to access provider routing capabilities
// while maintaining clean separation between routing and transport concerns.
// Handles provider selection and adapter wrapping for HTTP operations.
type routerAdapter struct{ router providers.Router }

func newRouterAdapter(router providers.Router) transport.Router {
	return &routerAdapter{router: router}
}

// Pick selects the appropriate provider adapter for the given provider and model.
// Returns a wrapped adapter that implements the transport interface contract.
func (r *routerAdapter) Pick(provider, model string) (transport.ProviderAdapter, error) {
	providerAdapter, err := r.router.Pick(provider, model)
	if err != nil {
		return nil, err
	}

	return &providerAdapterWrapper{adapter: providerAdapter}, nil
}

// businessValidatorAdapter implements transport.Validator by calling business validation functions.
// It bridges validation logic between business and transport layers for consistent
// response validation across different provider implementations.
type businessValidatorAdapter struct{}

func newBusinessValidatorAdapter() transport.Validator {
	return &businessValidatorAdapter{}
}

// ValidateProviderResponse validates provider response using business logic.
// Ensures response meets quality and consistency requirements.
func (v *businessValidatorAdapter) ValidateProviderResponse(resp *transport.Response) error {
	return business.ValidateProviderResponse(resp)
}

// ValidateGenerationResponse validates generation response using business logic.
// Checks content validity and structure for downstream processing.
func (v *businessValidatorAdapter) ValidateGenerationResponse(content string) error {
	return business.ValidateGenerationResponse(content)
}

// providerAdapterWrapper wraps providers.ProviderAdapter to implement transport.ProviderAdapter.
// It provides a bridge between provider-specific adapters and transport layer interfaces,
// delegating HTTP request building and response parsing to the underlying provider.
// Ensures consistent adapter interface across different LLM providers.
type providerAdapterWrapper struct{ adapter providers.ProviderAdapter }

// Build constructs an HTTP request from a transport request.
// Delegates to the wrapped provider adapter for request construction.
func (w *providerAdapterWrapper) Build(ctx context.Context, req *transport.Request) (*http.Request, error) {
	return w.adapter.Build(ctx, req)
}

// Parse converts an HTTP response to a transport response.
// Delegates to the wrapped provider adapter for response parsing.
func (w *providerAdapterWrapper) Parse(httpResp *http.Response) (*transport.Response, error) {
	return w.adapter.Parse(httpResp)
}

// Name returns the name of the wrapped provider adapter.
// Used for logging and debugging provider-specific operations.
func (w *providerAdapterWrapper) Name() string {
	return w.adapter.Name()
}

// businessScoreValidator adapts business validation functions to transport.ScoreValidator interface.
// It provides JSON validation and repair capabilities for LLM scoring responses,
// converting between business and transport layer score data structures.
// Enables clean separation of validation logic from transport concerns.
type businessScoreValidator struct{}

func newBusinessScoreValidator() transport.ScoreValidator {
	return &businessScoreValidator{}
}

// ValidateAndRepairScore validates and optionally repairs malformed JSON score content.
// It delegates to business layer validation logic and converts the result to transport format.
// When enableRepair is true, attempts to fix common JSON formatting issues in LLM responses.
// Returns structured score data ready for transport layer processing.
func (v *businessScoreValidator) ValidateAndRepairScore(
	content string,
	enableRepair bool,
) (*transport.ScoreData, error) {
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
