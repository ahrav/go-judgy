package scoring

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ahrav/go-judgy/internal/domain"
)

// TestScoreAnswers_ValidatorRepairOnce_SuccessPath tests the happy path for schema repair.
// Verifies that the validator is injected and repair is attempted exactly once.
func TestScoreAnswers_ValidatorRepairOnce_SuccessPath(t *testing.T) {
	t.Run("code fence repair succeeds", func(t *testing.T) {
		// Create a score that would be valid after repair
		expectedScore := NewScoreBuilder().
			WithID("score-answer-1").
			WithAnswerID("answer-1").
			WithValue(0.8).
			WithConfidence(0.9).
			WithInlineReasoning("This is a good answer with clear explanation").
			Build()

		// Create plan with raw JSON that needs code fence repair
		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(expectedScore).
			WithRawJSON(JSONWithCodeFence). // Will trigger repair logic
			Build()

		// Build test scenario
		activities, mock, _, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		// Create input
		input := NewInputBuilder().
			WithSingleAnswer("answer-1").
			Build()

		// Execute
		result, err := activities.ScoreAnswers(context.Background(), input)

		// Verify success
		require.NoError(t, err, "activity should succeed after repair")
		require.NotNil(t, result)
		require.Len(t, result.Scores, 1)

		score := result.Scores[0]
		assert.True(t, score.Valid, "score should be valid after repair")
		assert.Equal(t, 0.8, score.Value)
		assert.Equal(t, "This is a good answer with clear explanation", score.InlineReasoning)

		// Verify validator was injected
		validator := mock.GetLastValidator()
		assert.NotNil(t, validator, "validator should be injected into client")

		// Verify repair policy was set
		repairPolicy := mock.GetLastRepairPolicy()
		require.NotNil(t, repairPolicy)
		assert.Equal(t, 1, repairPolicy.MaxAttempts, "should use one-shot repair policy")
		assert.True(t, repairPolicy.AllowTransportRepairs, "should allow transport repairs")
	})

	t.Run("trailing comma repair succeeds", func(t *testing.T) {
		expectedScore := NewScoreBuilder().
			WithID("score-answer-1").
			WithAnswerID("answer-1").
			WithValue(0.8).
			WithInlineReasoning("This answer needs trailing comma repair").
			Build()

		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(expectedScore).
			WithRawJSON(JSONWithTrailingComma).
			Build()

		activities, mock, _, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 1)
		assert.True(t, result.Scores[0].Valid)

		// Verify validator injection
		assert.NotNil(t, mock.GetLastValidator())
	})

	t.Run("unquoted keys repair succeeds", func(t *testing.T) {
		expectedScore := NewScoreBuilder().
			WithID("score-answer-1").
			WithAnswerID("answer-1").
			WithValue(0.8).
			WithInlineReasoning("This answer has unquoted keys").
			Build()

		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(expectedScore).
			WithRawJSON(JSONWithUnquotedKeys).
			Build()

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 1)
		assert.True(t, result.Scores[0].Valid)
	})

	t.Run("single quotes repair succeeds", func(t *testing.T) {
		expectedScore := NewScoreBuilder().
			WithID("score-answer-1").
			WithAnswerID("answer-1").
			WithValue(0.8).
			WithInlineReasoning("This answer uses single quotes").
			Build()

		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(expectedScore).
			WithRawJSON(JSONWithSingleQuotes).
			Build()

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 1)
		assert.True(t, result.Scores[0].Valid)
		// Validator injection test removed - covered in dedicated test
	})
}

// TestScoreAnswers_ValidatorRepairOnce_FailurePath tests the failure path for schema repair.
// Verifies that non-retryable errors are returned when repair fails.
func TestScoreAnswers_ValidatorRepairOnce_FailurePath(t *testing.T) {
	t.Run("irreparable JSON returns non-retryable error", func(t *testing.T) {
		// Create plan with JSON that cannot be repaired
		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithError(errors.New("JSON still invalid after repair"), "non-retryable").
			WithRawJSON(InvalidJSONNotRepairable).
			Build()

		activities, mock, _, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		// Activity should succeed but create invalid score
		require.NoError(t, err, "activity should handle individual failures gracefully")
		require.NotNil(t, result)
		require.Len(t, result.Scores, 1)

		score := result.Scores[0]
		assert.False(t, score.Valid, "score should be invalid")
		assert.Contains(t, score.Error, "JSON still invalid after repair")

		// Verify validator was still injected
		assert.NotNil(t, mock.GetLastValidator())
	})

	t.Run("business rule violations after repair", func(t *testing.T) {
		// Create plan that simulates business rule violation after successful JSON repair
		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithError(errors.New("score value 1.5 outside valid range [0,1]"), "non-retryable").
			WithRawJSON(JSONViolatingBusinessRules).
			Build()

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 1)

		score := result.Scores[0]
		assert.False(t, score.Valid)
		assert.Contains(t, score.Error, "score value 1.5 outside valid range")
	})

	t.Run("no repair applied for already invalid JSON", func(t *testing.T) {
		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithError(errors.New("JSON still invalid after repair"), "non-retryable").
			WithRawJSON("completely broken json{{{").
			Build()

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 1)

		score := result.Scores[0]
		assert.False(t, score.Valid)
		assert.Contains(t, score.Error, "JSON still invalid after repair")
	})
}

// TestScoreAnswers_ValidatorInjection tests that validator and repair policy are properly injected.
func TestScoreAnswers_ValidatorInjection(t *testing.T) {
	t.Run("validator is injected with correct configuration", func(t *testing.T) {
		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(NewScoreBuilder().WithAnswerID("answer-1").Build()).
			Build()

		activities, mock, _, _ := NewTestScenarioBuilder().
			WithBlobThreshold(8192). // Custom threshold
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		_, err := activities.ScoreAnswers(context.Background(), input)
		require.NoError(t, err)

		// Verify validator was injected
		validator := mock.GetLastValidator()
		require.NotNil(t, validator, "validator should be injected")

		// Verify repair policy configuration
		repairPolicy := mock.GetLastRepairPolicy()
		require.NotNil(t, repairPolicy)
		assert.Equal(t, 1, repairPolicy.MaxAttempts, "should use one-shot repair")
		assert.True(t, repairPolicy.AllowTransportRepairs, "should allow transport repairs")
	})

	t.Run("validator configuration is consistent across calls", func(t *testing.T) {
		// Test with multiple answers to verify consistent injection
		plans := []ScorePlan{
			NewScorePlanBuilder().ForAnswer("answer-1").
				WithResult(NewScoreBuilder().WithAnswerID("answer-1").Build()).Build(),
			NewScorePlanBuilder().ForAnswer("answer-2").
				WithResult(NewScoreBuilder().WithAnswerID("answer-2").Build()).Build(),
		}

		activities, mock, _, _ := NewTestScenarioBuilder().
			WithScorePlans(plans...).
			Build()

		input := NewInputBuilder().WithMultipleAnswers(2).Build()

		_, err := activities.ScoreAnswers(context.Background(), input)
		require.NoError(t, err)

		// Each answer gets scored individually, so we should see consistent injection
		validator := mock.GetLastValidator()
		require.NotNil(t, validator)

		repairPolicy := mock.GetLastRepairPolicy()
		require.NotNil(t, repairPolicy)
		assert.Equal(t, 1, repairPolicy.MaxAttempts)
	})
}

// TestScoreAnswers_ValidatorEdgeCases tests edge cases for validator including empty JSON,
// extremely large JSON, and Unicode handling to ensure robust validation.
func TestScoreAnswers_ValidatorEdgeCases(t *testing.T) {
	t.Run("handles empty JSON gracefully", func(t *testing.T) {
		t.Run("completely empty string", func(t *testing.T) {
			plan := NewScorePlanBuilder().
				ForAnswer("answer-1").
				WithError(errors.New("malformed JSON"), "non-retryable").
				WithRawJSON(EmptyJSON).
				Build()

			activities, _, _, _ := NewTestScenarioBuilder().
				WithScorePlan(plan).
				Build()

			input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

			result, err := activities.ScoreAnswers(context.Background(), input)

			require.NoError(t, err, "activity should handle empty JSON gracefully")
			require.NotNil(t, result)
			require.Len(t, result.Scores, 1)

			score := result.Scores[0]
			assert.False(t, score.Valid, "score should be invalid for empty JSON")
			assert.Contains(t, score.Error, "malformed JSON", "error should indicate malformed JSON")
		})

		t.Run("whitespace only", func(t *testing.T) {
			plan := NewScorePlanBuilder().
				ForAnswer("answer-1").
				WithError(errors.New("malformed JSON"), "non-retryable").
				WithRawJSON(WhitespaceOnlyJSON).
				Build()

			activities, _, _, _ := NewTestScenarioBuilder().
				WithScorePlan(plan).
				Build()

			input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

			result, err := activities.ScoreAnswers(context.Background(), input)

			require.NoError(t, err)
			require.Len(t, result.Scores, 1)

			score := result.Scores[0]
			assert.False(t, score.Valid, "score should be invalid for whitespace-only JSON")
			assert.Contains(t, score.Error, "malformed JSON")
		})

		t.Run("empty object", func(t *testing.T) {
			plan := NewScorePlanBuilder().
				ForAnswer("answer-1").
				WithError(errors.New("business rule violation"), "non-retryable").
				WithRawJSON(EmptyObjectJSON).
				Build()

			activities, _, _, _ := NewTestScenarioBuilder().
				WithScorePlan(plan).
				Build()

			input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

			result, err := activities.ScoreAnswers(context.Background(), input)

			require.NoError(t, err)
			require.Len(t, result.Scores, 1)

			score := result.Scores[0]
			assert.False(t, score.Valid, "score should be invalid for empty object")
			assert.Contains(t, score.Error, "business rule violation")
		})
	})

	t.Run("handles extremely large JSON", func(t *testing.T) {
		// Test with 1MB JSON response (more reasonable for testing)
		const largeJSONSize = 1 * 1024 * 1024 // 1MB
		largeJSON := generateLargeJSON(largeJSONSize)

		expectedScore := NewScoreBuilder().
			WithID("score-answer-1").
			WithAnswerID("answer-1").
			WithValue(0.8).
			WithConfidence(0.9).
			WithInlineReasoning("Large reasoning content").
			Build()

		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(expectedScore).
			WithRawJSON(largeJSON).
			Build()

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err, "activity should handle large JSON without memory issues")
		require.NotNil(t, result)
		require.Len(t, result.Scores, 1)

		score := result.Scores[0]
		assert.True(t, score.Valid, "score should be valid for large JSON")
		assert.Equal(t, 0.8, score.Value)
		assert.Equal(t, 0.9, score.Confidence)

		// Verify the large JSON was processed correctly
		assert.Greater(t, len(largeJSON), largeJSONSize, "JSON should be larger than target size")
	})

	t.Run("handles Unicode in reasoning", func(t *testing.T) {
		expectedScore := NewScoreBuilder().
			WithID("score-answer-1").
			WithAnswerID("answer-1").
			WithValue(0.8).
			WithConfidence(0.9).
			WithInlineReasoning("测试中文字符 🚀 العربية עברית Здравствуй мир! This reasoning contains emojis 😀🎉 and various Unicode: αβγ ∑∏∆").
			Build()

		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(expectedScore).
			WithRawJSON(JSONWithUnicode).
			Build()

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err, "activity should handle Unicode characters gracefully")
		require.NotNil(t, result)
		require.Len(t, result.Scores, 1)

		score := result.Scores[0]
		assert.True(t, score.Valid, "score should be valid with Unicode content")
		assert.Equal(t, 0.8, score.Value)
		assert.Equal(t, 0.9, score.Confidence)

		// Verify Unicode characters are preserved in reasoning
		reasoning := score.InlineReasoning
		assert.Contains(t, reasoning, "测试中文字符", "Chinese characters should be preserved")
		assert.Contains(t, reasoning, "🚀", "rocket emoji should be preserved")
		assert.Contains(t, reasoning, "😀🎉", "emojis should be preserved")
		assert.Contains(t, reasoning, "العربية", "Arabic text should be preserved")
		assert.Contains(t, reasoning, "עברית", "Hebrew text should be preserved")
		assert.Contains(t, reasoning, "Здравствуй мир", "Cyrillic text should be preserved")
		assert.Contains(t, reasoning, "αβγ", "Greek letters should be preserved")
		assert.Contains(t, reasoning, "∑∏∆", "mathematical symbols should be preserved")
	})
}

// TestScoringValidator_DirectTesting tests the validator directly for comprehensive coverage.
func TestScoringValidator_DirectTesting(t *testing.T) {
	validator := NewScoringValidator("v1.0", TestThreshold)

	t.Run("valid JSON without repair", func(t *testing.T) {
		validated, repaired, err := validator.Validate([]byte(ValidJSONScore))

		require.NoError(t, err)
		assert.False(t, repaired, "no repair should be needed")
		assert.JSONEq(t, ValidJSONScore, string(validated))
	})

	t.Run("code fence repair", func(t *testing.T) {
		validated, repaired, err := validator.Validate([]byte(JSONWithCodeFence))

		require.NoError(t, err)
		assert.True(t, repaired, "repair should be applied")
		// The result should be valid JSON without code fences
		assert.NotContains(t, string(validated), "```")
	})

	t.Run("trailing comma repair", func(t *testing.T) {
		validated, repaired, err := validator.Validate([]byte(JSONWithTrailingComma))

		require.NoError(t, err)
		assert.True(t, repaired, "repair should be applied")
		// Should not contain trailing comma
		assert.NotContains(t, string(validated), ",}")
	})

	t.Run("unquoted keys repair", func(t *testing.T) {
		validated, repaired, err := validator.Validate([]byte(JSONWithUnquotedKeys))

		require.NoError(t, err)
		assert.True(t, repaired, "repair should be applied")
		// Should contain quoted keys
		assert.Contains(t, string(validated), `"score"`)
		assert.Contains(t, string(validated), `"reasoning"`)
	})

	t.Run("business rule violations are not repaired", func(t *testing.T) {
		_, _, err := validator.Validate([]byte(JSONViolatingBusinessRules))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "business rule violation")
	})

	t.Run("irreparable JSON fails after one attempt", func(t *testing.T) {
		_, _, err := validator.Validate([]byte(InvalidJSONNotRepairable))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "malformed JSON")
	})

	t.Run("short reasoning fails validation", func(t *testing.T) {
		shortReasoningJSON := `{
			"score": 0.8,
			"reasoning": "Short",
			"confidence": 0.9
		}`

		_, _, err := validator.Validate([]byte(shortReasoningJSON))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "business rule violation")
	})

	t.Run("confidence out of range fails validation", func(t *testing.T) {
		invalidConfidenceJSON := `{
			"score": 0.8,
			"reasoning": "This is a detailed reasoning that meets the minimum length requirement",
			"confidence": 1.5
		}`

		_, _, err := validator.Validate([]byte(invalidConfidenceJSON))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "business rule violation")
	})
}

// TestScoreAnswers_UsageTrackingWithRepair tests that usage is tracked even when repair is applied.
func TestScoreAnswers_UsageTrackingWithRepair(t *testing.T) {
	t.Run("usage tracked after successful repair", func(t *testing.T) {
		expectedScore := NewScoreBuilder().
			WithAnswerID("answer-1").
			WithUsage(75, 1, 150, domain.Cents(35)). // Custom usage
			Build()

		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(expectedScore).
			WithRawJSON(JSONWithCodeFence). // Will trigger repair
			WithUsage(75, 1, 150, domain.Cents(35)).
			Build()

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 1)

		// Verify usage is tracked correctly
		assert.Equal(t, int64(75), result.TokensUsed)
		assert.Equal(t, int64(1), result.CallsMade)
		assert.Equal(t, domain.Cents(35), result.CostCents)

		score := result.Scores[0]
		assert.True(t, score.Valid)
		assert.Equal(t, int64(75), score.TokensUsed)
		assert.Equal(t, int64(150), score.LatencyMs)
	})

	t.Run("no usage tracked for failed repair", func(t *testing.T) {
		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithError(errors.New("repair failed"), "non-retryable").
			WithUsage(0, 0, 0, 0). // No usage for failed requests
			Build()

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 1)

		// Verify no usage for failed score
		assert.Equal(t, int64(0), result.TokensUsed)
		assert.Equal(t, int64(0), result.CallsMade)
		assert.Equal(t, domain.Cents(0), result.CostCents)

		score := result.Scores[0]
		assert.False(t, score.Valid)
	})
}
