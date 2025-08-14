package scoring

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ahrav/go-judgy/internal/domain"
)

// TestScoreAnswers_OutputValidation tests that ScoreAnswersOutput.Validate() passes for all scenarios.
func TestScoreAnswers_OutputValidation(t *testing.T) {
	t.Run("valid scores pass output validation", func(t *testing.T) {
		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(NewScoreBuilder().
				WithID("550e8400-e29b-41d4-a716-446655440000").
				WithAnswerID("550e8400-e29b-41d4-a716-446655440001").
				WithValue(0.8).
				WithConfidence(0.9).
				WithInlineReasoning("Valid reasoning").
				Build()).
			WithUsage(50, 1, 100, domain.Cents(25)).
			Build()

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.NotNil(t, result)

		// The activity should ensure output validation passes
		assert.NoError(t, result.Validate(), "ScoreAnswersOutput.Validate() should pass for valid scores")

		// Verify the score itself is valid
		require.Len(t, result.Scores, 1)
		score := result.Scores[0]
		assert.True(t, score.Valid)
		assert.NoError(t, score.Validate(), "individual score should pass validation")
	})

	t.Run("invalid scores still create valid output structure", func(t *testing.T) {
		// Test the critical gap: when scoring fails, the activity synthesizes
		// an invalid Score, but the overall ScoreAnswersOutput should still pass validation
		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithError(errors.New("LLM scoring failed"), "non-retryable").
			Build()

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err, "activity should succeed even with LLM failures")
		require.NotNil(t, result)

		// This is the key test: output validation should pass even with invalid scores
		assert.NoError(t, result.Validate(), "ScoreAnswersOutput.Validate() should pass even with invalid scores")

		// Verify the invalid score structure is compliant
		require.Len(t, result.Scores, 1)
		score := result.Scores[0]
		assert.False(t, score.Valid, "score should be marked as invalid")
		assert.NotEmpty(t, score.Error, "invalid score should have error message")

		// The invalid score itself should still pass domain validation
		assert.NoError(t, score.Validate(), "even invalid scores should pass structural validation")

		// Verify the synthesized score has valid structure
		assert.NotEmpty(t, score.ID, "invalid score should have ID")
		expectedAnswerID := input.Answers[0].ID
		assert.Equal(t, expectedAnswerID, score.AnswerID, "invalid score should have correct answer ID")
		assert.Equal(t, 0.0, score.Value, "invalid score should have zero value")
		assert.Equal(t, 0.0, score.Confidence, "invalid score should have zero confidence")
	})

	t.Run("mixed valid and invalid scores pass output validation", func(t *testing.T) {
		plans := []ScorePlan{
			NewScorePlanBuilder().
				ForAnswer("answer-1").
				WithResult(NewScoreBuilder().
					WithID("550e8400-e29b-41d4-a716-446655440000").
					WithAnswerID("answer-1").
					WithValue(0.8).
					Build()).
				Build(),
			NewScorePlanBuilder().
				ForAnswer("answer-2").
				WithError(errors.New("scoring failed"), "non-retryable").
				Build(),
			NewScorePlanBuilder().
				ForAnswer("answer-3").
				WithResult(NewScoreBuilder().
					WithID("550e8400-e29b-41d4-a716-446655440002").
					WithAnswerID("answer-3").
					WithValue(0.6).
					Build()).
				Build(),
		}

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlans(plans...).
			Build()

		input := NewInputBuilder().WithMultipleAnswers(3).Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.NotNil(t, result)

		// Output validation should pass with mixed results
		assert.NoError(t, result.Validate(), "output should be valid with mixed score validity")

		require.Len(t, result.Scores, 3)

		// All scores should pass individual validation
		for i, score := range result.Scores {
			assert.NoError(t, score.Validate(), "score %d should pass validation", i)
		}

		// Verify validity flags are correct
		assert.True(t, result.Scores[0].Valid, "first score should be valid")
		assert.False(t, result.Scores[1].Valid, "second score should be invalid")
		assert.True(t, result.Scores[2].Valid, "third score should be valid")
	})
}

// TestScoreAnswers_UUIDInvariants tests UUID format requirements.
func TestScoreAnswers_UUIDInvariants(t *testing.T) {
	t.Run("valid scores have UUID format IDs", func(t *testing.T) {
		plan := NewScorePlanBuilder().
			ForAnswer("550e8400-e29b-41d4-a716-446655440001").
			WithResult(NewScoreBuilder().
				WithID("550e8400-e29b-41d4-a716-446655440000").
				WithAnswerID("550e8400-e29b-41d4-a716-446655440001").
				Build()).
			Build()

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := domain.ScoreAnswersInput{
			Question: "Test question",
			Answers: []domain.Answer{
				{
					ID: "550e8400-e29b-41d4-a716-446655440001",
					ContentRef: domain.ArtifactRef{
						Key:  "test-answer.txt",
						Kind: domain.ArtifactAnswer,
					},
				},
			},
			Config: domain.DefaultEvalConfig(),
		}

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 1)

		score := result.Scores[0]

		// Verify UUID format (basic check for UUID-like structure)
		assert.Len(t, score.ID, 36, "score ID should be UUID length")
		assert.Contains(t, score.ID, "-", "score ID should contain UUID hyphens")

		assert.Len(t, score.AnswerID, 36, "answer ID should be UUID length")
		assert.Contains(t, score.AnswerID, "-", "answer ID should contain UUID hyphens")

		// Score should pass validation with proper UUIDs
		assert.NoError(t, score.Validate(), "score with UUID IDs should pass validation")
	})

	t.Run("invalid scores get generated UUID format IDs", func(t *testing.T) {
		plan := NewScorePlanBuilder().
			ForAnswer("550e8400-e29b-41d4-a716-446655440001").
			WithError(errors.New("scoring failed"), "non-retryable").
			Build()

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := domain.ScoreAnswersInput{
			Question: "Test question",
			Answers: []domain.Answer{
				{
					ID: "550e8400-e29b-41d4-a716-446655440001",
					ContentRef: domain.ArtifactRef{
						Key:  "test-answer.txt",
						Kind: domain.ArtifactAnswer,
					},
				},
			},
			Config: domain.DefaultEvalConfig(),
		}

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 1)

		score := result.Scores[0]

		// Even invalid scores should have proper ID format
		assert.NotEmpty(t, score.ID, "invalid score should have generated ID")
		assert.Equal(t, "550e8400-e29b-41d4-a716-446655440001", score.AnswerID, "answer ID should be preserved")

		// Invalid score should still pass structural validation
		assert.NoError(t, score.Validate(), "invalid score should pass structural validation")
		assert.False(t, score.Valid, "score should be marked as invalid")
	})
}

// TestScoreAnswers_ReasoningInvariants tests the one-of reasoning invariant.
func TestScoreAnswers_ReasoningInvariants(t *testing.T) {
	t.Run("exactly one of inline reasoning or blob reference", func(t *testing.T) {
		plans := []ScorePlan{
			// Small reasoning - should be inline
			NewScorePlanBuilder().
				ForAnswer("answer-1").
				WithResult(NewScoreBuilder().
					WithAnswerID("answer-1").
					WithSmallReasoning().
					Build()).
				Build(),
			// Large reasoning - should be blob
			NewScorePlanBuilder().
				ForAnswer("answer-2").
				WithResult(NewScoreBuilder().
					WithAnswerID("answer-2").
					WithLargeReasoning().
					Build()).
				Build(),
		}

		activities, _, _, _ := NewTestScenarioBuilder().
			WithBlobThreshold(TestThreshold).
			WithScorePlans(plans...).
			Build()

		input := NewInputBuilder().WithMultipleAnswers(2).Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 2)

		// Verify one-of invariant for both scores
		for i, score := range result.Scores {
			hasInline := score.InlineReasoning != ""
			hasBlob := !score.ReasonRef.IsZero()

			assert.True(t, hasInline != hasBlob,
				"score %d should have exactly one of inline reasoning or blob ref (inline=%t, blob=%t)",
				i, hasInline, hasBlob)

			// Score should pass validation with proper reasoning structure
			assert.NoError(t, score.Validate(), "score %d should pass validation", i)
		}

		// First score should be inline, second should be blob
		assert.NotEmpty(t, result.Scores[0].InlineReasoning, "small reasoning should be inline")
		assert.True(t, result.Scores[0].ReasonRef.IsZero(), "small reasoning should not have blob ref")

		assert.Empty(t, result.Scores[1].InlineReasoning, "large reasoning should not be inline")
		assert.False(t, result.Scores[1].ReasonRef.IsZero(), "large reasoning should have blob ref")
	})

	t.Run("invalid scores have neither inline nor blob reasoning", func(t *testing.T) {
		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithError(errors.New("scoring failed"), "non-retryable").
			Build()

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 1)

		score := result.Scores[0]

		// Invalid scores should have no reasoning
		assert.Empty(t, score.InlineReasoning, "invalid score should have no inline reasoning")
		assert.True(t, score.ReasonRef.IsZero(), "invalid score should have no blob reference")

		// But should still pass validation
		assert.NoError(t, score.Validate(), "invalid score should pass structural validation")
		assert.False(t, score.Valid, "score should be marked as invalid")
	})
}

// TestScoreAnswers_ArtifactKindInvariants tests artifact kind consistency.
func TestScoreAnswers_ArtifactKindInvariants(t *testing.T) {
	t.Run("blob reasoning has correct artifact kind", func(t *testing.T) {
		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(NewScoreBuilder().
				WithAnswerID("answer-1").
				WithLargeReasoning().
				Build()).
			Build()

		activities, _, _, store := NewTestScenarioBuilder().
			WithBlobThreshold(TestThreshold).
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 1)

		score := result.Scores[0]

		// Verify blob reference has correct kind
		assert.False(t, score.ReasonRef.IsZero(), "should have blob reference")
		assert.Equal(t, domain.ArtifactJudgeRationale, score.ReasonRef.Kind,
			"blob reference should have correct artifact kind")

		// Verify artifact was stored with correct content
		artifacts := store.List()
		require.Len(t, artifacts, 1)

		storedContent, err := store.Get(context.Background(), score.ReasonRef)
		require.NoError(t, err)
		assert.NotEmpty(t, storedContent, "stored rationale should not be empty")

		// Score should pass validation
		assert.NoError(t, score.Validate(), "score with blob reference should pass validation")
	})
}

// TestScoreAnswers_MetricsConsistency tests that metrics are consistent across the output.
func TestScoreAnswers_MetricsConsistency(t *testing.T) {
	t.Run("aggregated metrics equal sum of individual scores", func(t *testing.T) {
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
				WithError(errors.New("failed"), "non-retryable"). // Should not contribute to totals
				Build(),
		}

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlans(plans...).
			Build()

		input := NewInputBuilder().WithMultipleAnswers(3).Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 3)

		// Calculate expected totals (only from valid scores)
		var expectedTokens, expectedCalls int64
		var expectedCost domain.Cents

		for _, score := range result.Scores {
			if score.Valid {
				expectedTokens += score.TokensUsed
				expectedCalls += score.CallsUsed
				expectedCost += score.CostCents
			}
		}

		// Verify aggregated metrics match individual scores
		assert.Equal(t, expectedTokens, result.TokensUsed, "aggregated tokens should equal sum of valid scores")
		assert.Equal(t, expectedCalls, result.CallsMade, "aggregated calls should equal sum of valid scores")
		assert.Equal(t, expectedCost, result.CostCents, "aggregated cost should equal sum of valid scores")

		// Expected values based on our test data
		assert.Equal(t, int64(250), result.TokensUsed, "tokens: 100+150")
		assert.Equal(t, int64(5), result.CallsMade, "calls: 2+3")
		assert.Equal(t, domain.Cents(125), result.CostCents, "cost: 50+75")

		// Output should pass validation
		assert.NoError(t, result.Validate(), "output with consistent metrics should pass validation")
	})

	t.Run("zero metrics handled correctly", func(t *testing.T) {
		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithError(errors.New("all failed"), "non-retryable").
			Build()

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 1)

		// All metrics should be zero with no valid scores
		assert.Equal(t, int64(0), result.TokensUsed)
		assert.Equal(t, int64(0), result.CallsMade)
		assert.Equal(t, domain.Cents(0), result.CostCents)

		// Output should still pass validation
		assert.NoError(t, result.Validate(), "output with zero metrics should pass validation")
	})
}

// TestScoreAnswers_StructuralInvariants tests overall structural requirements.
func TestScoreAnswers_StructuralInvariants(t *testing.T) {
	t.Run("output always has same number of scores as input answers", func(t *testing.T) {
		// Test with mixed success/failure to ensure 1:1 mapping is maintained
		plans := []ScorePlan{
			NewScorePlanBuilder().ForAnswer("answer-1").
				WithResult(NewScoreBuilder().WithAnswerID("answer-1").Build()).Build(),
			NewScorePlanBuilder().ForAnswer("answer-2").
				WithError(errors.New("failed"), "non-retryable").Build(),
			NewScorePlanBuilder().ForAnswer("answer-3").
				WithResult(NewScoreBuilder().WithAnswerID("answer-3").Build()).Build(),
			NewScorePlanBuilder().ForAnswer("answer-4").
				WithError(errors.New("failed"), "retryable").Build(),
			NewScorePlanBuilder().ForAnswer("answer-5").
				WithResult(NewScoreBuilder().WithAnswerID("answer-5").Build()).Build(),
		}

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlans(plans...).
			Build()

		input := NewInputBuilder().WithMultipleAnswers(5).Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.NotNil(t, result)

		// Must maintain 1:1 mapping
		assert.Len(t, result.Scores, 5, "output must have same number of scores as input answers")

		// Verify answer ID mapping is preserved
		for i, score := range result.Scores {
			expectedAnswerID := input.Answers[i].ID
			assert.Equal(t, expectedAnswerID, score.AnswerID,
				"score %d should correspond to correct answer", i)
		}

		// Output should pass validation regardless of individual score validity
		assert.NoError(t, result.Validate(), "output should pass validation with 1:1 mapping maintained")
	})

	t.Run("empty input produces valid empty output", func(t *testing.T) {
		activities, _, _, _ := NewTestScenarioBuilder().Build()

		// Create input with no answers
		input := domain.ScoreAnswersInput{
			Question: "Test question",
			Answers:  []domain.Answer{}, // Empty answers
			Config:   domain.DefaultEvalConfig(),
		}

		result, err := activities.ScoreAnswers(context.Background(), input)

		// This should fail during input validation
		require.Error(t, err, "empty answers should fail input validation")
		assert.Nil(t, result, "should not return result for invalid input")
	})
}
