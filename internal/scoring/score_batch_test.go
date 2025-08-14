package scoring

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ahrav/go-judgy/internal/domain"
	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
)

// TestScoreAnswers_BatchProcessing_MixedSuccessFailure tests batching with mixed outcomes.
func TestScoreAnswers_BatchProcessing_MixedSuccessFailure(t *testing.T) {
	t.Run("three answers: success, retryable error, non-retryable error", func(t *testing.T) {
		plans := []ScorePlan{
			// Answer 1: Success
			NewScorePlanBuilder().
				ForAnswer("answer-1").
				WithResult(NewScoreBuilder().
					WithID("score-1").
					WithAnswerID("answer-1").
					WithValue(0.8).
					WithUsage(50, 1, 100, domain.Cents(25)).
					Build()).
				Build(),
			// Answer 2: Retryable error (transport failure)
			NewScorePlanBuilder().
				ForAnswer("answer-2").
				WithError(errors.New("transport timeout"), "retryable").
				Build(),
			// Answer 3: Non-retryable error (schema violation)
			NewScorePlanBuilder().
				ForAnswer("answer-3").
				WithError(errors.New("invalid schema"), "non-retryable").
				Build(),
		}

		activities, _, eventSink, _ := NewTestScenarioBuilder().
			WithScorePlans(plans...).
			Build()

		input := NewInputBuilder().WithMultipleAnswers(3).Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		// Activity should succeed - partial failures are handled gracefully
		require.NoError(t, err, "activity should succeed with partial failures")
		require.NotNil(t, result)
		require.Len(t, result.Scores, 3, "should return scores for all answers")

		// Verify order preservation (using deterministic UUIDs)
		scores := result.Scores
		assert.Equal(t, "3815d28f-98b3-54ab-b3f1-8248eae1ef4b", scores[0].AnswerID, "first score should be for answer-1")
		assert.Equal(t, "7a9f57d3-f6f5-5ad7-b169-4685fdf0107b", scores[1].AnswerID, "second score should be for answer-2")
		assert.Equal(t, "9af43a66-e984-57cb-9d22-d5c2e9d34d00", scores[2].AnswerID, "third score should be for answer-3")

		// Verify score validity flags
		assert.True(t, scores[0].Valid, "first score should be valid")
		assert.False(t, scores[1].Valid, "second score should be invalid (retryable error)")
		assert.False(t, scores[2].Valid, "third score should be invalid (non-retryable error)")

		// Verify error details
		assert.Empty(t, scores[0].Error, "valid score should have no error")
		assert.Contains(t, scores[1].Error, "Request timeout", "should contain retryable error")
		assert.Contains(t, scores[2].Error, "invalid schema", "should contain non-retryable error")

		// Verify aggregated metrics only include successful scores
		assert.Equal(t, int64(50), result.TokensUsed, "tokens from successful scores only")
		assert.Equal(t, int64(1), result.CallsMade, "calls from successful scores only")
		assert.Equal(t, domain.Cents(25), result.CostCents, "cost from successful scores only")

		// Verify events are emitted for all scores (including invalid ones)
		answerEvents := eventSink.GetEventsByType("AnswerScored")
		assert.Len(t, answerEvents, 3, "should emit AnswerScored for all answers")

		usageEvents := eventSink.GetEventsByType("LLMUsage")
		assert.Len(t, usageEvents, 1, "should emit one LLMUsage for the batch")
	})

	t.Run("all failures returns empty metrics but valid structure", func(t *testing.T) {
		plans := []ScorePlan{
			NewScorePlanBuilder().
				ForAnswer("answer-1").
				WithError(errors.New("failure 1"), "non-retryable").
				Build(),
			NewScorePlanBuilder().
				ForAnswer("answer-2").
				WithError(errors.New("failure 2"), "retryable").
				Build(),
		}

		activities, _, eventSink, _ := NewTestScenarioBuilder().
			WithScorePlans(plans...).
			Build()

		input := NewInputBuilder().WithMultipleAnswers(2).Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err, "activity should succeed even with all failures")
		require.NotNil(t, result)
		require.Len(t, result.Scores, 2, "should return invalid scores for all answers")

		// All scores should be invalid
		for i, score := range result.Scores {
			assert.False(t, score.Valid, "score %d should be invalid", i)
			assert.NotEmpty(t, score.Error, "score %d should have error message", i)
		}

		// Metrics should be zero
		assert.Equal(t, int64(0), result.TokensUsed, "no tokens used for failed scores")
		assert.Equal(t, int64(0), result.CallsMade, "no calls made for failed scores")
		assert.Equal(t, domain.Cents(0), result.CostCents, "no cost for failed scores")

		// Events should still be emitted
		answerEvents := eventSink.GetEventsByType("AnswerScored")
		assert.Len(t, answerEvents, 2, "should emit events for failed scores")

		usageEvents := eventSink.GetEventsByType("LLMUsage")
		assert.Len(t, usageEvents, 1, "should emit usage event even for all failures")
	})

	t.Run("all successes aggregates correctly", func(t *testing.T) {
		plans := []ScorePlan{
			NewScorePlanBuilder().
				ForAnswer("answer-1").
				WithResult(NewScoreBuilder().WithAnswerID("answer-1").Build()).
				WithUsage(30, 1, 80, domain.Cents(15)).
				Build(),
			NewScorePlanBuilder().
				ForAnswer("answer-2").
				WithResult(NewScoreBuilder().WithAnswerID("answer-2").Build()).
				WithUsage(45, 1, 120, domain.Cents(20)).
				Build(),
			NewScorePlanBuilder().
				ForAnswer("answer-3").
				WithResult(NewScoreBuilder().WithAnswerID("answer-3").Build()).
				WithUsage(60, 1, 150, domain.Cents(30)).
				Build(),
		}

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlans(plans...).
			Build()

		input := NewInputBuilder().WithMultipleAnswers(3).Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 3)

		// All scores should be valid
		for i, score := range result.Scores {
			assert.True(t, score.Valid, "score %d should be valid", i)
			assert.Empty(t, score.Error, "score %d should have no error", i)
		}

		// Verify aggregated metrics
		assert.Equal(t, int64(135), result.TokensUsed, "tokens: 30+45+60")
		assert.Equal(t, int64(3), result.CallsMade, "calls: 1+1+1")
		assert.Equal(t, domain.Cents(65), result.CostCents, "cost: 15+20+30")
	})
}

// TestScoreAnswers_BatchProcessing_ErrorClassification tests proper error classification.
func TestScoreAnswers_BatchProcessing_ErrorClassification(t *testing.T) {
	t.Run("retryable errors create invalid scores", func(t *testing.T) {
		retryableError := &llmerrors.WorkflowError{
			Type:      llmerrors.ErrorTypeRateLimit,
			Message:   "rate limit",
			Code:      "RATE_LIMIT",
			Retryable: true,
			Cause:     errors.New("too many requests"),
		}

		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithError(retryableError, "retryable").
			Build()

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err, "activity should not fail for retryable LLM errors")
		require.Len(t, result.Scores, 1)

		score := result.Scores[0]
		assert.False(t, score.Valid, "score should be invalid")
		assert.Contains(t, score.Error, "Rate limit exceeded", "error should contain retryable error details")

		// Invalid score structure verification
		assert.Equal(t, 0.0, score.Value, "invalid score should have zero value")
		assert.Equal(t, 0.0, score.Confidence, "invalid score should have zero confidence")
		assert.Empty(t, score.InlineReasoning, "invalid score should have no reasoning")
		assert.True(t, score.ReasonRef.IsZero(), "invalid score should have no blob reference")
	})

	t.Run("transport errors create invalid scores", func(t *testing.T) {
		transportError := &llmerrors.WorkflowError{
			Type:      llmerrors.ErrorTypeNetwork,
			Message:   "connection failed",
			Code:      "TRANSPORT_ERROR",
			Retryable: true,
			Cause:     errors.New("network timeout"),
		}

		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithError(transportError, "transport").
			Build()

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err, "activity should not fail for transport errors")
		require.Len(t, result.Scores, 1)

		score := result.Scores[0]
		assert.False(t, score.Valid, "score should be invalid")
		assert.Contains(t, score.Error, "Network error", "error should contain transport error details")
	})

	t.Run("non-retryable errors create invalid scores", func(t *testing.T) {
		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithError(errors.New("validation failed"), "non-retryable").
			Build()

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err, "activity should not fail for non-retryable errors")
		require.Len(t, result.Scores, 1)

		score := result.Scores[0]
		assert.False(t, score.Valid, "score should be invalid")
		assert.Contains(t, score.Error, "validation failed", "error should contain non-retryable error details")
	})
}

// TestScoreAnswers_BatchProcessing_OrderPreservation tests that answer order is preserved.
func TestScoreAnswers_BatchProcessing_OrderPreservation(t *testing.T) {
	t.Run("preserves order with mixed success and failure", func(t *testing.T) {
		// Create answers with specific IDs to test order preservation
		plans := []ScorePlan{
			NewScorePlanBuilder().
				ForAnswer("answer-first").
				WithResult(NewScoreBuilder().
					WithID("score-first").
					WithAnswerID("answer-first").
					WithValue(0.1).
					Build()).
				Build(),
			NewScorePlanBuilder().
				ForAnswer("answer-second").
				WithError(errors.New("middle failure"), "non-retryable").
				Build(),
			NewScorePlanBuilder().
				ForAnswer("answer-third").
				WithResult(NewScoreBuilder().
					WithID("score-third").
					WithAnswerID("answer-third").
					WithValue(0.9).
					Build()).
				Build(),
		}

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlans(plans...).
			Build()

		// Create input with specific answer order (convert to UUIDs to match plans)
		input := domain.ScoreAnswersInput{
			Question: "Test question",
			Answers: []domain.Answer{
				{ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("answer-first")).String(), ContentRef: domain.ArtifactRef{Key: "first.txt", Kind: domain.ArtifactAnswer}},
				{ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("answer-second")).String(), ContentRef: domain.ArtifactRef{Key: "second.txt", Kind: domain.ArtifactAnswer}},
				{ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("answer-third")).String(), ContentRef: domain.ArtifactRef{Key: "third.txt", Kind: domain.ArtifactAnswer}},
			},
			Config: domain.DefaultEvalConfig(),
		}

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 3)

		// Verify order is preserved
		scores := result.Scores
		firstID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("answer-first")).String()
		secondID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("answer-second")).String()
		thirdID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("answer-third")).String()
		assert.Equal(t, firstID, scores[0].AnswerID, "first position should be answer-first")
		assert.Equal(t, secondID, scores[1].AnswerID, "second position should be answer-second")
		assert.Equal(t, thirdID, scores[2].AnswerID, "third position should be answer-third")

		// Verify values match expected pattern
		assert.Equal(t, 0.1, scores[0].Value, "first score should have value 0.1")
		assert.False(t, scores[1].Valid, "second score should be invalid")
		assert.Equal(t, 0.9, scores[2].Value, "third score should have value 0.9")
	})

	t.Run("preserves order with large batch", func(t *testing.T) {
		// Create 10 answers to test order preservation at scale
		var plans []ScorePlan
		for i := 0; i < 10; i++ {
			answerID := fmt.Sprintf("answer-%d", i+1) // Match WithMultipleAnswers pattern (answer-1, answer-2, etc.)
			scoreValue := float64(i) / 10.0           // 0.0, 0.1, 0.2, ... 0.9

			plan := NewScorePlanBuilder().
				ForAnswer(answerID).
				WithResult(NewScoreBuilder().
					WithAnswerID(answerID).
					WithValue(scoreValue).
					Build()).
				Build()
			plans = append(plans, plan)
		}

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlans(plans...).
			Build()

		input := NewInputBuilder().WithMultipleAnswers(10).Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 10)

		// Verify order and values
		for i, score := range result.Scores {
			// expected IDs come from WithMultipleAnswers deterministic UUIDs
			expectedAnswerID := input.Answers[i].ID
			expectedValue := float64(i) / 10.0

			assert.Equal(t, expectedAnswerID, score.AnswerID, "answer %d should be in position %d", i+1, i)
			assert.Equal(t, expectedValue, score.Value, "score %d should have correct value", i)
		}
	})
}

// TestScoreAnswers_BatchProcessing_ResourceAggregation tests resource usage aggregation.
func TestScoreAnswers_BatchProcessing_ResourceAggregation(t *testing.T) {
	t.Run("aggregates tokens calls and cost correctly", func(t *testing.T) {
		plans := []ScorePlan{
			NewScorePlanBuilder().
				ForAnswer("answer-1").
				WithResult(NewScoreBuilder().WithAnswerID("answer-1").Build()).
				WithUsage(100, 2, 200, domain.Cents(50)).
				Build(),
			NewScorePlanBuilder().
				ForAnswer("answer-2").
				WithResult(NewScoreBuilder().WithAnswerID("answer-2").Build()).
				WithUsage(150, 3, 300, domain.Cents(75)).
				Build(),
			NewScorePlanBuilder().
				ForAnswer("answer-3").
				WithError(errors.New("failed"), "non-retryable"). // No usage for failed scores
				Build(),
			NewScorePlanBuilder().
				ForAnswer("answer-4").
				WithResult(NewScoreBuilder().WithAnswerID("answer-4").Build()).
				WithUsage(200, 1, 100, domain.Cents(25)).
				Build(),
		}

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlans(plans...).
			Build()

		input := NewInputBuilder().WithMultipleAnswers(4).Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 4)

		// Verify aggregation (only successful scores contribute)
		expectedTokens := int64(100 + 150 + 200)   // answer-1 + answer-2 + answer-4
		expectedCalls := int64(2 + 3 + 1)          // answer-1 + answer-2 + answer-4
		expectedCost := domain.Cents(50 + 75 + 25) // answer-1 + answer-2 + answer-4

		assert.Equal(t, expectedTokens, result.TokensUsed, "should aggregate tokens from successful scores")
		assert.Equal(t, expectedCalls, result.CallsMade, "should aggregate calls from successful scores")
		assert.Equal(t, expectedCost, result.CostCents, "should aggregate cost from successful scores")

		// Verify individual score usage is preserved
		assert.Equal(t, int64(100), result.Scores[0].TokensUsed, "individual score usage should be preserved")
		assert.Equal(t, int64(150), result.Scores[1].TokensUsed, "individual score usage should be preserved")
		assert.Equal(t, int64(0), result.Scores[2].TokensUsed, "failed score should have zero usage")
		assert.Equal(t, int64(200), result.Scores[3].TokensUsed, "individual score usage should be preserved")
	})

	t.Run("handles zero usage correctly", func(t *testing.T) {
		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(NewScoreBuilder().WithAnswerID("answer-1").Build()).
			WithUsage(0, 0, 0, domain.Cents(0)). // Zero usage
			Build()

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 1)

		// Zero usage should be handled correctly
		assert.Equal(t, int64(0), result.TokensUsed)
		assert.Equal(t, int64(0), result.CallsMade)
		assert.Equal(t, domain.Cents(0), result.CostCents)

		score := result.Scores[0]
		assert.True(t, score.Valid, "score should be valid even with zero usage")
		assert.Equal(t, int64(0), score.TokensUsed)
	})
}

// TestScoreAnswers_BatchProcessing_BlobStorageMixed tests blob storage in batches.
func TestScoreAnswers_BatchProcessing_BlobStorageMixed(t *testing.T) {
	t.Run("mixed inline and blob storage in batch", func(t *testing.T) {
		plans := []ScorePlan{
			NewScorePlanBuilder().
				ForAnswer("answer-1").
				WithResult(NewScoreBuilder().
					WithAnswerID("answer-1").
					WithSmallReasoning(). // Inline
					Build()).
				Build(),
			NewScorePlanBuilder().
				ForAnswer("answer-2").
				WithResult(NewScoreBuilder().
					WithAnswerID("answer-2").
					WithLargeReasoning(). // Blob
					Build()).
				Build(),
			NewScorePlanBuilder().
				ForAnswer("answer-3").
				WithError(errors.New("failed"), "non-retryable"). // No reasoning
				Build(),
		}

		activities, _, _, store := NewTestScenarioBuilder().
			WithBlobThreshold(TestThreshold).
			WithScorePlans(plans...).
			Build()

		input := NewInputBuilder().WithMultipleAnswers(3).Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 3)

		// Verify storage decisions
		score1 := result.Scores[0]
		assert.NotEmpty(t, score1.InlineReasoning, "small reasoning should be inline")
		assert.True(t, score1.ReasonRef.IsZero(), "should not have blob reference")

		score2 := result.Scores[1]
		assert.Empty(t, score2.InlineReasoning, "large reasoning should be in blob")
		assert.False(t, score2.ReasonRef.IsZero(), "should have blob reference")

		score3 := result.Scores[2]
		assert.Empty(t, score3.InlineReasoning, "failed score should have no reasoning")
		assert.True(t, score3.ReasonRef.IsZero(), "failed score should have no blob reference")

		// Verify artifact store contains only the large reasoning
		artifacts := store.List()
		assert.Len(t, artifacts, 1, "only one artifact should be stored")
	})
}
