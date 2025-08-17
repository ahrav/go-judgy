//nolint:testpackage // Tests need access to unexported functions like nonRetryable
package aggregation

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/pkg/activity"
	"github.com/ahrav/go-judgy/pkg/events"
)

// mockEventSink implements events.EventSink for testing.
type mockEventSink struct {
	events []events.Envelope
}

func (m *mockEventSink) Append(_ context.Context, envelope events.Envelope) error {
	m.events = append(m.events, envelope)
	return nil
}

// TestAggregateScores_Mean tests mean aggregation method.
func TestAggregateScores_Mean(t *testing.T) {
	t.Run("calculates mean correctly", func(t *testing.T) {
		// Create activities with mock event sink
		mockSink := &mockEventSink{}
		base := activity.NewBaseActivities(mockSink)
		activities := NewActivities(base)

		ctx := context.Background()
		input := createAggregateScoresInput(domain.AggregationMethodMean, 0.0, 3)
		// Match answer IDs from input.Answers
		input.Scores = []domain.Score{
			createValidScore("score1", input.Answers[0].ID, 0.8),
			createValidScore("score2", input.Answers[1].ID, 0.6),
			createValidScore("score3", input.Answers[2].ID, 0.9),
		}

		result, err := activities.AggregateScores(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, result)

		// Expected mean: (0.8 + 0.6 + 0.9) / 3 = 0.7666...
		assert.InDelta(t, 0.7666, result.AggregateScore, 0.001)
		assert.Equal(t, domain.AggregationMethodMean, result.Method)
		assert.Equal(t, 3, result.ValidScoreCount)
		assert.Equal(t, 3, result.TotalScoreCount)
		assert.Equal(t, input.Answers[2].ID, result.WinnerAnswerID) // Highest score
	})

	t.Run("handles invalid scores", func(t *testing.T) {
		mockSink := &mockEventSink{}
		base := activity.NewBaseActivities(mockSink)
		activities := NewActivities(base)

		ctx := context.Background()
		input := createAggregateScoresInput(domain.AggregationMethodMean, 0.0, 4)
		input.Scores = []domain.Score{
			createValidScore("score1", input.Answers[0].ID, 0.8),
			createInvalidScore("score2", input.Answers[1].ID),
			createValidScore("score3", input.Answers[2].ID, 0.6),
			createInvalidScore("score4", input.Answers[3].ID),
		}

		result, err := activities.AggregateScores(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, result)

		// Expected mean: (0.8 + 0.6) / 2 = 0.7
		assert.Equal(t, 0.7, result.AggregateScore)
		assert.Equal(t, 2, result.ValidScoreCount)
		assert.Equal(t, 4, result.TotalScoreCount)
	})
}

// TestAggregateScores_Median tests median aggregation method.
func TestAggregateScores_Median(t *testing.T) {
	t.Run("odd number of scores", func(t *testing.T) {
		mockSink := &mockEventSink{}
		base := activity.NewBaseActivities(mockSink)
		activities := NewActivities(base)

		ctx := context.Background()
		input := createAggregateScoresInput(domain.AggregationMethodMedian, 0.0, 3)
		input.Scores = []domain.Score{
			createValidScore("score1", input.Answers[0].ID, 0.9),
			createValidScore("score2", input.Answers[1].ID, 0.5),
			createValidScore("score3", input.Answers[2].ID, 0.7),
		}

		result, err := activities.AggregateScores(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, result)

		// Sorted: [0.5, 0.7, 0.9], median = 0.7
		assert.Equal(t, 0.7, result.AggregateScore)
		assert.Equal(t, domain.AggregationMethodMedian, result.Method)
	})

	t.Run("even number of scores", func(t *testing.T) {
		mockSink := &mockEventSink{}
		base := activity.NewBaseActivities(mockSink)
		activities := NewActivities(base)

		ctx := context.Background()
		input := createAggregateScoresInput("median", 0.0, 4)
		input.Scores = []domain.Score{
			createValidScore("score1", input.Answers[0].ID, 0.9),
			createValidScore("score2", input.Answers[1].ID, 0.5),
			createValidScore("score3", input.Answers[2].ID, 0.7),
			createValidScore("score4", input.Answers[3].ID, 0.3),
		}

		result, err := activities.AggregateScores(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, result)

		// Sorted: [0.3, 0.5, 0.7, 0.9], median = (0.5 + 0.7) / 2 = 0.6
		assert.Equal(t, 0.6, result.AggregateScore)
		assert.Equal(t, domain.AggregationMethodMedian, result.Method)
	})
}

// TestAggregateScores_TrimmedMean tests trimmed mean aggregation method.
func TestAggregateScores_TrimmedMean(t *testing.T) {
	t.Run("10% trim", func(t *testing.T) {
		mockSink := &mockEventSink{}
		base := activity.NewBaseActivities(mockSink)
		activities := NewActivities(base)

		ctx := context.Background()
		input := createAggregateScoresInput(domain.AggregationMethodTrimmedMean, 0.1, 10)
		// Create 10 scores: [0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0]
		input.Scores = []domain.Score{}
		for i := 0; i < 10; i++ {
			score := createValidScore(
				uuid.New().String(),
				input.Answers[i].ID,
				float64(i+1)/10.0,
			)
			input.Scores = append(input.Scores, score)
		}

		result, err := activities.AggregateScores(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, result)

		// 10% trim from each end: remove 0.1 and 1.0
		// Mean of [0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9] = 0.55
		assert.InDelta(t, 0.55, result.AggregateScore, 0.001)
		assert.Equal(t, domain.AggregationMethodTrimmedMean, result.Method)
	})

	t.Run("20% trim", func(t *testing.T) {
		mockSink := &mockEventSink{}
		base := activity.NewBaseActivities(mockSink)
		activities := NewActivities(base)

		ctx := context.Background()
		input := createAggregateScoresInput(domain.AggregationMethodTrimmedMean, 0.2, 5)
		scores := []float64{0.1, 0.3, 0.5, 0.7, 0.9}
		for i, val := range scores {
			score := createValidScore(
				uuid.New().String(),
				uuid.New().String(),
				val,
			)
			input.Scores = append(input.Scores, score)
			if i < len(input.Answers) {
				input.Answers[i].ID = score.AnswerID
			} else {
				input.Answers = append(input.Answers, domain.Answer{
					ID: score.AnswerID,
					ContentRef: domain.ArtifactRef{
						Key:  "test",
						Size: 100,
						Kind: domain.ArtifactAnswer,
					},
				})
			}
		}

		result, err := activities.AggregateScores(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, result)

		// 20% trim from 5 scores = 1 from each end
		// Mean of [0.3, 0.5, 0.7] = 0.5
		assert.Equal(t, 0.5, result.AggregateScore)
	})
}

// TestAggregateScores_TieBreaking tests deterministic tie-breaking.
func TestAggregateScores_TieBreaking(t *testing.T) {
	t.Run("lexicographic tie-breaking", func(t *testing.T) {
		mockSink := &mockEventSink{}
		base := activity.NewBaseActivities(mockSink)
		activities := NewActivities(base)

		ctx := context.Background()
		input := createAggregateScoresInput(domain.AggregationMethodMean, 0.0, 3)

		// Create three answers with IDs that will sort deterministically
		// Use valid UUIDs that sort lexicographically
		answer1ID := "b0000000-0000-4000-8000-000000000001"
		answer2ID := "a0000000-0000-4000-8000-000000000002"
		answer3ID := "c0000000-0000-4000-8000-000000000003"

		input.Scores = []domain.Score{
			createValidScore("score1", answer1ID, 0.8),
			createValidScore("score2", answer2ID, 0.8), // Same score
			createValidScore("score3", answer3ID, 0.8), // Same score
		}
		input.Answers = []domain.Answer{
			{
				ID:         answer1ID,
				ContentRef: domain.ArtifactRef{Key: "test1", Size: 100, Kind: domain.ArtifactAnswer},
				AnswerProvenance: domain.AnswerProvenance{
					Provider:    "test-provider",
					Model:       "test-model",
					GeneratedAt: time.Now(),
				},
			},
			{
				ID:         answer2ID,
				ContentRef: domain.ArtifactRef{Key: "test2", Size: 100, Kind: domain.ArtifactAnswer},
				AnswerProvenance: domain.AnswerProvenance{
					Provider:    "test-provider",
					Model:       "test-model",
					GeneratedAt: time.Now(),
				},
			},
			{
				ID:         answer3ID,
				ContentRef: domain.ArtifactRef{Key: "test3", Size: 100, Kind: domain.ArtifactAnswer},
				AnswerProvenance: domain.AnswerProvenance{
					Provider:    "test-provider",
					Model:       "test-model",
					GeneratedAt: time.Now(),
				},
			},
		}

		result, err := activities.AggregateScores(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, result)

		// answer2ID should win (starts with 'a')
		assert.Equal(t, answer2ID, result.WinnerAnswerID)
		// The other two are tied
		assert.Len(t, result.TiedWithIDs, 2)
		assert.Contains(t, result.TiedWithIDs, answer1ID)
		assert.Contains(t, result.TiedWithIDs, answer3ID)
	})
}

// TestAggregateScores_EdgeCases tests edge cases.
func TestAggregateScores_EdgeCases(t *testing.T) {
	t.Run("all invalid scores", func(t *testing.T) {
		mockSink := &mockEventSink{}
		base := activity.NewBaseActivities(mockSink)
		activities := NewActivities(base)

		ctx := context.Background()
		input := createAggregateScoresInput(domain.AggregationMethodMean, 0.0, 2)
		input.MinValidScores = 1
		input.Scores = []domain.Score{
			createInvalidScore("score1", input.Answers[0].ID),
			createInvalidScore("score2", input.Answers[1].ID),
		}

		_, err := activities.AggregateScores(ctx, input)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "insufficient valid scores")
	})

	t.Run("single valid score", func(t *testing.T) {
		mockSink := &mockEventSink{}
		base := activity.NewBaseActivities(mockSink)
		activities := NewActivities(base)

		ctx := context.Background()
		input := createAggregateScoresInput(domain.AggregationMethodMean, 0.0, 1)
		input.Scores = []domain.Score{
			createValidScore("score1", input.Answers[0].ID, 0.75),
		}

		result, err := activities.AggregateScores(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, result)

		assert.Equal(t, 0.75, result.AggregateScore)
		assert.Equal(t, input.Answers[0].ID, result.WinnerAnswerID)
		assert.Equal(t, 1, result.ValidScoreCount)
	})

	t.Run("minimum valid scores not met", func(t *testing.T) {
		mockSink := &mockEventSink{}
		base := activity.NewBaseActivities(mockSink)
		activities := NewActivities(base)

		ctx := context.Background()
		input := createAggregateScoresInput(domain.AggregationMethodMean, 0.0, 3)
		input.MinValidScores = 3
		input.Scores = []domain.Score{
			createValidScore("score1", input.Answers[0].ID, 0.8),
			createInvalidScore("score2", input.Answers[1].ID),
			createValidScore("score3", input.Answers[2].ID, 0.6),
		}

		_, err := activities.AggregateScores(ctx, input)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "insufficient valid scores: got 2, need 3")
	})
}

// TestAggregateScores_MixedScoreFormats tests handling of InlineReasoning and ReasonRef.
func TestAggregateScores_MixedScoreFormats(t *testing.T) {
	t.Run("mixed inline and ref reasoning", func(t *testing.T) {
		mockSink := &mockEventSink{}
		base := activity.NewBaseActivities(mockSink)
		activities := NewActivities(base)

		ctx := context.Background()
		input := createAggregateScoresInput(domain.AggregationMethodMean, 0.0, 2)

		// Create scores with mixed reasoning formats
		score1 := createValidScore("score1", input.Answers[0].ID, 0.8)
		// score1 already has InlineReasoning set

		score2 := createValidScore("score2", input.Answers[1].ID, 0.7)
		// Replace InlineReasoning with ReasonRef for score2
		score2.InlineReasoning = ""
		score2.ReasonRef = domain.ArtifactRef{
			Key:  "reasoning/score2.txt",
			Size: 5000,
			Kind: domain.ArtifactJudgeRationale,
		}

		input.Scores = []domain.Score{score1, score2}

		result, err := activities.AggregateScores(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, result)

		// Should handle both formats correctly
		assert.Equal(t, 0.75, result.AggregateScore) // (0.8 + 0.7) / 2
		assert.Equal(t, 2, result.ValidScoreCount)
	})
}

// TestAggregateScores_CostAggregation tests cost aggregation from all scores.
func TestAggregateScores_CostAggregation(t *testing.T) {
	t.Run("aggregates costs from all scores including invalid", func(t *testing.T) {
		mockSink := &mockEventSink{}
		base := activity.NewBaseActivities(mockSink)
		activities := NewActivities(base)

		ctx := context.Background()
		input := createAggregateScoresInput(domain.AggregationMethodMean, 0.0, 3)

		score1 := createValidScore("score1", input.Answers[0].ID, 0.8)
		score1.CostCents = 100

		score2 := createInvalidScore("score2", input.Answers[1].ID)
		score2.CostCents = 50 // Invalid score still has cost

		score3 := createValidScore("score3", input.Answers[2].ID, 0.6)
		score3.CostCents = 75

		input.Scores = []domain.Score{score1, score2, score3}

		result, err := activities.AggregateScores(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, result)

		// Total cost should include all scores
		assert.Equal(t, domain.Cents(225), result.CostCents)
		assert.Equal(t, 2, result.ValidScoreCount)
		assert.Equal(t, 3, result.TotalScoreCount)
	})
}

// TestAggregateScores_EventEmission tests event emission.
func TestAggregateScores_EventEmission(t *testing.T) {
	t.Run("emits VerdictReached event", func(t *testing.T) {
		mockSink := &mockEventSink{}
		base := activity.NewBaseActivities(mockSink)
		activities := NewActivities(base)

		ctx := context.Background()
		input := createAggregateScoresInput(domain.AggregationMethodMean, 0.0, 2)
		input.ClientIdempotencyKey = "test-idem-key"

		score1 := createValidScore("score1", input.Answers[0].ID, 0.8)
		// Clear InlineReasoning and set ReasonRef
		score1.InlineReasoning = ""
		score1.ReasonRef = domain.ArtifactRef{
			Key:  "reasoning/score1.txt",
			Size: 1000,
			Kind: domain.ArtifactJudgeRationale,
		}

		score2 := createValidScore("score2", input.Answers[1].ID, 0.6)

		input.Scores = []domain.Score{score1, score2}

		_, err := activities.AggregateScores(ctx, input)
		require.NoError(t, err)

		// Check that event was emitted
		assert.Len(t, mockSink.events, 1)
		event := mockSink.events[0]

		// Check event properties
		assert.Equal(t, "VerdictReached", event.Type)
		assert.NotEmpty(t, event.IdempotencyKey)
		assert.NotNil(t, event.Payload)
	})
}

// TestEventSinkIntegration tests that the event sink receives events properly.
func TestEventSinkIntegration(t *testing.T) {
	t.Run("base activities emit events correctly", func(t *testing.T) {
		mockSink := &mockEventSink{}
		base := activity.NewBaseActivities(mockSink)

		ctx := context.Background()
		testEnvelope := events.Envelope{
			Type:           "TestEvent",
			IdempotencyKey: "test-key",
		}

		base.EmitEventSafe(ctx, testEnvelope, "TestEvent")

		// Check that event was captured
		assert.Len(t, mockSink.events, 1)
		assert.Equal(t, "TestEvent", mockSink.events[0].Type)
	})

	t.Run("activities embedded base works", func(t *testing.T) {
		mockSink := &mockEventSink{}
		base := activity.NewBaseActivities(mockSink)
		activities := NewActivities(base)

		ctx := context.Background()
		testEnvelope := events.Envelope{
			Type:           "TestEvent",
			IdempotencyKey: "test-key",
		}

		// Call EmitEventSafe on the Activities struct through embedded BaseActivities
		activities.EmitEventSafe(ctx, testEnvelope, "TestEvent")

		// Check that event was captured
		assert.Len(t, mockSink.events, 1)
		assert.Equal(t, "TestEvent", mockSink.events[0].Type)
	})
}

// TestAggregateScores_Determinism tests that aggregation is deterministic.
func TestAggregateScores_Determinism(t *testing.T) {
	t.Run("same input produces same output", func(t *testing.T) {
		mockSink := &mockEventSink{}
		base := activity.NewBaseActivities(mockSink)
		activities := NewActivities(base)

		ctx := context.Background()
		input := createAggregateScoresInput(domain.AggregationMethodMean, 0.0, 3)
		input.Scores = []domain.Score{
			createValidScore("score1", "answer1", 0.8),
			createValidScore("score2", "answer2", 0.6),
			createValidScore("score3", "answer3", 0.9),
		}

		// Run multiple times
		result1, err1 := activities.AggregateScores(ctx, input)
		result2, err2 := activities.AggregateScores(ctx, input)
		result3, err3 := activities.AggregateScores(ctx, input)

		require.NoError(t, err1)
		require.NoError(t, err2)
		require.NoError(t, err3)

		// All results should be identical
		assert.Equal(t, result1.AggregateScore, result2.AggregateScore)
		assert.Equal(t, result2.AggregateScore, result3.AggregateScore)
		assert.Equal(t, result1.WinnerAnswerID, result2.WinnerAnswerID)
		assert.Equal(t, result2.WinnerAnswerID, result3.WinnerAnswerID)
	})
}

// Helper functions

func createAggregateScoresInput(method domain.AggregationMethod, trimFraction float64, numAnswers int) domain.AggregateScoresInput {
	answers := make([]domain.Answer, numAnswers)
	for i := 0; i < numAnswers; i++ {
		answerID := uuid.New().String()
		answers[i] = domain.Answer{
			ID: answerID,
			ContentRef: domain.ArtifactRef{
				Key:  "answers/test.txt",
				Size: 100,
				Kind: domain.ArtifactAnswer,
			},
			AnswerProvenance: domain.AnswerProvenance{
				Provider:    "test-provider",
				Model:       "test-model",
				GeneratedAt: time.Now(),
			},
		}
	}

	return domain.AggregateScoresInput{
		Scores:  []domain.Score{},
		Answers: answers,
		Policy: domain.AggregationPolicy{
			Method:       method,
			TrimFraction: trimFraction,
		},
		MinValidScores:       1,
		ClientIdempotencyKey: uuid.New().String(),
	}
}

func createValidScore(id, answerID string, value float64) domain.Score {
	return domain.Score{
		ID:       id,
		AnswerID: answerID,
		Value:    value,
		ScoreEvidence: domain.ScoreEvidence{
			InlineReasoning: "test reasoning for score",
		},
		ScoreProvenance: domain.ScoreProvenance{
			JudgeID:  "test-judge",
			Provider: "test-provider",
			Model:    "test-model",
		},
		ScoreValidity: domain.ScoreValidity{
			Valid: true,
			Error: "",
		},
	}
}

func createInvalidScore(id, answerID string) domain.Score {
	return domain.Score{
		ID:            id,
		AnswerID:      answerID,
		Value:         0.0,
		ScoreEvidence: domain.ScoreEvidence{
			// Invalid scores don't require reasoning
		},
		ScoreProvenance: domain.ScoreProvenance{
			JudgeID:  "test-judge",
			Provider: "test-provider",
			Model:    "test-model",
		},
		ScoreValidity: domain.ScoreValidity{
			Valid: false,
			Error: "score validation failed",
		},
	}
}
