package scoring

import (
	"context"
	"sync"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/business"
	"github.com/ahrav/go-judgy/pkg/activity"
	"github.com/ahrav/go-judgy/pkg/events"
)

// capturingEventSink implements events.EventSink for testing event emission.
// It captures all emitted events in a thread-safe slice for verification.
type capturingEventSink struct {
	mu     sync.Mutex
	events []events.Envelope
}

// newCapturingEventSink creates a new event sink that captures events for testing.
func newCapturingEventSink() *capturingEventSink {
	return &capturingEventSink{
		events: make([]events.Envelope, 0),
	}
}

// Append captures the event envelope for testing verification.
func (c *capturingEventSink) Append(ctx context.Context, envelope events.Envelope) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, envelope)
	return nil
}

// GetEvents returns all captured events in order of emission.
func (c *capturingEventSink) GetEvents() []events.Envelope {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Return a copy to avoid race conditions
	result := make([]events.Envelope, len(c.events))
	copy(result, c.events)
	return result
}

// GetEventsByType returns all captured events of the specified type.
func (c *capturingEventSink) GetEventsByType(eventType string) []events.Envelope {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result []events.Envelope
	for _, event := range c.events {
		if event.Type == eventType {
			result = append(result, event)
		}
	}
	return result
}

// GetEventCount returns the total number of captured events.
func (c *capturingEventSink) GetEventCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.events)
}

// Clear removes all captured events for test isolation.
func (c *capturingEventSink) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = c.events[:0]
}

// HasEventWithIdempotencyKey checks if any event has the specified idempotency key.
func (c *capturingEventSink) HasEventWithIdempotencyKey(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, event := range c.events {
		if event.IdempotencyKey == key {
			return true
		}
	}
	return false
}

// capturingBaseActivities wraps activity.BaseActivities with event capture.
// This enables testing of event emission without affecting the core activity logic.
type capturingBaseActivities struct {
	activity.BaseActivities
	sink *capturingEventSink
}

// newCapturingBaseActivities creates a BaseActivities wrapper with event capture.
func newCapturingBaseActivities() (*capturingBaseActivities, *capturingEventSink) {
	sink := newCapturingEventSink()
	base := activity.NewBaseActivities(sink)
	capturing := &capturingBaseActivities{
		BaseActivities: base,
		sink:           sink,
	}
	return capturing, sink
}

// GetEventSink returns the underlying event sink for direct access.
func (c *capturingBaseActivities) GetEventSink() *capturingEventSink {
	return c.sink
}

// NewMockProgressReporter creates a ProgressReporter for testing that captures
// all progress messages. This is the test implementation used in tests.
func NewMockProgressReporter() (ProgressReporter, func() []string) {
	var messages []string
	var mu sync.Mutex

	progressReporter := func(message string) {
		mu.Lock()
		defer mu.Unlock()
		messages = append(messages, message)
	}

	getMessages := func() []string {
		mu.Lock()
		defer mu.Unlock()
		result := make([]string, len(messages))
		copy(result, messages)
		return result
	}

	return progressReporter, getMessages
}

// createLargeReasoning creates a reasoning string larger than the blob threshold.
// Used for testing blob-if-large policy behavior.
func createLargeReasoning(threshold int) string {
	// Create a string that's definitely larger than the threshold
	base := "This is a very detailed reasoning that explains the scoring decision in great detail. "
	needed := (threshold / len(base)) + 2 // Ensure we exceed threshold
	result := ""
	for i := 0; i < needed; i++ {
		result += base
	}
	return result
}

// createSmallReasoning creates a reasoning string smaller than the blob threshold.
// Used for testing inline storage behavior.
func createSmallReasoning() string {
	return "Brief reasoning"
}

// createThresholdReasoning creates a reasoning string exactly at the blob threshold.
// Used for testing edge case behavior at the threshold boundary.
func createThresholdReasoning(threshold int) string {
	base := "x"
	return base + string(make([]byte, threshold-1))
}

// testArtifactStore extends InMemoryArtifactStore to provide test-specific functionality.
type testArtifactStore struct {
	*business.InMemoryArtifactStore
	mu      sync.RWMutex
	storage map[string]string
}

// newTestArtifactStore creates an artifact store with testing capabilities.
func newTestArtifactStore() *testArtifactStore {
	return &testArtifactStore{
		InMemoryArtifactStore: business.NewInMemoryArtifactStore(),
		storage:               make(map[string]string),
	}
}

// Put stores content and tracks it for testing.
func (s *testArtifactStore) Put(ctx context.Context, content string, kind domain.ArtifactKind, key string) (domain.ArtifactRef, error) {
	ref, err := s.InMemoryArtifactStore.Put(ctx, content, kind, key)
	if err == nil {
		s.mu.Lock()
		s.storage[key] = content
		s.mu.Unlock()
	}
	return ref, err
}

// List returns all stored artifact keys for testing verification.
func (s *testArtifactStore) List() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]string, 0, len(s.storage))
	for key := range s.storage {
		keys = append(keys, key)
	}
	return keys
}
