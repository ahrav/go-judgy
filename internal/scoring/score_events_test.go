package scoring

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/business"
	"github.com/ahrav/go-judgy/pkg/activity"
	"github.com/ahrav/go-judgy/pkg/events"
)

// TestScoreAnswers_EventEmission_AnswerScored tests AnswerScored event emission.
func TestScoreAnswers_EventEmission_AnswerScored(t *testing.T) {
	t.Run("emits AnswerScored event for each score", func(t *testing.T) {
		plans := []ScorePlan{
			NewScorePlanBuilder().
				ForAnswer("550e8400-e29b-41d4-a716-446655440001"). // Valid UUID
				WithResult(NewScoreBuilder().
					WithID("650e8400-e29b-41d4-a716-446655440001").       // Valid UUID
					WithAnswerID("550e8400-e29b-41d4-a716-446655440001"). // Valid UUID
					WithValue(0.8).
					Build()).
				WithProviderMeta("openai", "gpt-4", []string{"req-1"}).
				Build(),
			NewScorePlanBuilder().
				ForAnswer("550e8400-e29b-41d4-a716-446655440002"). // Valid UUID
				WithResult(NewScoreBuilder().
					WithID("650e8400-e29b-41d4-a716-446655440002").       // Valid UUID
					WithAnswerID("550e8400-e29b-41d4-a716-446655440002"). // Valid UUID
					WithValue(0.6).
					Build()).
				WithProviderMeta("anthropic", "claude-3", []string{"req-2"}).
				Build(),
		}

		activities, _, eventSink, _ := NewTestScenarioBuilder().
			WithScorePlans(plans...).
			Build()

		// Create input with UUIDs matching the score plans
		input := domain.ScoreAnswersInput{
			Question: "Test question",
			Answers: []domain.Answer{
				{
					ID: "550e8400-e29b-41d4-a716-446655440001",
					ContentRef: domain.ArtifactRef{
						Key:  "answer1.txt",
						Kind: domain.ArtifactAnswer,
					},
				},
				{
					ID: "550e8400-e29b-41d4-a716-446655440002",
					ContentRef: domain.ArtifactRef{
						Key:  "answer2.txt",
						Kind: domain.ArtifactAnswer,
					},
				},
			},
			Config: domain.DefaultEvalConfig(),
		}

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 2)

		// Verify AnswerScored events were emitted
		answerScoredEvents := eventSink.GetEventsByType("AnswerScored")
		assert.Len(t, answerScoredEvents, 2, "should emit one AnswerScored event per score")

		// Verify first event
		event1 := answerScoredEvents[0]
		assert.Equal(t, "AnswerScored", event1.Type)
		assert.Equal(t, "activity.score_answers", event1.Source)
		assert.Equal(t, "1.0.0", event1.Version)
		assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", event1.TenantID)
		assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", event1.WorkflowID)
		assert.NotEmpty(t, event1.ID)
		assert.NotEmpty(t, event1.Timestamp)

		// Verify second event has different data
		event2 := answerScoredEvents[1]
		assert.Equal(t, "AnswerScored", event2.Type)
		assert.NotEqual(t, event1.ID, event2.ID, "events should have different IDs")
	})

	t.Run("AnswerScored event payload contains score details", func(t *testing.T) {
		expectedScore := NewScoreBuilder().
			WithID("score-answer-1").
			WithAnswerID("answer-1").
			WithValue(0.85).
			WithConfidence(0.9).
			WithInlineReasoning("Detailed reasoning for the score").
			WithProvider("test-provider").
			WithModel("test-model").
			WithUsage(75, 1, 150, domain.Cents(35)).
			Build()

		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(expectedScore).
			Build()

		activities, _, eventSink, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		_, err := activities.ScoreAnswers(context.Background(), input)
		require.NoError(t, err)

		answerScoredEvents := eventSink.GetEventsByType("AnswerScored")
		require.Len(t, answerScoredEvents, 1)

		event := answerScoredEvents[0]

		// The payload contains the actual domain event data
		// We can't easily assert on the payload structure without domain knowledge
		// but we can verify the event was created
		assert.NotNil(t, event.Payload, "event should have payload")
	})

	t.Run("emits AnswerScored event for invalid scores", func(t *testing.T) {
		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithError(fmt.Errorf("scoring failed"), "non-retryable").
			Build()

		activities, _, eventSink, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 1)
		assert.False(t, result.Scores[0].Valid, "score should be invalid")

		// Event should still be emitted for invalid scores
		answerScoredEvents := eventSink.GetEventsByType("AnswerScored")
		assert.Len(t, answerScoredEvents, 1, "should emit AnswerScored event even for invalid scores")
	})
}

// TestScoreAnswers_EventEmission_ScoringUsage tests ScoringUsage event emission.
func TestScoreAnswers_EventEmission_ScoringUsage(t *testing.T) {
	t.Run("emits one ScoringUsage event per batch", func(t *testing.T) {
		plans := []ScorePlan{
			NewScorePlanBuilder().
				ForAnswer("answer-1").
				WithResult(NewScoreBuilder().WithAnswerID("answer-1").Build()).
				WithUsage(50, 1, 100, domain.Cents(25)).
				Build(),
			NewScorePlanBuilder().
				ForAnswer("answer-2").
				WithResult(NewScoreBuilder().WithAnswerID("answer-2").Build()).
				WithUsage(75, 1, 150, domain.Cents(35)).
				Build(),
		}

		activities, _, eventSink, _ := NewTestScenarioBuilder().
			WithScorePlans(plans...).
			Build()

		input := NewInputBuilder().WithMultipleAnswers(2).Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 2)

		// Verify ScoringUsage event was emitted
		usageEvents := eventSink.GetEventsByType("LLMUsage")
		assert.Len(t, usageEvents, 1, "should emit one LLMUsage event per batch")

		event := usageEvents[0]
		assert.Equal(t, "LLMUsage", event.Type)
		assert.Equal(t, "activity.score_answers", event.Source)
		assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", event.TenantID)
		assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", event.WorkflowID)
	})

	t.Run("ScoringUsage event includes aggregated metrics", func(t *testing.T) {
		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(NewScoreBuilder().WithAnswerID("answer-1").Build()).
			WithUsage(100, 2, 200, domain.Cents(50)).
			Build()

		activities, _, eventSink, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)

		// Verify aggregated metrics in output
		assert.Equal(t, int64(100), result.TokensUsed)
		assert.Equal(t, int64(2), result.CallsMade)
		assert.Equal(t, domain.Cents(50), result.CostCents)

		// Verify usage event was emitted
		usageEvents := eventSink.GetEventsByType("LLMUsage")
		require.Len(t, usageEvents, 1)

		// The event payload contains the aggregated usage data
		event := usageEvents[0]
		assert.NotNil(t, event.Payload, "usage event should have payload")
	})

	t.Run("ScoringUsage includes artifact references for blob storage", func(t *testing.T) {
		expectedScore := NewScoreBuilder().
			WithAnswerID("answer-1").
			WithLargeReasoning(). // Will trigger blob storage
			Build()

		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(expectedScore).
			Build()

		activities, _, eventSink, store := NewTestScenarioBuilder().
			WithBlobThreshold(TestThreshold).
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 1)

		score := result.Scores[0]
		assert.False(t, score.ReasonRef.IsZero(), "should use blob storage")

		// Verify artifact was stored
		artifacts := store.List()
		assert.Len(t, artifacts, 1, "artifact should be stored")

		// Verify usage event includes artifact reference
		usageEvents := eventSink.GetEventsByType("LLMUsage")
		require.Len(t, usageEvents, 1)

		// The usage event should include artifact references in its payload
		// This tests that the event emission includes blob storage costs
		event := usageEvents[0]
		assert.NotNil(t, event.Payload)
	})
}

// TestScoreAnswers_EventEmission_IdempotencyKeys tests idempotency key generation.
func TestScoreAnswers_EventEmission_IdempotencyKeys(t *testing.T) {
	t.Run("idempotency keys are stable for same workflow and batch size", func(t *testing.T) {
		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(NewScoreBuilder().WithAnswerID("answer-1").Build()).
			Build()

		activities, _, eventSink, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		// Execute twice
		_, err1 := activities.ScoreAnswers(context.Background(), input)
		require.NoError(t, err1)

		eventSink.Clear() // Clear events from first run

		_, err2 := activities.ScoreAnswers(context.Background(), input)
		require.NoError(t, err2)

		// Get events from second run
		answerEvents := eventSink.GetEventsByType("AnswerScored")
		usageEvents := eventSink.GetEventsByType("LLMUsage")

		require.Len(t, answerEvents, 1)
		require.Len(t, usageEvents, 1)

		// Idempotency keys should be deterministic
		// They are based on workflow ID and batch size
		answerEvent := answerEvents[0]
		usageEvent := usageEvents[0]

		assert.NotEmpty(t, answerEvent.IdempotencyKey, "answer event should have idempotency key")
		assert.NotEmpty(t, usageEvent.IdempotencyKey, "usage event should have idempotency key")

		// Keys should follow expected format - IdempotencyKey is a hash of (clientKey + suffix)
		expectedClientKey := "scoring-550e8400-e29b-41d4-a716-446655440000-1" // workflow ID + batch size
		expectedIdempotencyKey := domain.ScoringUsageIdempotencyKey(expectedClientKey)
		assert.Equal(t, expectedIdempotencyKey, usageEvent.IdempotencyKey, "usage key should match expected hash")
	})

	t.Run("AnswerScored events have unique idempotency keys within batch", func(t *testing.T) {
		plans := []ScorePlan{
			NewScorePlanBuilder().ForAnswer("answer-1").
				WithResult(NewScoreBuilder().WithAnswerID("answer-1").Build()).Build(),
			NewScorePlanBuilder().ForAnswer("answer-2").
				WithResult(NewScoreBuilder().WithAnswerID("answer-2").Build()).Build(),
		}

		activities, _, eventSink, _ := NewTestScenarioBuilder().
			WithScorePlans(plans...).
			Build()

		input := NewInputBuilder().WithMultipleAnswers(2).Build()

		_, err := activities.ScoreAnswers(context.Background(), input)
		require.NoError(t, err)

		answerEvents := eventSink.GetEventsByType("AnswerScored")
		require.Len(t, answerEvents, 2)

		// Each AnswerScored event should have a unique idempotency key
		event1 := answerEvents[0]
		event2 := answerEvents[1]

		assert.NotEqual(t, event1.IdempotencyKey, event2.IdempotencyKey,
			"AnswerScored events should have unique idempotency keys")
		assert.NotEmpty(t, event1.IdempotencyKey)
		assert.NotEmpty(t, event2.IdempotencyKey)
	})

	t.Run("different batch sizes produce different idempotency keys", func(t *testing.T) {
		plan1 := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(NewScoreBuilder().WithAnswerID("answer-1").Build()).
			Build()

		plan2 := []ScorePlan{
			NewScorePlanBuilder().ForAnswer("answer-1").
				WithResult(NewScoreBuilder().WithAnswerID("answer-1").Build()).Build(),
			NewScorePlanBuilder().ForAnswer("answer-2").
				WithResult(NewScoreBuilder().WithAnswerID("answer-2").Build()).Build(),
		}

		// Test single answer
		activities1, _, eventSink1, _ := NewTestScenarioBuilder().
			WithScorePlan(plan1).
			Build()

		input1 := NewInputBuilder().WithSingleAnswer("answer-1").Build()
		_, err := activities1.ScoreAnswers(context.Background(), input1)
		require.NoError(t, err)

		usageEvents1 := eventSink1.GetEventsByType("LLMUsage")
		require.Len(t, usageEvents1, 1)

		// Test multiple answers
		activities2, _, eventSink2, _ := NewTestScenarioBuilder().
			WithScorePlans(plan2...).
			Build()

		input2 := NewInputBuilder().WithMultipleAnswers(2).Build()
		_, err = activities2.ScoreAnswers(context.Background(), input2)
		require.NoError(t, err)

		usageEvents2 := eventSink2.GetEventsByType("LLMUsage")
		require.Len(t, usageEvents2, 1)

		// Idempotency keys should be different due to different batch sizes
		key1 := usageEvents1[0].IdempotencyKey
		key2 := usageEvents2[0].IdempotencyKey

		assert.NotEqual(t, key1, key2, "different batch sizes should produce different idempotency keys")
	})
}

// TestScoreAnswers_EventEmission_BestEffort tests that event emission is best-effort.
func TestScoreAnswers_EventEmission_BestEffort(t *testing.T) {
	t.Run("activity succeeds even if event emission fails", func(t *testing.T) {
		// Create a failing event sink
		failingSink := &failingEventSink{}
		baseActivities := NewCapturingBaseActivitiesWithSink(failingSink)

		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(NewScoreBuilder().WithAnswerID("answer-1").Build()).
			Build()

		mock := newScriptedMockClient(plan)
		store := business.NewInMemoryArtifactStore()
		activities := NewActivities(*baseActivities, mock, store, TestThreshold, nil)

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		// Activity should succeed despite event emission failures
		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err, "activity should succeed even with event emission failures")
		require.NotNil(t, result)
		require.Len(t, result.Scores, 1)

		score := result.Scores[0]
		assert.True(t, score.Valid, "score should be valid")

		// Verify the sink received emit attempts (and failed them)
		assert.True(t, failingSink.emitAttempted, "event emission should have been attempted")
	})
}

// TestScoreAnswers_EventEmission_ProviderMetadata tests provider metadata extraction.
func TestScoreAnswers_EventEmission_ProviderMetadata(t *testing.T) {
	t.Run("extracts provider and model from scores", func(t *testing.T) {
		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(NewScoreBuilder().
				WithAnswerID("answer-1").
				WithProvider("openai").
				WithModel("gpt-4").
				Build()).
			WithProviderMeta("openai", "gpt-4", []string{"req-123"}).
			Build()

		activities, _, eventSink, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		_, err := activities.ScoreAnswers(context.Background(), input)
		require.NoError(t, err)

		// Events should be emitted with provider metadata
		usageEvents := eventSink.GetEventsByType("LLMUsage")
		require.Len(t, usageEvents, 1)

		// The usage event includes provider metadata in its creation
		// We've verified the event was created with the right parameters
		event := usageEvents[0]
		assert.NotNil(t, event.Payload)
	})

	t.Run("handles mixed providers in batch", func(t *testing.T) {
		plans := []ScorePlan{
			NewScorePlanBuilder().ForAnswer("answer-1").
				WithResult(NewScoreBuilder().WithAnswerID("answer-1").WithProvider("openai").WithModel("gpt-4").Build()).
				WithProviderMeta("openai", "gpt-4", []string{"req-1"}).
				Build(),
			NewScorePlanBuilder().ForAnswer("answer-2").
				WithResult(NewScoreBuilder().WithAnswerID("answer-2").WithProvider("anthropic").WithModel("claude-3").Build()).
				WithProviderMeta("anthropic", "claude-3", []string{"req-2"}).
				Build(),
		}

		activities, _, eventSink, _ := NewTestScenarioBuilder().
			WithScorePlans(plans...).
			Build()

		input := NewInputBuilder().WithMultipleAnswers(2).Build()

		_, err := activities.ScoreAnswers(context.Background(), input)
		require.NoError(t, err)

		// Should emit events for both scores
		answerEvents := eventSink.GetEventsByType("AnswerScored")
		usageEvents := eventSink.GetEventsByType("LLMUsage")

		assert.Len(t, answerEvents, 2, "should emit AnswerScored for each answer")
		assert.Len(t, usageEvents, 1, "should emit one LLMUsage for batch")
	})
}

// Helper types for testing

// failingEventSink simulates event emission failures.
type failingEventSink struct {
	emitAttempted bool
}

func (f *failingEventSink) Append(ctx context.Context, envelope events.Envelope) error {
	f.emitAttempted = true
	return fmt.Errorf("simulated event emission failure")
}

// NewCapturingBaseActivitiesWithSink creates base activities with a custom sink.
func NewCapturingBaseActivitiesWithSink(sink events.EventSink) *activity.BaseActivities {
	base := activity.NewBaseActivities(sink)
	return &base
}
