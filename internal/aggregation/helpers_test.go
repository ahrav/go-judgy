package aggregation

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/pkg/activity"
	"github.com/ahrav/go-judgy/pkg/events"
)

// Test constants for mock data generation.
const (
	// defaultTestScore is the default score value for test data.
	defaultTestScore = 0.75
	// defaultTestConfidence is the default confidence level for test scores.
	defaultTestConfidence = 0.85
	// defaultTestCost is the default cost in cents for test scores.
	defaultTestCost = 25
	// testTenantID is a valid UUID for testing.
	testTenantID = "550e8400-e29b-41d4-a716-446655440000"
	// testWorkflowID is a deterministic workflow ID for testing.
	testWorkflowID = "test-workflow-deterministic"
)

// Deterministic test answer UUIDs for predictable testing.
var (
	// TestAnswerUUID1 is the deterministic UUID for the first test answer.
	TestAnswerUUID1 = CreateDeterministicTestUUID("answer-1")
	// TestAnswerUUID2 is the deterministic UUID for the second test answer.
	TestAnswerUUID2 = CreateDeterministicTestUUID("answer-2")
	// TestAnswerUUID3 is the deterministic UUID for the third test answer.
	TestAnswerUUID3 = CreateDeterministicTestUUID("answer-3")
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

// EnhancedMockMetricsRecorder provides comprehensive mock behavior for testing.
type EnhancedMockMetricsRecorder struct {
	mu         sync.RWMutex
	counters   map[string]float64
	histograms map[string][]float64
	gauges     map[string]float64

	// Control behavior
	shouldFail    bool
	failureCount  int
	failuresLeft  int
	recordLatency time.Duration
}

// NewEnhancedMockMetricsRecorder creates a properly configured mock metrics recorder.
func NewEnhancedMockMetricsRecorder() *EnhancedMockMetricsRecorder {
	return &EnhancedMockMetricsRecorder{
		counters:   make(map[string]float64),
		histograms: make(map[string][]float64),
		gauges:     make(map[string]float64),
	}
}

// WithFailures configures the mock to fail N times before succeeding.
func (m *EnhancedMockMetricsRecorder) WithFailures(count int) *EnhancedMockMetricsRecorder {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.shouldFail = true
	m.failureCount = count
	m.failuresLeft = count
	return m
}

// WithLatency configures artificial latency for testing timeouts.
func (m *EnhancedMockMetricsRecorder) WithLatency(latency time.Duration) *EnhancedMockMetricsRecorder {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recordLatency = latency
	return m
}

// IncrementCounter implements MetricsRecorder interface.
func (m *EnhancedMockMetricsRecorder) IncrementCounter(name string, tags map[string]string, value float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.recordLatency > 0 {
		time.Sleep(m.recordLatency)
	}

	if m.shouldFail && m.failuresLeft > 0 {
		m.failuresLeft--
		return // Simulate failure by not recording
	}

	key := fmt.Sprintf("%s:%v", name, tags)
	m.counters[key] += value
}

// RecordHistogram implements MetricsRecorder interface.
func (m *EnhancedMockMetricsRecorder) RecordHistogram(name string, tags map[string]string, value float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.recordLatency > 0 {
		time.Sleep(m.recordLatency)
	}

	if m.shouldFail && m.failuresLeft > 0 {
		m.failuresLeft--
		return // Simulate failure by not recording
	}

	key := fmt.Sprintf("%s:%v", name, tags)
	m.histograms[key] = append(m.histograms[key], value)
}

// SetGauge implements MetricsRecorder interface.
func (m *EnhancedMockMetricsRecorder) SetGauge(name string, tags map[string]string, value float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.recordLatency > 0 {
		time.Sleep(m.recordLatency)
	}

	if m.shouldFail && m.failuresLeft > 0 {
		m.failuresLeft--
		return // Simulate failure by not recording
	}

	key := fmt.Sprintf("%s:%v", name, tags)
	m.gauges[key] = value
}

// GetCounters returns a copy of all recorded counters (thread-safe).
func (m *EnhancedMockMetricsRecorder) GetCounters() map[string]float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]float64)
	for k, v := range m.counters {
		result[k] = v
	}
	return result
}

// GetHistograms returns a copy of all recorded histograms (thread-safe).
func (m *EnhancedMockMetricsRecorder) GetHistograms() map[string][]float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string][]float64)
	for k, v := range m.histograms {
		result[k] = make([]float64, len(v))
		copy(result[k], v)
	}
	return result
}

// GetGauges returns a copy of all recorded gauges (thread-safe).
func (m *EnhancedMockMetricsRecorder) GetGauges() map[string]float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]float64)
	for k, v := range m.gauges {
		result[k] = v
	}
	return result
}

// Reset clears all recorded metrics.
func (m *EnhancedMockMetricsRecorder) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.counters = make(map[string]float64)
	m.histograms = make(map[string][]float64)
	m.gauges = make(map[string]float64)
	m.failuresLeft = m.failureCount
}

// ScoreOption provides functional options for test score creation.
type ScoreOption func(*domain.Score)

// WithScoreValue sets the score value.
func WithScoreValue(value float64) ScoreOption {
	return func(s *domain.Score) {
		s.Value = value
	}
}

// WithScoreValidity sets the score validity.
func WithScoreValidity(valid bool, errorMsg string) ScoreOption {
	return func(s *domain.Score) {
		s.ScoreValidity = domain.ScoreValidity{
			Valid: valid,
			Error: errorMsg,
		}
	}
}

// WithInlineReasoning sets inline reasoning for the score.
func WithInlineReasoning(reasoning string) ScoreOption {
	return func(s *domain.Score) {
		s.ScoreEvidence.InlineReasoning = reasoning
		s.ScoreEvidence.ReasonRef = domain.ArtifactRef{} // Clear ref
	}
}

// WithReasonRef sets a reason reference for the score.
func WithReasonRef(key string) ScoreOption {
	return func(s *domain.Score) {
		s.ScoreEvidence.ReasonRef = domain.ArtifactRef{
			Key:  key,
			Kind: domain.ArtifactJudgeRationale,
		}
		s.ScoreEvidence.InlineReasoning = "" // Clear inline
	}
}

// WithScoreConfidence sets the confidence level.
func WithScoreConfidence(confidence float64) ScoreOption {
	return func(s *domain.Score) {
		s.Confidence = confidence
	}
}

// WithScoreCost sets the cost in cents.
func WithScoreCost(costCents int64) ScoreOption {
	return func(s *domain.Score) {
		s.ScoreUsage.CostCents = domain.Cents(costCents)
	}
}

// WithScoreTokens sets the tokens used.
func WithScoreTokens(tokens int64) ScoreOption {
	return func(s *domain.Score) {
		s.ScoreUsage.TokensUsed = tokens
	}
}

// WithScoreDimensions sets dimensional scores.
func WithScoreDimensions(dimensions []domain.DimensionScore) ScoreOption {
	return func(s *domain.Score) {
		s.Dimensions = dimensions
	}
}

// CreateTestScore creates a test score with the given answer ID and options.
func CreateTestScore(answerID string, opts ...ScoreOption) domain.Score {
	score := domain.Score{
		ID:       uuid.New().String(),
		AnswerID: answerID,
		Value:    defaultTestScore,
		ScoreEvidence: domain.ScoreEvidence{
			InlineReasoning: "Test reasoning for score evaluation",
		},
		ScoreProvenance: domain.ScoreProvenance{
			JudgeID:  "test-judge",
			Provider: "test-provider",
			Model:    "test-model",
		},
		ScoreValidity: domain.ScoreValidity{
			Valid: true,
		},
		Confidence: defaultTestConfidence,
		ScoreUsage: domain.ScoreUsage{
			CostCents:  domain.Cents(defaultTestCost),
			TokensUsed: 50,
		},
	}

	for _, opt := range opts {
		opt(&score)
	}

	return score
}

// CreateTestAnswer creates a test answer with the given ID.
func CreateTestAnswer(id string) domain.Answer {
	// Use UUID for ID if the provided ID is not already a UUID
	answerID := id
	if _, err := uuid.Parse(id); err != nil {
		answerID = uuid.New().String()
	}

	return domain.Answer{
		ID: answerID,
		ContentRef: domain.ArtifactRef{
			Key:  fmt.Sprintf("answer-%s.txt", id),
			Kind: domain.ArtifactAnswer,
		},
		AnswerProvenance: domain.AnswerProvenance{
			Provider:    "test-provider",
			Model:       "test-model",
			GeneratedAt: time.Now(),
		},
	}
}

// CreateDeterministicTestUUID creates a deterministic UUID for testing based on a seed.
func CreateDeterministicTestUUID(seed string) string {
	// Use a namespace UUID to create deterministic UUIDs for testing
	namespace := uuid.Must(uuid.Parse("550e8400-e29b-41d4-a716-446655440000"))
	return uuid.NewSHA1(namespace, []byte(seed)).String()
}

// CreateTestAnswers creates multiple test answers with deterministic IDs.
func CreateTestAnswers(count int) []domain.Answer {
	answers := make([]domain.Answer, count)
	for i := 0; i < count; i++ {
		// Generate deterministic UUID for each answer
		answerUUID := CreateDeterministicTestUUID(fmt.Sprintf("answer-%d", i+1))
		answers[i] = CreateTestAnswer(answerUUID)
	}
	return answers
}

// CreateTestScoresForAnswers creates test scores for the given answers.
func CreateTestScoresForAnswers(answers []domain.Answer, scoresPerAnswer int, opts ...ScoreOption) []domain.Score {
	var scores []domain.Score
	for _, answer := range answers {
		for i := 0; i < scoresPerAnswer; i++ {
			score := CreateTestScore(answer.ID, opts...)
			scores = append(scores, score)
		}
	}
	return scores
}

// CreateTestAggregateInput creates a complete test input for AggregateScores.
func CreateTestAggregateInput(numAnswers, scoresPerAnswer int, method domain.AggregationMethod) domain.AggregateScoresInput {
	answers := CreateTestAnswers(numAnswers)
	scores := CreateTestScoresForAnswers(answers, scoresPerAnswer)

	return domain.AggregateScoresInput{
		Scores:  scores,
		Answers: answers,
		Policy: domain.AggregationPolicy{
			Method: method,
		},
		MinValidScores:       1,
		ClientIdempotencyKey: "test-idempotency-key",
	}
}

// CreateTestActivities creates activities with test infrastructure.
func CreateTestActivities(eventSink *CapturingEventSink, metrics *EnhancedMockMetricsRecorder) *Activities {
	// Use capturing sink if provided, otherwise create a new one
	var sink events.EventSink
	if eventSink != nil {
		sink = eventSink
	} else {
		sink = NewCapturingEventSink()
	}

	base := activity.NewBaseActivities(sink)
	activities := NewActivities(base)
	if metrics != nil {
		activities = activities.WithMetrics(metrics)
	}
	return activities
}

// ConfigurableWorkflowContext provides test-controllable workflow metadata.
type ConfigurableWorkflowContext struct {
	WorkflowID string
	RunID      string
	TenantID   string
	ActivityID string
}

// NewTestWorkflowContext creates a workflow context for testing.
func NewTestWorkflowContext(workflowID, tenantID string) ConfigurableWorkflowContext {
	return ConfigurableWorkflowContext{
		WorkflowID: workflowID,
		RunID:      "run-" + uuid.New().String()[:8],
		TenantID:   tenantID,
		ActivityID: "activity-" + uuid.New().String()[:8],
	}
}

// Assertion utilities for test validation.

// AssertAggregationCorrect validates the aggregation output against expected properties.
func AssertAggregationCorrect(
	t interface{ Errorf(string, ...any) },
	output *domain.AggregateScoresOutput,
	input domain.AggregateScoresInput,
) {
	if output == nil {
		t.Errorf("aggregation output is nil")
		return
	}

	// Validate basic properties
	if output.AggregateScore < 0 || output.AggregateScore > 1 {
		t.Errorf("aggregate score %f is outside valid range [0,1]", output.AggregateScore)
	}

	if output.ValidScoreCount < 0 {
		t.Errorf("valid score count %d cannot be negative", output.ValidScoreCount)
	}

	if output.TotalScoreCount != len(input.Scores) {
		t.Errorf("total score count %d does not match input scores %d", output.TotalScoreCount, len(input.Scores))
	}

	if output.Method != input.Policy.Method {
		t.Errorf("output method %s does not match input policy method %s", output.Method, input.Policy.Method)
	}

	// Validate winner
	if output.WinnerAnswerID != "" {
		found := false
		for _, answer := range input.Answers {
			if answer.ID == output.WinnerAnswerID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("winner answer ID %s not found in input answers", output.WinnerAnswerID)
		}
	}
}

// AssertEventsEmitted validates that expected events were emitted.
func AssertEventsEmitted(
	t interface{ Errorf(string, ...any) },
	sink *CapturingEventSink,
	expectedType string,
	expectedCount int,
) {
	events := sink.GetEventsByType(expectedType)
	if len(events) != expectedCount {
		t.Errorf("expected %d %s events, got %d", expectedCount, expectedType, len(events))
	}

	for i, event := range events {
		if event.IdempotencyKey == "" {
			t.Errorf("event %d missing idempotency key", i)
		}
		if event.Type != expectedType {
			t.Errorf("event %d has wrong type: expected %s, got %s", i, expectedType, event.Type)
		}
	}
}

// AssertMetricsRecorded validates that expected metrics were recorded.
func AssertMetricsRecorded(
	t interface{ Errorf(string, ...any) },
	metrics *EnhancedMockMetricsRecorder,
	expectedCounters []string,
	expectedGauges []string,
	expectedHistograms []string,
) {
	counters := metrics.GetCounters()
	gauges := metrics.GetGauges()
	histograms := metrics.GetHistograms()

	for _, expected := range expectedCounters {
		if _, exists := counters[expected]; !exists {
			t.Errorf("expected counter %s not found", expected)
		}
	}

	for _, expected := range expectedGauges {
		if _, exists := gauges[expected]; !exists {
			t.Errorf("expected gauge %s not found", expected)
		}
	}

	for _, expected := range expectedHistograms {
		if _, exists := histograms[expected]; !exists {
			t.Errorf("expected histogram %s not found", expected)
		}
	}
}

// AssertFloatEqual checks float equality within epsilon tolerance.
func AssertFloatEqual(t interface{ Errorf(string, ...any) }, expected, actual float64, msg string) {
	if math.Abs(expected-actual) > epsilon {
		t.Errorf("%s: expected %f, got %f (diff %f > epsilon %f)", msg, expected, actual, math.Abs(expected-actual), epsilon)
	}
}

// AssertEventIdempotency verifies event idempotency keys match between runs.
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

// Edge case generators for comprehensive testing.

// GenerateScoreDistribution creates scores with specific statistical properties.
func GenerateScoreDistribution(answerID string, mean, stddev float64, count int) []domain.Score {
	var scores []domain.Score
	for i := 0; i < count; i++ {
		// Simple normal distribution approximation for testing
		value := mean + stddev*(float64(i%5)-2)/2
		if value < 0 {
			value = 0
		}
		if value > 1 {
			value = 1
		}

		score := CreateTestScore(answerID, WithScoreValue(value))
		scores = append(scores, score)
	}
	return scores
}

// GenerateEpsilonTiedScores creates scores that are within epsilon of each other.
func GenerateEpsilonTiedScores(answerIDs []string, baseValue float64) []domain.Score {
	var scores []domain.Score
	for i, answerID := range answerIDs {
		// Create values within epsilon of baseValue
		value := baseValue + float64(i)*epsilon/10
		score := CreateTestScore(answerID, WithScoreValue(value))
		scores = append(scores, score)
	}
	return scores
}

// GenerateInvalidScores creates a mix of valid and invalid scores for testing.
func GenerateInvalidScores(answerID string, validCount, invalidCount int) []domain.Score {
	var scores []domain.Score

	// Valid scores
	for i := 0; i < validCount; i++ {
		score := CreateTestScore(answerID, WithScoreValue(0.5+float64(i)*0.1))
		scores = append(scores, score)
	}

	// Invalid scores
	for i := 0; i < invalidCount; i++ {
		score := CreateTestScore(answerID,
			WithScoreValidity(false, fmt.Sprintf("test error %d", i)),
			WithScoreValue(0.0),
		)
		scores = append(scores, score)
	}

	return scores
}
