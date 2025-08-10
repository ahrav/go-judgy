// Package worker provides initialization and setup utilities for Temporal workers.
// This package contains initialization logic that should be executed during
// worker startup, keeping activity packages focused on pure activity logic.
package worker

import (
	"fmt"

	"github.com/ahrav/go-judgy/internal/llm"
	"github.com/ahrav/go-judgy/internal/llm/business"
	"github.com/ahrav/go-judgy/internal/llm/configuration"
)

// InitializeLLMClient creates an LLM client with comprehensive configuration.
// Returns the client for dependency injection rather than setting global state.
// Must be called during worker startup to establish the client with middleware
// pipeline including caching, circuit breaking, rate limiting, and observability.
func InitializeLLMClient(cfg *configuration.Config) (llm.Client, error) {
	if cfg == nil {
		cfg = configuration.DefaultConfig()
		// The ArtifactStore will be set separately since business layer needs different interface
	}

	client, err := llm.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize LLM client: %w", err)
	}

	return client, nil
}

// InitializeArtifactStore creates an artifact store instance.
// Returns an in-memory store for development/testing.
// Production deployments should provide distributed storage (S3, GCS, etc.)
func InitializeArtifactStore() business.ArtifactStore {
	return business.NewInMemoryArtifactStore()
}
