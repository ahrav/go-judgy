package llm

import (
	"context"
	"errors"
	"sync"

	"github.com/ahrav/go-judgy/internal/domain"
)

// Artifact store errors.
var (
	ErrArtifactKeyEmpty = errors.New("artifact key cannot be empty")
	ErrArtifactNotFound = errors.New("artifact not found")
)

// ArtifactStore provides external content storage and retrieval for LLM operations.
// Enables separation of large content from metadata while supporting scoring workflows
// that require access to previously generated answer content.
type ArtifactStore interface {
	// Get retrieves stored content using artifact reference key.
	// Returns content string or error for missing/invalid references.
	Get(ctx context.Context, ref domain.ArtifactRef) (string, error)

	// Put stores content and returns artifact reference for future retrieval.
	// Creates new artifact entries for answer generation and content archival.
	Put(ctx context.Context, content string, kind domain.ArtifactKind, key string) (domain.ArtifactRef, error)

	// Exists checks artifact presence without content retrieval.
	// Enables efficient existence validation for caching and workflow decisions.
	Exists(ctx context.Context, ref domain.ArtifactRef) (bool, error)
}

// InMemoryArtifactStore provides in-memory content storage for development.
// Suitable for testing and development environments. Production deployments
// should use distributed blob storage like S3, GCS, or Azure Storage.
type InMemoryArtifactStore struct {
	mu      sync.RWMutex
	storage map[string]string
}

// NewInMemoryArtifactStore creates an in-memory artifact storage instance.
// Initializes empty storage map for immediate use in development and
// testing environments requiring artifact storage capabilities.
func NewInMemoryArtifactStore() *InMemoryArtifactStore {
	return &InMemoryArtifactStore{
		storage: make(map[string]string),
	}
}

// Get retrieves stored content from in-memory storage.
// Validates reference key and returns content or appropriate error
// for missing artifacts or invalid references.
func (s *InMemoryArtifactStore) Get(ctx context.Context, ref domain.ArtifactRef) (string, error) {
	if ref.Key == "" {
		return "", ErrArtifactKeyEmpty
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	content, exists := s.storage[ref.Key]
	if !exists {
		return "", ErrArtifactNotFound
	}

	return content, nil
}

// Put stores content in memory and creates artifact reference.
// Calculates content size and creates reference with metadata
// for consistent artifact management and retrieval.
func (s *InMemoryArtifactStore) Put(ctx context.Context, content string, kind domain.ArtifactKind, key string) (domain.ArtifactRef, error) {
	if key == "" {
		return domain.ArtifactRef{}, ErrArtifactKeyEmpty
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.storage[key] = content

	ref := domain.ArtifactRef{
		Key:  key,
		Size: int64(len(content)),
		Kind: kind,
	}

	return ref, nil
}

// Exists checks artifact presence in memory storage.
// Provides efficient existence validation without content retrieval
// to support caching decisions and workflow optimization.
func (s *InMemoryArtifactStore) Exists(ctx context.Context, ref domain.ArtifactRef) (bool, error) {
	if ref.Key == "" {
		return false, ErrArtifactKeyEmpty
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	_, exists := s.storage[ref.Key]
	return exists, nil
}
