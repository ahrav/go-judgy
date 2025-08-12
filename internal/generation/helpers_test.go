package generation

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/business"
	"github.com/ahrav/go-judgy/internal/llm/transport"
	"github.com/ahrav/go-judgy/pkg/activity"
	"github.com/ahrav/go-judgy/pkg/events"
)

// Test constants for mock data generation.
const (
	// defaultCacheHitLatencyMs is the latency in milliseconds for cache hits.
	defaultCacheHitLatencyMs = 50
	// defaultLatencyMs is the default latency in milliseconds for non-cached responses.
	defaultLatencyMs = 200
	// tokensPerAnswer is the number of tokens per answer in test scenarios.
	tokensPerAnswer = 100
	// centsPerAnswer is the cost in cents per answer in test scenarios.
	centsPerAnswer = 25
	// defaultScoreValue is the default score value for test answers.
	defaultScoreValue = 75
	// defaultScoreConfidence is the default confidence level for test scores.
	defaultScoreConfidence = 0.85
	// tokensPerScore is the number of tokens per score in test scenarios.
	tokensPerScore = 50
)

// CapturingEventSink captures all emitted events for test assertions.
// Thread-safe implementation that records events with metadata for validation.
type CapturingEventSink struct {
	mu     sync.RWMutex
	events []events.Envelope
	// Track idempotency to ensure no duplicates
	seenKeys map[string]bool
	// Optional: simulate failures for resilience testing
	failureCount int
	failuresLeft int
}

// NewCapturingEventSink creates a new capturing event sink for testing.
func NewCapturingEventSink() *CapturingEventSink {
	return &CapturingEventSink{
		events:   make([]events.Envelope, 0),
		seenKeys: make(map[string]bool),
	}
}

// NewFailingEventSink creates a sink that fails N times before succeeding.
func NewFailingEventSink(failures int) *CapturingEventSink {
	return &CapturingEventSink{
		events:       make([]events.Envelope, 0),
		seenKeys:     make(map[string]bool),
		failureCount: failures,
		failuresLeft: failures,
	}
}

// Append adds an event to the sink, implementing the EventSink interface.
func (c *CapturingEventSink) Append(ctx context.Context, envelope events.Envelope) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Simulate failures for resilience testing
	if c.failuresLeft > 0 {
		c.failuresLeft--
		return errors.New("simulated event sink failure")
	}

	// Check for duplicate idempotency keys
	if c.seenKeys[envelope.IdempotencyKey] {
		// Idempotent - don't store duplicate
		return nil
	}

	c.events = append(c.events, envelope)
	c.seenKeys[envelope.IdempotencyKey] = true
	return nil
}

// GetEvents returns all captured events (thread-safe).
func (c *CapturingEventSink) GetEvents() []events.Envelope {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]events.Envelope, len(c.events))
	copy(result, c.events)
	return result
}

// GetEventsByType returns events filtered by type.
func (c *CapturingEventSink) GetEventsByType(eventType string) []events.Envelope {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var filtered []events.Envelope
	for _, e := range c.events {
		if e.Type == eventType {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// AssertEventCount asserts the expected number of events by type.
func (c *CapturingEventSink) AssertEventCount(eventType string, expected int) error {
	actual := len(c.GetEventsByType(eventType))
	if actual != expected {
		return fmt.Errorf("expected %d %s events, got %d", expected, eventType, actual)
	}
	return nil
}

// AssertIdempotencyKeys verifies all events have proper idempotency keys.
func (c *CapturingEventSink) AssertIdempotencyKeys() error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for i, e := range c.events {
		if e.IdempotencyKey == "" {
			return fmt.Errorf("event %d (type %s) missing idempotency key", i, e.Type)
		}
	}
	return nil
}

// Reset clears all captured events for reuse in tests.
func (c *CapturingEventSink) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.events = make([]events.Envelope, 0)
	c.seenKeys = make(map[string]bool)
	c.failuresLeft = c.failureCount
}

// EnhancedMockLLMClient provides comprehensive mock behavior for testing.
type EnhancedMockLLMClient struct {
	// Behavior control
	generateReturnsError bool
	scoreReturnsError    bool
	generateError        error
	scoreError           error

	// Delays for timeout testing
	generateDelay time.Duration
	scoreDelay    time.Duration

	// Cache simulation
	simulateCacheHit bool
	cacheHitLatency  time.Duration

	// Response customization
	numAnswers        int
	useProperIDs      bool
	includeProvenance bool

	// Call tracking
	generateCalls int64
	scoreCalls    int64
	mu            sync.Mutex
}

// NewEnhancedMockClient creates a properly configured mock client.
func NewEnhancedMockClient() *EnhancedMockLLMClient {
	return &EnhancedMockLLMClient{
		numAnswers:        1,
		useProperIDs:      true,
		includeProvenance: true,
		cacheHitLatency:   defaultCacheHitLatencyMs * time.Millisecond, // Fast response indicates cache hit
	}
}

// Generate implements the LLM client interface with enhanced testing features.
func (m *EnhancedMockLLMClient) Generate(ctx context.Context, input domain.GenerateAnswersInput) (*domain.GenerateAnswersOutput, error) {
	m.mu.Lock()
	m.generateCalls++
	m.mu.Unlock()

	// Simulate delay for timeout testing
	if m.generateDelay > 0 {
		select {
		case <-time.After(m.generateDelay):
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled: %w", ctx.Err())
		}
	}

	if m.generateReturnsError {
		if m.generateError != nil {
			return nil, m.generateError
		}
		return nil, fmt.Errorf("generate failed: %w", ErrProviderUnavailable)
	}

	// Generate proper idempotency key like real client
	tenantID := "test-tenant-deterministic"
	provider := "openai"
	model := "gpt-4"
	if input.Config.Provider != "" {
		provider = input.Config.Provider
	}
	if input.Config.Model != "" {
		model = input.Config.Model
	}

	canonicalPayload, err := transport.BuildCanonicalPayloadFromGenerate(
		tenantID,
		input.Question,
		provider,
		model,
		"", // SystemPrompt not in EvalConfig
		int(input.Config.MaxAnswerTokens),
		input.Config.Temperature,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build canonical payload: %w", err)
	}

	clientIdemKey := transport.HashCanonicalPayload(canonicalPayload)

	// Generate answers
	answers := make([]domain.Answer, 0, m.numAnswers)
	for i := 0; i < m.numAnswers; i++ {
		answerID := fmt.Sprintf("answer-%d", i)
		if m.useProperIDs {
			answerID = uuid.New().String()
		}

		latency := int64(defaultLatencyMs) // Normal latency
		if m.simulateCacheHit {
			latency = int64(m.cacheHitLatency / time.Millisecond)
		}

		answer := domain.Answer{
			ID: answerID,
			Metadata: map[string]any{
				"content":        fmt.Sprintf("Test answer #%d from enhanced mock", i+1),
				"latency_millis": latency, // Store in metadata if needed
			},
		}

		if m.includeProvenance {
			answer.Provider = provider
			answer.Model = model
			answer.ProviderRequestIDs = []string{fmt.Sprintf("req-%s-%d", uuid.New().String()[:8], i)}
		}

		answers = append(answers, answer)
	}

	return &domain.GenerateAnswersOutput{
		Answers:       answers,
		TokensUsed:    int64(tokensPerAnswer * m.numAnswers),
		CallsMade:     1,
		CostCents:     domain.Cents(centsPerAnswer * m.numAnswers),
		ClientIdemKey: clientIdemKey,
	}, nil
}

// Score implements the LLM client interface for scoring.
func (m *EnhancedMockLLMClient) Score(ctx context.Context, input domain.ScoreAnswersInput) (*domain.ScoreAnswersOutput, error) {
	m.mu.Lock()
	m.scoreCalls++
	m.mu.Unlock()

	// Simulate delay for timeout testing
	if m.scoreDelay > 0 {
		select {
		case <-time.After(m.scoreDelay):
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled: %w", ctx.Err())
		}
	}

	if m.scoreReturnsError {
		if m.scoreError != nil {
			return nil, m.scoreError
		}
		return nil, fmt.Errorf("score failed: %w", ErrProviderUnavailable)
	}

	scores := make([]domain.Score, 0, len(input.Answers))
	for _, answer := range input.Answers {
		scoreID := uuid.New().String()
		if !m.useProperIDs {
			scoreID = fmt.Sprintf("score-%s", answer.ID)
		}

		scores = append(scores, domain.Score{
			ID:         scoreID,
			AnswerID:   answer.ID,
			Value:      defaultScoreValue,
			Confidence: defaultScoreConfidence,
			ScoreValidity: domain.ScoreValidity{
				Valid: true,
			},
		})
	}

	return &domain.ScoreAnswersOutput{
		Scores:     scores,
		TokensUsed: int64(tokensPerScore * len(scores)),
		CallsMade:  1,
	}, nil
}

// ConfigurableWorkflowContext provides test-controllable workflow metadata.
type ConfigurableWorkflowContext struct {
	WorkflowID string
	RunID      string
	TenantID   string
	TeamID     string // Add team support
	ActivityID string
}

// NewTestWorkflowContext creates a workflow context for testing.
func NewTestWorkflowContext(workflowID, tenantID string) ConfigurableWorkflowContext {
	return ConfigurableWorkflowContext{
		WorkflowID: workflowID,
		RunID:      "run-" + uuid.New().String()[:8],
		TenantID:   tenantID,
		TeamID:     "team-" + tenantID,
		ActivityID: "activity-" + uuid.New().String()[:8],
	}
}

// CreateTestActivities creates activities with test infrastructure.
func CreateTestActivities(client *EnhancedMockLLMClient, sink *CapturingEventSink) *Activities {
	base := activity.NewBaseActivities(sink)
	artifactStore := business.NewInMemoryArtifactStore()
	return NewActivities(base, client, artifactStore)
}

// AssertArtifactIdempotency verifies artifact keys are deterministic.
func AssertArtifactIdempotency(
	t interface{ Errorf(string, ...any) },
	key1, key2 string,
	message string,
) {
	if key1 != key2 {
		t.Errorf("%s: expected identical artifact keys, got %q and %q", message, key1, key2)
	}
}

// AssertEventIdempotency verifies event idempotency keys match.
func AssertEventIdempotency(
	t interface{ Errorf(string, ...any) },
	events1, events2 []events.Envelope,
	eventType string,
) {
	if len(events1) != len(events2) {
		t.Errorf("different number of %s events: %d vs %d", eventType, len(events1), len(events2))
		return
	}

	for i := range events1 {
		if events1[i].IdempotencyKey != events2[i].IdempotencyKey {
			t.Errorf("%s event %d: different idempotency keys: %q vs %q",
				eventType, i, events1[i].IdempotencyKey, events2[i].IdempotencyKey)
		}
	}
}
