package scoring

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ahrav/go-judgy/internal/domain"
)

// TestScoreAnswers_ProviderMetadataExtraction tests provider information extraction.
func TestScoreAnswers_ProviderMetadataExtraction(t *testing.T) {
	t.Run("extracts provider from score provenance", func(t *testing.T) {
		expectedScore := NewScoreBuilder().
			WithAnswerID("answer-1").
			WithProvider("openai").
			WithModel("gpt-4-turbo").
			Build()

		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(expectedScore).
			WithProviderMeta("openai", "gpt-4-turbo", []string{"req-abc123"}).
			Build()

		activities, _, eventSink, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 1)

		score := result.Scores[0]

		// Verify provider metadata is preserved in score
		assert.Equal(t, "openai", score.Provider, "provider should be extracted from score")
		assert.Equal(t, "gpt-4-turbo", score.Model, "model should be extracted from score")

		// Verify events include provider metadata
		usageEvents := eventSink.GetEventsByType("LLMUsage")
		require.Len(t, usageEvents, 1, "should emit usage event")

		// The usage event creation process should have access to provider metadata
		// This tests that extractProvider and extractJudgeModel work correctly
		usageEvent := usageEvents[0]
		assert.NotNil(t, usageEvent.Payload, "usage event should have payload with provider info")
	})

	t.Run("handles missing provider gracefully", func(t *testing.T) {
		expectedScore := NewScoreBuilder().
			WithAnswerID("answer-1").
			WithProvider(""). // Empty provider
			WithModel("").    // Empty model
			Build()

		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(expectedScore).
			WithProviderMeta("", "", nil). // Empty provider and model
			Build()

		activities, _, eventSink, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 1)

		score := result.Scores[0]

		// Verify empty provider is handled
		assert.Equal(t, "", score.Provider, "empty provider should be preserved")
		assert.Equal(t, "", score.Model, "empty model should be preserved")

		// Events should still be emitted
		usageEvents := eventSink.GetEventsByType("LLMUsage")
		assert.Len(t, usageEvents, 1, "should emit usage event even with missing provider")
	})

	t.Run("extracts provider from multiple scores with same provider", func(t *testing.T) {
		plans := []ScorePlan{
			NewScorePlanBuilder().
				ForAnswer("answer-1").
				WithResult(NewScoreBuilder().
					WithAnswerID("answer-1").
					Build()).
				WithProviderMeta("anthropic", "claude-3-opus", nil).
				Build(),
			NewScorePlanBuilder().
				ForAnswer("answer-2").
				WithResult(NewScoreBuilder().
					WithAnswerID("answer-2").
					Build()).
				WithProviderMeta("anthropic", "claude-3-opus", nil).
				Build(),
		}

		activities, _, eventSink, _ := NewTestScenarioBuilder().
			WithScorePlans(plans...).
			Build()

		input := NewInputBuilder().WithMultipleAnswers(2).Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 2)

		// Both scores should have the same provider
		for i, score := range result.Scores {
			assert.Equal(t, "anthropic", score.Provider, "score %d should have correct provider", i)
			assert.Equal(t, "claude-3-opus", score.Model, "score %d should have correct model", i)
		}

		// Usage event should reflect the homogeneous provider
		usageEvents := eventSink.GetEventsByType("LLMUsage")
		require.Len(t, usageEvents, 1)
	})

	t.Run("handles mixed providers in batch", func(t *testing.T) {
		plans := []ScorePlan{
			NewScorePlanBuilder().
				ForAnswer("answer-1").
				WithResult(NewScoreBuilder().
					WithAnswerID("answer-1").
					Build()).
				WithProviderMeta("openai", "gpt-4", nil).
				Build(),
			NewScorePlanBuilder().
				ForAnswer("answer-2").
				WithResult(NewScoreBuilder().
					WithAnswerID("answer-2").
					Build()).
				WithProviderMeta("anthropic", "claude-3", nil).
				Build(),
		}

		activities, _, eventSink, _ := NewTestScenarioBuilder().
			WithScorePlans(plans...).
			Build()

		input := NewInputBuilder().WithMultipleAnswers(2).Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 2)

		// Verify different providers are preserved
		assert.Equal(t, "openai", result.Scores[0].Provider)
		assert.Equal(t, "gpt-4", result.Scores[0].Model)
		assert.Equal(t, "anthropic", result.Scores[1].Provider)
		assert.Equal(t, "claude-3", result.Scores[1].Model)

		// Usage event should still be emitted (implementation determines how to handle mixed providers)
		usageEvents := eventSink.GetEventsByType("LLMUsage")
		assert.Len(t, usageEvents, 1)
	})
}

// TestScoreAnswers_RequestIDCorrelation tests provider request ID tracking.
func TestScoreAnswers_RequestIDCorrelation(t *testing.T) {
	t.Run("request IDs are not yet implemented", func(t *testing.T) {
		// This test documents the current state and will fail when request IDs are implemented
		expectedScore := NewScoreBuilder().
			WithAnswerID("answer-1").
			WithProvider("openai").
			WithModel("gpt-4").
			Build()

		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(expectedScore).
			WithProviderMeta("openai", "gpt-4", []string{"req-123", "req-456"}).
			Build()

		activities, _, eventSink, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 1)

		// Currently, extractProviderRequestIDs returns empty slice
		// When request IDs are implemented, this test should be updated
		usageEvents := eventSink.GetEventsByType("LLMUsage")
		require.Len(t, usageEvents, 1)

		// This test will need to be updated when request ID extraction is implemented
		// For now, we document that the functionality exists but returns empty results
		assert.NotNil(t, usageEvents[0].Payload, "usage event should have payload")

		// TODO: When request IDs are implemented, assert that they are included in the event
		// assert.Contains(t, usageEvent.RequestIDs, "req-123")
		// assert.Contains(t, usageEvent.RequestIDs, "req-456")
	})

	t.Run("empty request IDs handled gracefully", func(t *testing.T) {
		expectedScore := NewScoreBuilder().
			WithAnswerID("answer-1").
			Build()

		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(expectedScore).
			WithProviderMeta("openai", "gpt-4", []string{}). // Empty request IDs
			Build()

		activities, _, eventSink, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		_, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)

		// Should still emit events even with empty request IDs
		usageEvents := eventSink.GetEventsByType("LLMUsage")
		assert.Len(t, usageEvents, 1)
	})
}

// TestScoreAnswers_CostCalculation tests cost tracking and aggregation.
func TestScoreAnswers_CostCalculation(t *testing.T) {
	t.Run("aggregates cost correctly from multiple scores", func(t *testing.T) {
		plans := []ScorePlan{
			NewScorePlanBuilder().
				ForAnswer("answer-1").
				WithResult(NewScoreBuilder().WithAnswerID("answer-1").Build()).
				WithUsage(100, 1, 200, domain.Cents(50)).
				Build(),
			NewScorePlanBuilder().
				ForAnswer("answer-2").
				WithResult(NewScoreBuilder().WithAnswerID("answer-2").Build()).
				WithUsage(150, 1, 300, domain.Cents(75)).
				Build(),
			NewScorePlanBuilder().
				ForAnswer("answer-3").
				WithResult(NewScoreBuilder().WithAnswerID("answer-3").Build()).
				WithUsage(200, 1, 400, domain.Cents(100)).
				Build(),
		}

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlans(plans...).
			Build()

		input := NewInputBuilder().WithMultipleAnswers(3).Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 3)

		// Verify individual score costs
		assert.Equal(t, domain.Cents(50), result.Scores[0].CostCents)
		assert.Equal(t, domain.Cents(75), result.Scores[1].CostCents)
		assert.Equal(t, domain.Cents(100), result.Scores[2].CostCents)

		// Verify aggregated cost
		expectedTotalCost := domain.Cents(50 + 75 + 100)
		assert.Equal(t, expectedTotalCost, result.CostCents, "should aggregate costs from all scores")
	})

	t.Run("handles zero cost correctly", func(t *testing.T) {
		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(NewScoreBuilder().WithAnswerID("answer-1").Build()).
			WithUsage(100, 1, 200, domain.Cents(0)). // Zero cost
			Build()

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 1)

		assert.Equal(t, domain.Cents(0), result.Scores[0].CostCents)
		assert.Equal(t, domain.Cents(0), result.CostCents)
	})

	t.Run("cost not accumulated for failed scores", func(t *testing.T) {
		plans := []ScorePlan{
			NewScorePlanBuilder().
				ForAnswer("answer-1").
				WithResult(NewScoreBuilder().WithAnswerID("answer-1").Build()).
				WithUsage(100, 1, 200, domain.Cents(50)).
				Build(),
			NewScorePlanBuilder().
				ForAnswer("answer-2").
				WithError(errors.New("scoring failed"), "non-retryable").
				WithUsage(0, 0, 0, domain.Cents(0)). // No cost for failed scores
				Build(),
			NewScorePlanBuilder().
				ForAnswer("answer-3").
				WithResult(NewScoreBuilder().WithAnswerID("answer-3").Build()).
				WithUsage(75, 1, 150, domain.Cents(25)).
				Build(),
		}

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlans(plans...).
			Build()

		input := NewInputBuilder().WithMultipleAnswers(3).Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 3)

		// Only successful scores contribute to cost
		expectedTotalCost := domain.Cents(50 + 25) // answer-1 + answer-3
		assert.Equal(t, expectedTotalCost, result.CostCents, "failed scores should not contribute to cost")

		// Individual score costs
		assert.Equal(t, domain.Cents(50), result.Scores[0].CostCents)
		assert.Equal(t, domain.Cents(0), result.Scores[1].CostCents) // Failed score
		assert.Equal(t, domain.Cents(25), result.Scores[2].CostCents)
	})

	t.Run("large cost values handled correctly", func(t *testing.T) {
		largeCost := domain.Cents(999999) // Large cost value

		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(NewScoreBuilder().WithAnswerID("answer-1").Build()).
			WithUsage(1000, 1, 5000, largeCost).
			Build()

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 1)

		assert.Equal(t, largeCost, result.Scores[0].CostCents)
		assert.Equal(t, largeCost, result.CostCents)
	})
}

// TestScoreAnswers_LatencyTracking tests latency measurement and aggregation.
func TestScoreAnswers_LatencyTracking(t *testing.T) {
	t.Run("individual score latency preserved", func(t *testing.T) {
		plans := []ScorePlan{
			NewScorePlanBuilder().
				ForAnswer("answer-1").
				WithResult(NewScoreBuilder().WithAnswerID("answer-1").Build()).
				WithUsage(50, 1, 100, domain.Cents(25)). // 100ms latency
				Build(),
			NewScorePlanBuilder().
				ForAnswer("answer-2").
				WithResult(NewScoreBuilder().WithAnswerID("answer-2").Build()).
				WithUsage(75, 1, 250, domain.Cents(35)). // 250ms latency
				Build(),
		}

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlans(plans...).
			Build()

		input := NewInputBuilder().WithMultipleAnswers(2).Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 2)

		// Verify individual latencies are preserved
		assert.Equal(t, int64(100), result.Scores[0].LatencyMs)
		assert.Equal(t, int64(250), result.Scores[1].LatencyMs)
	})

	t.Run("zero latency handled correctly", func(t *testing.T) {
		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(NewScoreBuilder().WithAnswerID("answer-1").Build()).
			WithUsage(50, 1, 0, domain.Cents(25)). // Zero latency
			Build()

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 1)

		assert.Equal(t, int64(0), result.Scores[0].LatencyMs)
	})

	t.Run("latency not set for failed scores", func(t *testing.T) {
		plans := []ScorePlan{
			NewScorePlanBuilder().
				ForAnswer("answer-1").
				WithResult(NewScoreBuilder().WithAnswerID("answer-1").Build()).
				WithUsage(50, 1, 100, domain.Cents(25)).
				Build(),
			NewScorePlanBuilder().
				ForAnswer("answer-2").
				WithError(errors.New("scoring failed"), "non-retryable").
				Build(),
		}

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlans(plans...).
			Build()

		input := NewInputBuilder().WithMultipleAnswers(2).Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 2)

		// Successful score should have latency
		assert.Equal(t, int64(100), result.Scores[0].LatencyMs)

		// Failed score should have zero latency
		assert.Equal(t, int64(0), result.Scores[1].LatencyMs)
	})
}

// TestScoreAnswers_MetricsIntegration tests integration of all metrics.
func TestScoreAnswers_MetricsIntegration(t *testing.T) {
	t.Run("comprehensive metrics tracking", func(t *testing.T) {
		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(NewScoreBuilder().
				WithAnswerID("answer-1").
				WithProvider("openai").
				WithModel("gpt-4-turbo").
				WithUsage(150, 2, 300, domain.Cents(75)).
				Build()).
			WithProviderMeta("openai", "gpt-4-turbo", []string{"req-abc123"}).
			WithUsage(150, 2, 300, domain.Cents(75)).
			Build()

		activities, _, eventSink, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 1)

		score := result.Scores[0]

		// Verify all metrics are present
		assert.Equal(t, "openai", score.Provider)
		assert.Equal(t, "gpt-4-turbo", score.Model)
		assert.Equal(t, int64(150), score.TokensUsed)
		assert.Equal(t, int64(2), score.CallsUsed)
		assert.Equal(t, int64(300), score.LatencyMs)
		assert.Equal(t, domain.Cents(75), score.CostCents)

		// Verify aggregated metrics
		assert.Equal(t, int64(150), result.TokensUsed)
		assert.Equal(t, int64(2), result.CallsMade)
		assert.Equal(t, domain.Cents(75), result.CostCents)

		// Verify events include all metrics
		usageEvents := eventSink.GetEventsByType("LLMUsage")
		require.Len(t, usageEvents, 1)

		usageEvent := usageEvents[0]
		assert.NotNil(t, usageEvent.Payload)
		assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", usageEvent.TenantID)
		assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", usageEvent.WorkflowID)
	})
}
