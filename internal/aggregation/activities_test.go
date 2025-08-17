package aggregation

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/pkg/activity"
)

// MockMetricsRecorder implements MetricsRecorder for testing.
type MockMetricsRecorder struct {
	counters   map[string]float64
	histograms map[string][]float64
	gauges     map[string]float64
}

func NewMockMetricsRecorder() *MockMetricsRecorder {
	return &MockMetricsRecorder{
		counters:   make(map[string]float64),
		histograms: make(map[string][]float64),
		gauges:     make(map[string]float64),
	}
}

func (m *MockMetricsRecorder) IncrementCounter(name string, tags map[string]string, value float64) {
	key := fmt.Sprintf("%s:%v", name, tags)
	m.counters[key] += value
}

func (m *MockMetricsRecorder) RecordHistogram(name string, tags map[string]string, value float64) {
	key := fmt.Sprintf("%s:%v", name, tags)
	m.histograms[key] = append(m.histograms[key], value)
}

func (m *MockMetricsRecorder) SetGauge(name string, tags map[string]string, value float64) {
	key := fmt.Sprintf("%s:%v", name, tags)
	m.gauges[key] = value
}

// Helper function to create a valid score with inline reasoning
func createScoreWithInline(id, answerID string, value float64, valid bool) domain.Score {
	score := domain.Score{
		ID:       id,
		AnswerID: answerID,
		Value:    value,
		ScoreEvidence: domain.ScoreEvidence{
			InlineReasoning: "test reasoning",
		},
		ScoreProvenance: domain.ScoreProvenance{
			JudgeID:  "judge1",
			Provider: "openai",
			Model:    "gpt-4",
		},
		ScoreValidity: domain.ScoreValidity{
			Valid: valid,
		},
	}
	return score
}

// Helper function to create a valid score with ReasonRef
func createScoreWithRef(id, answerID string, value float64, valid bool, refKey string) domain.Score {
	score := domain.Score{
		ID:       id,
		AnswerID: answerID,
		Value:    value,
		ScoreEvidence: domain.ScoreEvidence{
			ReasonRef: domain.ArtifactRef{
				Key:  refKey,
				Kind: domain.ArtifactJudgeRationale,
			},
		},
		ScoreProvenance: domain.ScoreProvenance{
			JudgeID:  "judge1",
			Provider: "openai",
			Model:    "gpt-4",
		},
		ScoreValidity: domain.ScoreValidity{
			Valid: valid,
		},
	}
	return score
}

// Test winner aggregation with policy-aware per-answer aggregation
func TestDetermineWinnerWithPolicyAggregation(t *testing.T) {
	tests := []struct {
		name           string
		scores         []domain.Score
		answers        []domain.Answer
		policy         domain.AggregationPolicy
		expectedWinner string
		expectedTied   []string
		description    string
	}{
		{
			name: "median_policy_lower_single_score_wins",
			scores: []domain.Score{
				createScoreWithInline("s1", "answer1", 0.9, true),
				createScoreWithInline("s2", "answer1", 0.2, true),
				createScoreWithInline("s3", "answer1", 0.2, true),
				createScoreWithInline("s4", "answer2", 0.5, true),
				createScoreWithInline("s5", "answer2", 0.5, true),
				createScoreWithInline("s6", "answer2", 0.5, true),
			},
			answers: []domain.Answer{
				{ID: "answer1"},
				{ID: "answer2"},
			},
			policy: domain.AggregationPolicy{
				Method: "median",
			},
			expectedWinner: "answer2",
			expectedTied:   nil,
			description:    "Answer2 wins with median 0.5 vs Answer1 median 0.2, despite Answer1 having highest single score",
		},
		{
			name: "mean_policy_aggregates_correctly",
			scores: []domain.Score{
				createScoreWithInline("s1", "answer1", 0.8, true),
				createScoreWithInline("s2", "answer1", 0.7, true),
				createScoreWithInline("s3", "answer1", 0.9, true),
				createScoreWithInline("s4", "answer2", 0.6, true),
				createScoreWithInline("s5", "answer2", 0.6, true),
				createScoreWithInline("s6", "answer2", 0.6, true),
			},
			answers: []domain.Answer{
				{ID: "answer1"},
				{ID: "answer2"},
			},
			policy: domain.AggregationPolicy{
				Method: "mean",
			},
			expectedWinner: "answer1",
			expectedTied:   nil,
			description:    "Answer1 wins with mean 0.8 vs Answer2 mean 0.6",
		},
		{
			name: "trimmed_mean_policy",
			scores: []domain.Score{
				createScoreWithInline("s1", "answer1", 1.0, true), // trimmed
				createScoreWithInline("s2", "answer1", 0.5, true),
				createScoreWithInline("s3", "answer1", 0.5, true),
				createScoreWithInline("s4", "answer1", 0.0, true), // trimmed
				createScoreWithInline("s5", "answer2", 0.6, true),
				createScoreWithInline("s6", "answer2", 0.6, true),
			},
			answers: []domain.Answer{
				{ID: "answer1"},
				{ID: "answer2"},
			},
			policy: domain.AggregationPolicy{
				Method:       "trimmed_mean",
				TrimFraction: 0.25,
			},
			expectedWinner: "answer2",
			expectedTied:   nil,
			description:    "Answer2 wins with trimmed mean 0.6 vs Answer1 trimmed mean 0.5",
		},
		{
			name: "epsilon_tie_detection",
			scores: []domain.Score{
				createScoreWithInline("s1", "answer1", 0.5, true),
				createScoreWithInline("s2", "answer1", 0.5, true),
				createScoreWithInline("s3", "answer2", 0.5+1e-10, true), // Within epsilon
				createScoreWithInline("s4", "answer2", 0.5-1e-10, true), // Within epsilon
			},
			answers: []domain.Answer{
				{ID: "answer1"},
				{ID: "answer2"},
			},
			policy: domain.AggregationPolicy{
				Method: "mean",
			},
			expectedWinner: "answer1", // Lexicographic tie-break
			expectedTied:   []string{"answer2"},
			description:    "Detects tie within epsilon and breaks lexicographically",
		},
		{
			name: "lexicographic_tie_breaking",
			scores: []domain.Score{
				createScoreWithInline("s1", "zebra", 0.5, true),
				createScoreWithInline("s2", "apple", 0.5, true),
				createScoreWithInline("s3", "banana", 0.5, true),
			},
			answers: []domain.Answer{
				{ID: "zebra"},
				{ID: "apple"},
				{ID: "banana"},
			},
			policy: domain.AggregationPolicy{
				Method: "mean",
			},
			expectedWinner: "apple",
			expectedTied:   []string{"banana", "zebra"},
			description:    "Breaks tie lexicographically",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			winner, tied := determineWinnerWithPolicyAggregation(tt.scores, tt.answers, tt.policy)
			assert.Equal(t, tt.expectedWinner, winner, tt.description)
			assert.Equal(t, tt.expectedTied, tied, tt.description)
		})
	}
}

// Test trimmed mean edge cases
func TestCalculateTrimmedMean(t *testing.T) {
	tests := []struct {
		name         string
		scores       []domain.Score
		trimFraction float64
		expected     float64
		description  string
	}{
		{
			name: "trim_0.5_with_n2_fallback_to_mean",
			scores: []domain.Score{
				createScoreWithInline("s1", "a1", 0.2, true),
				createScoreWithInline("s2", "a1", 0.8, true),
			},
			trimFraction: 0.5,
			expected:     0.5,
			description:  "Trim 0.5 with n=2 should fallback to mean",
		},
		{
			name: "trim_0.5_with_n3_fallback_to_mean",
			scores: []domain.Score{
				createScoreWithInline("s1", "a1", 0.2, true),
				createScoreWithInline("s2", "a1", 0.5, true),
				createScoreWithInline("s3", "a1", 0.8, true),
			},
			trimFraction: 0.5,
			expected:     0.5,
			description:  "Trim 0.5 with n=3 should fallback to mean",
		},
		{
			name: "trim_0.5_with_n4_fallback_to_mean",
			scores: []domain.Score{
				createScoreWithInline("s1", "a1", 0.1, true),
				createScoreWithInline("s2", "a1", 0.3, true),
				createScoreWithInline("s3", "a1", 0.7, true),
				createScoreWithInline("s4", "a1", 0.9, true),
			},
			trimFraction: 0.5,
			expected:     0.5,
			description:  "Trim 0.5 with n=4 (trimCount*2 == n) should fallback to mean",
		},
		{
			name: "trim_clamped_above_0.5",
			scores: []domain.Score{
				createScoreWithInline("s1", "a1", 0.1, true),
				createScoreWithInline("s2", "a1", 0.3, true),
				createScoreWithInline("s3", "a1", 0.7, true),
				createScoreWithInline("s4", "a1", 0.9, true),
			},
			trimFraction: 0.7, // Will be clamped to 0.5
			expected:     0.5,
			description:  "Trim fraction >0.5 is clamped to 0.5",
		},
		{
			name: "trim_clamped_below_0",
			scores: []domain.Score{
				createScoreWithInline("s1", "a1", 0.1, true),
				createScoreWithInline("s2", "a1", 0.3, true),
				createScoreWithInline("s3", "a1", 0.7, true),
				createScoreWithInline("s4", "a1", 0.9, true),
			},
			trimFraction: -0.1, // Will be clamped to 0
			expected:     0.5,
			description:  "Negative trim fraction is clamped to 0",
		},
		{
			name: "normal_trimmed_mean",
			scores: []domain.Score{
				createScoreWithInline("s1", "a1", 0.0, true), // Will be trimmed
				createScoreWithInline("s2", "a1", 0.3, true),
				createScoreWithInline("s3", "a1", 0.5, true),
				createScoreWithInline("s4", "a1", 0.7, true),
				createScoreWithInline("s5", "a1", 1.0, true), // Will be trimmed
			},
			trimFraction: 0.2,
			expected:     0.5,
			description:  "Normal trimmed mean removes 1 from each end",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateTrimmedMean(tt.scores, tt.trimFraction)
			assert.InDelta(t, tt.expected, result, epsilon, tt.description)
		})
	}
}

// Test reasoning validation
func TestValidateScoreReasoning(t *testing.T) {
	tests := []struct {
		name        string
		scores      []domain.Score
		expectError bool
		description string
	}{
		{
			name: "valid_scores_with_inline_reasoning",
			scores: []domain.Score{
				createScoreWithInline("s1", "a1", 0.5, true),
				createScoreWithInline("s2", "a2", 0.6, true),
			},
			expectError: false,
			description: "Valid scores with inline reasoning should pass",
		},
		{
			name: "valid_scores_with_reason_ref",
			scores: []domain.Score{
				createScoreWithRef("s1", "a1", 0.5, true, "ref1"),
				createScoreWithRef("s2", "a2", 0.6, true, "ref2"),
			},
			expectError: false,
			description: "Valid scores with ReasonRef should pass",
		},
		{
			name: "both_reasoning_fields_set",
			scores: []domain.Score{
				{
					ID:       "s1",
					AnswerID: "a1",
					Value:    0.5,
					ScoreEvidence: domain.ScoreEvidence{
						InlineReasoning: "inline",
						ReasonRef: domain.ArtifactRef{
							Key:  "ref1",
							Kind: domain.ArtifactJudgeRationale,
						},
					},
					ScoreProvenance: domain.ScoreProvenance{
						JudgeID:  "judge1",
						Provider: "openai",
						Model:    "gpt-4",
					},
					ScoreValidity: domain.ScoreValidity{
						Valid: true,
					},
				},
			},
			expectError: true,
			description: "Valid score with both reasoning fields set should fail",
		},
		{
			name: "neither_reasoning_field_set",
			scores: []domain.Score{
				{
					ID:            "s1",
					AnswerID:      "a1",
					Value:         0.5,
					ScoreEvidence: domain.ScoreEvidence{
						// Neither InlineReasoning nor ReasonRef set
					},
					ScoreProvenance: domain.ScoreProvenance{
						JudgeID:  "judge1",
						Provider: "openai",
						Model:    "gpt-4",
					},
					ScoreValidity: domain.ScoreValidity{
						Valid: true,
					},
				},
			},
			expectError: true,
			description: "Valid score with neither reasoning field set should fail",
		},
		{
			name: "invalid_score_with_no_reasoning_allowed",
			scores: []domain.Score{
				{
					ID:            "s1",
					AnswerID:      "a1",
					Value:         0.5,
					ScoreEvidence: domain.ScoreEvidence{
						// No reasoning
					},
					ScoreProvenance: domain.ScoreProvenance{
						JudgeID:  "judge1",
						Provider: "openai",
						Model:    "gpt-4",
					},
					ScoreValidity: domain.ScoreValidity{
						Valid: false, // Invalid score
						Error: "error occurred",
					},
				},
			},
			expectError: false,
			description: "Invalid scores are allowed to have no reasoning",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateScoreReasoning(tt.scores)
			if tt.expectError {
				assert.Error(t, err, tt.description)
			} else {
				assert.NoError(t, err, tt.description)
			}
		})
	}
}

// Test float epsilon comparisons
func TestEpsilonComparisons(t *testing.T) {
	tests := []struct {
		name        string
		value1      float64
		value2      float64
		areTied     bool
		description string
	}{
		{
			name:        "exact_match",
			value1:      0.5,
			value2:      0.5,
			areTied:     true,
			description: "Exact match should be tied",
		},
		{
			name:        "within_epsilon",
			value1:      0.5,
			value2:      0.5 + 1e-10,
			areTied:     true,
			description: "Values within epsilon should be tied",
		},
		{
			name:        "outside_epsilon",
			value1:      0.5,
			value2:      0.5 + 2e-9,
			areTied:     false,
			description: "Values outside epsilon should not be tied",
		},
		{
			name:        "negative_epsilon_diff",
			value1:      0.5,
			value2:      0.5 - 1e-10,
			areTied:     true,
			description: "Negative difference within epsilon should be tied",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tied := math.Abs(tt.value1-tt.value2) <= epsilon
			assert.Equal(t, tt.areTied, tied, tt.description)
		})
	}
}

// Test event emission includes all ArtifactRefs
func TestEmitVerdictReachedEvent(t *testing.T) {
	ctx := context.Background()
	base := activity.NewBaseActivities(nil)
	activities := NewActivities(base)

	scores := []domain.Score{
		createScoreWithRef("s1", "a1", 0.5, true, "ref1"),
		createScoreWithRef("s2", "a2", 0.6, true, "ref2"),
		createScoreWithInline("s3", "a3", 0.7, true),
		createScoreWithRef("s4", "a4", 0.8, true, "ref3"),
	}

	output := &domain.AggregateScoresOutput{
		WinnerAnswerID:  "a4",
		AggregateScore:  0.65,
		Method:          "mean",
		ValidScoreCount: 4,
		TotalScoreCount: 4,
		CostCents:       100,
	}

	input := domain.AggregateScoresInput{
		Scores:               scores,
		ClientIdempotencyKey: "test-key",
	}

	wfCtx := activity.WorkflowContext{
		WorkflowID: "wf1",
		RunID:      "run1",
		TenantID:   "550e8400-e29b-41d4-a716-446655440000",
		ActivityID: "act1",
	}

	// This should collect artifact refs: ["ref1", "ref2", "ref3"]
	activities.emitVerdictReachedEvent(ctx, output, input, wfCtx)
	// The event emission is best-effort, so we don't check the result
}

// Test metrics recording
func TestMetricsRecording(t *testing.T) {
	ctx := context.Background()
	mockMetrics := NewMockMetricsRecorder()
	base := activity.NewBaseActivities(nil)
	activities := NewActivities(base).WithMetrics(mockMetrics)

	answerID1 := uuid.New().String()
	answerID2 := uuid.New().String()

	input := domain.AggregateScoresInput{
		Scores: []domain.Score{
			createScoreWithInline(uuid.New().String(), answerID1, 0.5, true),
			createScoreWithInline(uuid.New().String(), answerID1, 0.5, true),
			createScoreWithInline(uuid.New().String(), answerID2, 0.6, true),
			createScoreWithInline(uuid.New().String(), answerID2, 0.6, false), // Invalid score
		},
		Answers: []domain.Answer{
			{
				ID: answerID1,
				ContentRef: domain.ArtifactRef{
					Key:  "answer1.txt",
					Kind: domain.ArtifactAnswer,
				},
				AnswerProvenance: domain.AnswerProvenance{
					Provider:    "test",
					Model:       "test-model",
					GeneratedAt: time.Now(),
				},
			},
			{
				ID: answerID2,
				ContentRef: domain.ArtifactRef{
					Key:  "answer2.txt",
					Kind: domain.ArtifactAnswer,
				},
				AnswerProvenance: domain.AnswerProvenance{
					Provider:    "test",
					Model:       "test-model",
					GeneratedAt: time.Now(),
				},
			},
		},
		Policy: domain.AggregationPolicy{
			Method: "mean",
		},
		MinValidScores:       2,
		ClientIdempotencyKey: "test-key",
	}

	output, err := activities.AggregateScores(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, output)

	// Check metrics were recorded
	assert.Contains(t, mockMetrics.counters, "aggregation_method_used:map[method:mean tenant:550e8400-e29b-41d4-a716-446655440000]")
	assert.Contains(t, mockMetrics.gauges, "invalid_score_rate:map[tenant:550e8400-e29b-41d4-a716-446655440000]")
	assert.Contains(t, mockMetrics.histograms, "score_distribution:map[method:mean tenant:550e8400-e29b-41d4-a716-446655440000]")

	// Check invalid score rate
	invalidRate := mockMetrics.gauges["invalid_score_rate:map[tenant:550e8400-e29b-41d4-a716-446655440000]"]
	assert.InDelta(t, 0.25, invalidRate, epsilon) // 1 invalid out of 4 scores
}

// Test input validation failure
func TestInputValidationFailure(t *testing.T) {
	ctx := context.Background()
	base := activity.NewBaseActivities(nil)
	activities := NewActivities(base)

	tests := []struct {
		name        string
		input       domain.AggregateScoresInput
		expectError string
	}{
		{
			name: "empty_scores",
			input: domain.AggregateScoresInput{
				Scores:               []domain.Score{},
				Answers:              []domain.Answer{{ID: "a1"}},
				Policy:               domain.AggregationPolicy{Method: "mean"},
				MinValidScores:       1,
				ClientIdempotencyKey: "test-key",
			},
			expectError: "invalid input",
		},
		{
			name: "below_min_valid_scores",
			input: domain.AggregateScoresInput{
				Scores: []domain.Score{
					createScoreWithInline("s1", "a1", 0.5, false), // Invalid
				},
				Answers:              []domain.Answer{{ID: "a1"}},
				Policy:               domain.AggregationPolicy{Method: "mean"},
				MinValidScores:       2,
				ClientIdempotencyKey: "test-key",
			},
			expectError: "minimum valid scores not met",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := activities.AggregateScores(ctx, tt.input)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectError)
		})
	}
}

// Test that idempotency key is properly generated
func TestIdempotencyKeyGeneration(t *testing.T) {
	clientKey := "client-key-123"
	verdictKey := domain.VerdictReachedIdempotencyKey(clientKey)

	// Should follow the pattern H(client_key || ":verdict:1")
	assert.NotEmpty(t, verdictKey)
	assert.Len(t, verdictKey, 64) // SHA256 produces 64 hex characters

	// Should be deterministic
	verdictKey2 := domain.VerdictReachedIdempotencyKey(clientKey)
	assert.Equal(t, verdictKey, verdictKey2)

	// Different client keys should produce different verdict keys
	differentKey := domain.VerdictReachedIdempotencyKey("different-key")
	assert.NotEqual(t, verdictKey, differentKey)
}

// Test median calculation with epsilon awareness
func TestCalculateMedian(t *testing.T) {
	tests := []struct {
		name     string
		scores   []domain.Score
		expected float64
	}{
		{
			name: "odd_number_of_scores",
			scores: []domain.Score{
				createScoreWithInline("s1", "a1", 0.1, true),
				createScoreWithInline("s2", "a1", 0.5, true),
				createScoreWithInline("s3", "a1", 0.9, true),
			},
			expected: 0.5,
		},
		{
			name: "even_number_of_scores",
			scores: []domain.Score{
				createScoreWithInline("s1", "a1", 0.2, true),
				createScoreWithInline("s2", "a1", 0.4, true),
				createScoreWithInline("s3", "a1", 0.6, true),
				createScoreWithInline("s4", "a1", 0.8, true),
			},
			expected: 0.5, // (0.4 + 0.6) / 2
		},
		{
			name:     "empty_scores",
			scores:   []domain.Score{},
			expected: 0,
		},
		{
			name: "single_score",
			scores: []domain.Score{
				createScoreWithInline("s1", "a1", 0.7, true),
			},
			expected: 0.7,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateMedian(tt.scores)
			assert.InDelta(t, tt.expected, result, epsilon)
		})
	}
}

// Test tie frequency metric
func TestTieFrequencyMetric(t *testing.T) {
	ctx := context.Background()
	mockMetrics := NewMockMetricsRecorder()
	base := activity.NewBaseActivities(nil)
	activities := NewActivities(base).WithMetrics(mockMetrics)

	answerID1 := uuid.New().String()
	answerID2 := uuid.New().String()

	// Create a scenario with a tie
	input := domain.AggregateScoresInput{
		Scores: []domain.Score{
			createScoreWithInline(uuid.New().String(), answerID1, 0.5, true),
			createScoreWithInline(uuid.New().String(), answerID2, 0.5, true),
		},
		Answers: []domain.Answer{
			{
				ID: answerID1,
				ContentRef: domain.ArtifactRef{
					Key:  "answer1.txt",
					Kind: domain.ArtifactAnswer,
				},
				AnswerProvenance: domain.AnswerProvenance{
					Provider:    "test",
					Model:       "test-model",
					GeneratedAt: time.Now(),
				},
			},
			{
				ID: answerID2,
				ContentRef: domain.ArtifactRef{
					Key:  "answer2.txt",
					Kind: domain.ArtifactAnswer,
				},
				AnswerProvenance: domain.AnswerProvenance{
					Provider:    "test",
					Model:       "test-model",
					GeneratedAt: time.Now(),
				},
			},
		},
		Policy: domain.AggregationPolicy{
			Method: "mean",
		},
		MinValidScores:       1,
		ClientIdempotencyKey: "test-key",
	}

	output, err := activities.AggregateScores(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, output)

	// Check that tie frequency was recorded
	key := "tie_frequency:map[method:mean tenant:550e8400-e29b-41d4-a716-446655440000]"
	assert.Contains(t, mockMetrics.counters, key)
	assert.Equal(t, float64(1), mockMetrics.counters[key])

	// Check that we have a winner and a tied answer
	assert.NotEmpty(t, output.WinnerAnswerID)
	assert.Len(t, output.TiedWithIDs, 1)
}

// =============================================================================
// COMPREHENSIVE TEST SUITE USING NEW TEST UTILITIES
// =============================================================================

// TestAggregateScoresComprehensive provides comprehensive testing of the AggregateScores activity
// using the new test utilities. Tests all aggregation methods, edge cases, and error scenarios.
func TestAggregateScoresComprehensive(t *testing.T) {
	tests := []struct {
		name            string
		setupInput      func() domain.AggregateScoresInput
		expectedError   string
		validateOutput  func(t *testing.T, output *domain.AggregateScoresOutput)
		validateMetrics func(t *testing.T, metrics *EnhancedMockMetricsRecorder)
		validateEvents  func(t *testing.T, sink *CapturingEventSink)
	}{
		{
			name: "successful_mean_aggregation",
			setupInput: func() domain.AggregateScoresInput {
				answers := CreateTestAnswers(2)
				scores := []domain.Score{
					CreateTestScore(answers[0].ID, WithScoreValue(0.8)),
					CreateTestScore(answers[0].ID, WithScoreValue(0.6)),
					CreateTestScore(answers[1].ID, WithScoreValue(0.4)),
					CreateTestScore(answers[1].ID, WithScoreValue(0.2)),
				}
				return domain.AggregateScoresInput{
					Scores:               scores,
					Answers:              answers,
					Policy:               domain.AggregationPolicy{Method: "mean"},
					MinValidScores:       2,
					ClientIdempotencyKey: "test-mean-key",
				}
			},
			validateOutput: func(t *testing.T, output *domain.AggregateScoresOutput) {
				assert.Equal(t, TestAnswerUUID1, output.WinnerAnswerID) // First answer wins with mean 0.7
				AssertFloatEqual(t, 0.5, output.AggregateScore, "overall aggregate score")
				assert.Equal(t, domain.AggregationMethodMean, output.Method)
				assert.Equal(t, 4, output.ValidScoreCount)
				assert.Equal(t, 4, output.TotalScoreCount)
			},
			validateMetrics: func(t *testing.T, metrics *EnhancedMockMetricsRecorder) {
				counters := metrics.GetCounters()
				assert.Contains(t, counters, "aggregation_method_used:map[method:mean tenant:550e8400-e29b-41d4-a716-446655440000]")
			},
			validateEvents: func(t *testing.T, sink *CapturingEventSink) {
				AssertEventsEmitted(t, sink, "VerdictReached", 1)
			},
		},
		{
			name: "median_aggregation_with_outliers",
			setupInput: func() domain.AggregateScoresInput {
				answers := CreateTestAnswers(2)
				scores := []domain.Score{
					CreateTestScore(answers[0].ID, WithScoreValue(0.1)), // Outlier
					CreateTestScore(answers[0].ID, WithScoreValue(0.5)),
					CreateTestScore(answers[0].ID, WithScoreValue(0.5)),
					CreateTestScore(answers[0].ID, WithScoreValue(0.9)), // Outlier
					CreateTestScore(answers[1].ID, WithScoreValue(0.4)),
					CreateTestScore(answers[1].ID, WithScoreValue(0.4)),
				}
				return domain.AggregateScoresInput{
					Scores:               scores,
					Answers:              answers,
					Policy:               domain.AggregationPolicy{Method: "median"},
					MinValidScores:       2,
					ClientIdempotencyKey: "test-median-key",
				}
			},
			validateOutput: func(t *testing.T, output *domain.AggregateScoresOutput) {
				assert.Equal(t, TestAnswerUUID1, output.WinnerAnswerID) // First answer wins with median 0.5
				assert.Equal(t, domain.AggregationMethodMedian, output.Method)
				assert.Equal(t, 6, output.ValidScoreCount)
			},
		},
		{
			name: "trimmed_mean_removes_outliers",
			setupInput: func() domain.AggregateScoresInput {
				answers := CreateTestAnswers(1)
				scores := []domain.Score{
					CreateTestScore(answers[0].ID, WithScoreValue(0.0)), // Will be trimmed
					CreateTestScore(answers[0].ID, WithScoreValue(0.4)),
					CreateTestScore(answers[0].ID, WithScoreValue(0.5)),
					CreateTestScore(answers[0].ID, WithScoreValue(0.6)),
					CreateTestScore(answers[0].ID, WithScoreValue(1.0)), // Will be trimmed
				}
				return domain.AggregateScoresInput{
					Scores:  scores,
					Answers: answers,
					Policy: domain.AggregationPolicy{
						Method:       "trimmed_mean",
						TrimFraction: 0.2, // Trim 20% from each end
					},
					MinValidScores:       3,
					ClientIdempotencyKey: "test-trimmed-key",
				}
			},
			validateOutput: func(t *testing.T, output *domain.AggregateScoresOutput) {
				// Trimmed mean of [0.4, 0.5, 0.6] = 0.5
				AssertFloatEqual(t, 0.5, output.AggregateScore, "trimmed mean calculation")
				assert.Equal(t, domain.AggregationMethodTrimmedMean, output.Method)
			},
		},
		{
			name: "epsilon_tie_detection_and_breaking",
			setupInput: func() domain.AggregateScoresInput {
				answers := CreateTestAnswers(3)
				// Create scores that are within epsilon of each other
				scores := GenerateEpsilonTiedScores([]string{answers[0].ID, answers[1].ID, answers[2].ID}, 0.5)
				return domain.AggregateScoresInput{
					Scores:               scores,
					Answers:              answers,
					Policy:               domain.AggregationPolicy{Method: "mean"},
					MinValidScores:       1,
					ClientIdempotencyKey: "test-tie-key",
				}
			},
			validateOutput: func(t *testing.T, output *domain.AggregateScoresOutput) {
				// Verify tie detection works correctly
				allAnswerIDs := []string{TestAnswerUUID1, TestAnswerUUID2, TestAnswerUUID3}

				// Winner should be one of the three answers
				assert.Contains(t, allAnswerIDs, output.WinnerAnswerID, "winner should be one of the test answers")

				// Should have exactly 2 tied answers (the other two)
				assert.Len(t, output.TiedWithIDs, 2, "should have 2 tied answers")

				// Tied answers should be the other two answers, not the winner
				for _, tiedID := range output.TiedWithIDs {
					assert.Contains(t, allAnswerIDs, tiedID, "tied answer should be one of the test answers")
					assert.NotEqual(t, output.WinnerAnswerID, tiedID, "tied answer should not be the winner")
				}

				// All three answers should be accounted for (winner + tied)
				allAccountedIDs := append([]string{output.WinnerAnswerID}, output.TiedWithIDs...)
				assert.ElementsMatch(t, allAnswerIDs, allAccountedIDs, "all answers should be accounted for")
			},
			validateMetrics: func(t *testing.T, metrics *EnhancedMockMetricsRecorder) {
				counters := metrics.GetCounters()
				assert.Contains(t, counters, "tie_frequency:map[method:mean tenant:550e8400-e29b-41d4-a716-446655440000]")
			},
		},
		{
			name: "mixed_valid_invalid_scores",
			setupInput: func() domain.AggregateScoresInput {
				answers := CreateTestAnswers(2)
				scores := append(
					GenerateInvalidScores(answers[0].ID, 2, 1),    // 2 valid, 1 invalid
					GenerateInvalidScores(answers[1].ID, 3, 2)..., // 3 valid, 2 invalid
				)
				return domain.AggregateScoresInput{
					Scores:               scores,
					Answers:              answers,
					Policy:               domain.AggregationPolicy{Method: "mean"},
					MinValidScores:       3,
					ClientIdempotencyKey: "test-mixed-key",
				}
			},
			validateOutput: func(t *testing.T, output *domain.AggregateScoresOutput) {
				assert.Equal(t, 5, output.ValidScoreCount) // Only valid scores counted
				assert.Equal(t, 8, output.TotalScoreCount) // All scores included
			},
			validateMetrics: func(t *testing.T, metrics *EnhancedMockMetricsRecorder) {
				gauges := metrics.GetGauges()
				// 3 invalid out of 8 total = 0.375 invalid rate
				assert.InDelta(t, 0.375, gauges["invalid_score_rate:map[tenant:550e8400-e29b-41d4-a716-446655440000]"], epsilon)
			},
		},
		{
			name: "dimensional_score_aggregation",
			setupInput: func() domain.AggregateScoresInput {
				answers := CreateTestAnswers(1)
				dimensions := []domain.DimensionScore{
					{Name: "accuracy", Value: 0.8},
					{Name: "relevance", Value: 0.6},
					{Name: "clarity", Value: 0.7},
				}
				scores := []domain.Score{
					CreateTestScore(answers[0].ID, WithScoreDimensions(dimensions)),
				}
				return domain.AggregateScoresInput{
					Scores:               scores,
					Answers:              answers,
					Policy:               domain.AggregationPolicy{Method: "mean"},
					MinValidScores:       1,
					ClientIdempotencyKey: "test-dimensional-key",
				}
			},
			validateOutput: func(t *testing.T, output *domain.AggregateScoresOutput) {
				assert.NotNil(t, output.AggregateDimensions)
				assert.Contains(t, output.AggregateDimensions, domain.Dimension("accuracy"))
				assert.Contains(t, output.AggregateDimensions, domain.Dimension("relevance"))
				assert.Contains(t, output.AggregateDimensions, domain.Dimension("clarity"))
			},
		},
		{
			name: "insufficient_valid_scores_error",
			setupInput: func() domain.AggregateScoresInput {
				answers := CreateTestAnswers(1)
				scores := GenerateInvalidScores(answers[0].ID, 1, 3) // Only 1 valid, need 5
				return domain.AggregateScoresInput{
					Scores:               scores,
					Answers:              answers,
					Policy:               domain.AggregationPolicy{Method: "mean"},
					MinValidScores:       5,
					ClientIdempotencyKey: "test-insufficient-key",
				}
			},
			expectedError: "minimum valid scores not met",
		},
		{
			name: "invalid_aggregation_policy_error",
			setupInput: func() domain.AggregateScoresInput {
				input := CreateTestAggregateInput(2, 2, "invalid_method")
				return input
			},
			expectedError: "failed on the 'oneof' tag",
		},
		{
			name: "reasoning_validation_failure",
			setupInput: func() domain.AggregateScoresInput {
				answers := CreateTestAnswers(1)
				// Create score with both inline reasoning and ReasonRef (invalid)
				scores := []domain.Score{
					{
						ID:       uuid.New().String(),
						AnswerID: answers[0].ID,
						Value:    0.5,
						ScoreEvidence: domain.ScoreEvidence{
							InlineReasoning: "inline reasoning",
							ReasonRef: domain.ArtifactRef{
								Key:  "reason-ref",
								Kind: domain.ArtifactJudgeRationale,
							},
						},
						ScoreValidity: domain.ScoreValidity{Valid: true},
					},
				}
				return domain.AggregateScoresInput{
					Scores:               scores,
					Answers:              answers,
					Policy:               domain.AggregationPolicy{Method: "mean"},
					MinValidScores:       1,
					ClientIdempotencyKey: "test-reasoning-key",
				}
			},
			expectedError: "reasoning validation failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			sink := NewCapturingEventSink()
			metrics := NewEnhancedMockMetricsRecorder()
			activities := CreateTestActivities(sink, metrics)

			input := tt.setupInput()

			output, err := activities.AggregateScores(ctx, input)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, output)

			// Always validate basic output properties
			AssertAggregationCorrect(t, output, input)

			// Run specific validations
			if tt.validateOutput != nil {
				tt.validateOutput(t, output)
			}
			if tt.validateMetrics != nil {
				tt.validateMetrics(t, metrics)
			}
			if tt.validateEvents != nil {
				tt.validateEvents(t, sink)
			}
		})
	}
}

// TestEventEmissionIdempotency verifies that event emission is idempotent.
func TestEventEmissionIdempotency(t *testing.T) {
	ctx := context.Background()
	sink := NewCapturingEventSink()
	activities := CreateTestActivities(sink, nil)

	input := CreateTestAggregateInput(2, 2, "mean")

	// Run the same aggregation twice
	output1, err1 := activities.AggregateScores(ctx, input)
	require.NoError(t, err1)

	output2, err2 := activities.AggregateScores(ctx, input)
	require.NoError(t, err2)

	// Outputs should be identical
	assert.Equal(t, output1.WinnerAnswerID, output2.WinnerAnswerID)
	assert.Equal(t, output1.AggregateScore, output2.AggregateScore)

	// Events should be idempotent (only one emitted due to same idempotency key)
	events := sink.GetEventsByType("VerdictReached")
	assert.Len(t, events, 1, "only one event should be emitted due to idempotency")
}

// TestEventEmissionFailureResilience verifies event emission failures don't affect aggregation.
func TestEventEmissionFailureResilience(t *testing.T) {
	ctx := context.Background()
	failingSink := NewFailingEventSink(5) // Fail 5 times
	activities := CreateTestActivities(failingSink, nil)

	input := CreateTestAggregateInput(2, 2, "mean")

	// Aggregation should succeed despite event emission failures
	output, err := activities.AggregateScores(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, output)

	// Verify aggregation results are correct
	AssertAggregationCorrect(t, output, input)

	// No events should be captured due to failures
	events := failingSink.GetEvents()
	assert.Empty(t, events, "no events should be captured due to failures")
}

// TestMetricsFailureResilience verifies metrics failures don't affect aggregation.
func TestMetricsFailureResilience(t *testing.T) {
	ctx := context.Background()
	sink := NewCapturingEventSink()
	failingMetrics := NewEnhancedMockMetricsRecorder().WithFailures(10)
	activities := CreateTestActivities(sink, failingMetrics)

	input := CreateTestAggregateInput(2, 2, "mean")

	// Aggregation should succeed despite metrics failures
	output, err := activities.AggregateScores(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, output)

	// Verify aggregation results are correct
	AssertAggregationCorrect(t, output, input)

	// No metrics should be recorded due to failures
	counters := failingMetrics.GetCounters()
	assert.Empty(t, counters, "no metrics should be recorded due to failures")
}

// TestStatisticalAggregationProperties verifies mathematical properties of aggregation methods.
func TestStatisticalAggregationProperties(t *testing.T) {
	tests := []struct {
		name     string
		method   domain.AggregationMethod
		values   []float64
		expected float64
	}{
		{
			name:     "mean_property_check",
			method:   domain.AggregationMethodMean,
			values:   []float64{0.2, 0.4, 0.6, 0.8},
			expected: 0.5,
		},
		{
			name:     "median_odd_count",
			method:   domain.AggregationMethodMedian,
			values:   []float64{0.1, 0.5, 0.9},
			expected: 0.5,
		},
		{
			name:     "median_even_count",
			method:   domain.AggregationMethodMedian,
			values:   []float64{0.2, 0.4, 0.6, 0.8},
			expected: 0.5, // (0.4 + 0.6) / 2
		},
		{
			name:     "trimmed_mean_removes_extremes",
			method:   domain.AggregationMethodTrimmedMean,
			values:   []float64{0.0, 0.4, 0.5, 0.6, 1.0}, // Trim extremes
			expected: 0.5,                                // Mean of [0.4, 0.5, 0.6]
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			activities := CreateTestActivities(nil, nil)

			// Create scores with the test values
			answers := CreateTestAnswers(1)
			var scores []domain.Score
			for i, value := range tt.values {
				score := CreateTestScore(answers[0].ID, WithScoreValue(value))
				score.ID = fmt.Sprintf("score-%d", i) // Deterministic IDs
				scores = append(scores, score)
			}

			policy := domain.AggregationPolicy{Method: tt.method}
			if tt.method == domain.AggregationMethodTrimmedMean {
				policy.TrimFraction = 0.2 // 20% trim
			}

			input := domain.AggregateScoresInput{
				Scores:               scores,
				Answers:              answers,
				Policy:               policy,
				MinValidScores:       1,
				ClientIdempotencyKey: fmt.Sprintf("test-%s-key", tt.method.String()),
			}

			output, err := activities.AggregateScores(ctx, input)
			require.NoError(t, err)
			require.NotNil(t, output)

			AssertFloatEqual(t, tt.expected, output.AggregateScore,
				fmt.Sprintf("%s aggregation", tt.method))
		})
	}
}

// TestCostAggregation verifies that costs are properly aggregated from all scores.
func TestCostAggregation(t *testing.T) {
	ctx := context.Background()
	activities := CreateTestActivities(nil, nil)

	answers := CreateTestAnswers(2)
	scores := []domain.Score{
		CreateTestScore(answers[0].ID, WithScoreCost(100), WithScoreValidity(true, "")),
		CreateTestScore(answers[0].ID, WithScoreCost(150), WithScoreValidity(true, "")),
		CreateTestScore(answers[1].ID, WithScoreCost(75), WithScoreValidity(false, "error")), // Invalid but cost counted
		CreateTestScore(answers[1].ID, WithScoreCost(200), WithScoreValidity(true, "")),
	}

	input := domain.AggregateScoresInput{
		Scores:               scores,
		Answers:              answers,
		Policy:               domain.AggregationPolicy{Method: "mean"},
		MinValidScores:       2,
		ClientIdempotencyKey: "test-cost-key",
	}

	output, err := activities.AggregateScores(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, output)

	// Total cost should include ALL scores (valid and invalid)
	expectedCost := domain.Cents(100 + 150 + 75 + 200)
	assert.Equal(t, expectedCost, output.CostCents)
}

// TestTemporalErrorHandling verifies proper Temporal error handling.
func TestTemporalErrorHandling(t *testing.T) {
	ctx := context.Background()
	activities := CreateTestActivities(nil, nil)

	tests := []struct {
		name               string
		input              domain.AggregateScoresInput
		expectNonRetryable bool
		errorTag           string
	}{
		{
			name: "validation_error_non_retryable",
			input: domain.AggregateScoresInput{
				Scores:               []domain.Score{}, // Empty scores
				Answers:              CreateTestAnswers(1),
				Policy:               domain.AggregationPolicy{Method: "mean"},
				MinValidScores:       1,
				ClientIdempotencyKey: "invalid-key",
			},
			expectNonRetryable: true,
			errorTag:           "AggregateScores",
		},
		{
			name: "insufficient_scores_non_retryable",
			input: domain.AggregateScoresInput{
				Scores:               GenerateInvalidScores(TestAnswerUUID1, 0, 5), // All invalid
				Answers:              CreateTestAnswers(1),
				Policy:               domain.AggregationPolicy{Method: "mean"},
				MinValidScores:       3,
				ClientIdempotencyKey: "insufficient-key",
			},
			expectNonRetryable: true,
			errorTag:           "AggregateScores",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := activities.AggregateScores(ctx, tt.input)
			require.Error(t, err)

			// Check if it's a Temporal ApplicationError
			var appErr *temporal.ApplicationError
			require.ErrorAs(t, err, &appErr)

			if tt.expectNonRetryable {
				assert.True(t, appErr.NonRetryable(), "error should be non-retryable")
			}

			assert.Equal(t, tt.errorTag, appErr.Type(), "error tag should match")
		})
	}
}
