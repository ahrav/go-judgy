package business_test

import (
	"context"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/business"
)

// MockArtifactStore provides predictable content for testing scenarios.
// Returns predefined content for specific keys to enable deterministic
// testing of LLM workflows that depend on artifact storage.
type MockArtifactStore struct {
	content map[string]string
}

// NewMockArtifactStore creates a test artifact store with predefined content.
// Enables controlled testing scenarios where specific content must be
// returned for particular artifact keys in scoring operations.
func NewMockArtifactStore(content map[string]string) *MockArtifactStore {
	if content == nil {
		content = make(map[string]string)
	}
	return &MockArtifactStore{content: content}
}

// Get retrieves predefined content from mock storage.
// Returns test content for configured keys or appropriate errors
// to simulate various artifact retrieval scenarios.
func (m *MockArtifactStore) Get(_ context.Context, ref domain.ArtifactRef) (string, error) {
	if ref.Key == "" {
		return "", business.ErrArtifactKeyEmpty
	}

	content, exists := m.content[ref.Key]
	if !exists {
		return "", business.ErrArtifactNotFound
	}

	return content, nil
}

// Put stores content in mock storage for testing workflows.
// Updates predefined content map and creates artifact reference
// to support testing of content generation and storage flows.
func (m *MockArtifactStore) Put(_ context.Context, content string, kind domain.ArtifactKind, key string) (domain.ArtifactRef, error) {
	if key == "" {
		return domain.ArtifactRef{}, business.ErrArtifactKeyEmpty
	}

	m.content[key] = content

	ref := domain.ArtifactRef{
		Key:  key,
		Size: int64(len(content)),
		Kind: kind,
	}

	return ref, nil
}

// Exists checks predefined content availability in mock storage.
// Enables testing of existence validation logic without requiring
// actual storage backend availability or network connectivity.
func (m *MockArtifactStore) Exists(_ context.Context, ref domain.ArtifactRef) (bool, error) {
	if ref.Key == "" {
		return false, business.ErrArtifactKeyEmpty
	}

	_, exists := m.content[ref.Key]
	return exists, nil
}
