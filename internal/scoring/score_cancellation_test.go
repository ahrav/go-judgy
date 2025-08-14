package scoring

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ahrav/go-judgy/internal/domain"
)

// TestScoreAnswers_ContextCancellation tests context cancellation behavior.
func TestScoreAnswers_ContextCancellation(t *testing.T) {
	t.Run("context cancellation returns retryable error", func(t *testing.T) {
		// Create a mock that will delay long enough for cancellation
		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(NewScoreBuilder().WithAnswerID("answer-1").Build()).
			Build()

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		// Create a context that gets cancelled immediately
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		result, err := activities.ScoreAnswers(ctx, input)

		// Should return a retryable error due to context cancellation
		require.Error(t, err, "should return error when context is cancelled")
		assert.Nil(t, result, "should not return result when cancelled")

		// The error should indicate context cancellation
		assert.Contains(t, err.Error(), "context", "error should mention context cancellation")
	})

	t.Run("context cancellation during batch processing", func(t *testing.T) {
		// Create multiple answers to test cancellation during batch processing
		plans := []ScorePlan{
			NewScorePlanBuilder().
				ForAnswer("answer-1").
				WithResult(NewScoreBuilder().WithAnswerID("answer-1").Build()).
				Build(),
			NewScorePlanBuilder().
				ForAnswer("answer-2").
				WithResult(NewScoreBuilder().WithAnswerID("answer-2").Build()).
				Build(),
		}

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlans(plans...).
			Build()

		input := NewInputBuilder().WithMultipleAnswers(2).Build()

		// Create a context with a very short timeout
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
		defer cancel()

		// Give the context time to expire
		time.Sleep(10 * time.Millisecond)

		result, err := activities.ScoreAnswers(ctx, input)

		// Should return retryable error due to context timeout
		require.Error(t, err, "should return error when context times out")
		assert.Nil(t, result, "should not return partial results when cancelled")

		// Error should be related to context
		assert.Contains(t, err.Error(), "context", "error should mention context")
	})

	t.Run("context cancellation before processing starts", func(t *testing.T) {
		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(NewScoreBuilder().WithAnswerID("answer-1").Build()).
			Build()

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		// Create an already-cancelled context
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		result, err := activities.ScoreAnswers(ctx, input)

		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "context", "should detect cancellation before processing")
	})
}

// TestScoreAnswers_ProgressReporting tests progress reporting during long operations.
func TestScoreAnswers_ProgressReporting(t *testing.T) {
	t.Run("progress reported for each answer in batch", func(t *testing.T) {
		// Create multiple answers to trigger progress reporting
		plans := []ScorePlan{
			NewScorePlanBuilder().
				ForAnswer("answer-1").
				WithResult(NewScoreBuilder().
					WithID("score-1").
					WithAnswerID("answer-1").
					Build()).
				Build(),
			NewScorePlanBuilder().
				ForAnswer("answer-2").
				WithResult(NewScoreBuilder().
					WithID("score-2").
					WithAnswerID("answer-2").
					Build()).
				Build(),
			NewScorePlanBuilder().
				ForAnswer("answer-3").
				WithResult(NewScoreBuilder().
					WithID("score-3").
					WithAnswerID("answer-3").
					Build()).
				Build(),
		}

		activities, _, _, _, getProgressMessages := NewTestScenarioBuilder().
			WithScorePlans(plans...).
			BuildWithProgressCapture()

		input := NewInputBuilder().WithMultipleAnswers(3).Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Scores, 3)

		// Verify progress messages were recorded
		progressMessages := getProgressMessages()
		assert.GreaterOrEqual(t, len(progressMessages), 3, "should record progress for each answer")

		// Verify progress messages contain answer information
		for i, message := range progressMessages {
			if i < 3 { // We expect at least 3 progress messages
				assert.Contains(t, message, "Scoring answer", "progress should contain scoring information")
				assert.Contains(t, message, fmt.Sprintf("%d/3", i+1), "progress should show current/total")
			}
		}
	})

	t.Run("progress includes answer ID in message", func(t *testing.T) {
		plan := NewScorePlanBuilder().
			ForAnswer("answer-specific-id").
			WithResult(NewScoreBuilder().WithAnswerID("answer-specific-id").Build()).
			Build()

		activities, _, _, _, getProgressMessages := NewTestScenarioBuilder().
			WithScorePlan(plan).
			BuildWithProgressCapture()

		input := domain.ScoreAnswersInput{
			Question: "Test question",
			Answers: []domain.Answer{
				{
					ID: "answer-specific-id",
					ContentRef: domain.ArtifactRef{
						Key:  "test-answer.txt",
						Kind: domain.ArtifactAnswer,
					},
				},
			},
			Config: domain.DefaultEvalConfig(),
		}

		_, err := activities.ScoreAnswers(context.Background(), input)
		require.NoError(t, err)

		progressMessages := getProgressMessages()
		require.GreaterOrEqual(t, len(progressMessages), 1, "should record at least one progress message")

		// Verify progress contains the specific answer ID
		assert.Contains(t, progressMessages[0], "answer-specific-id", "progress should contain answer ID")
	})

	t.Run("progress recorded even for failed scores", func(t *testing.T) {
		plans := []ScorePlan{
			NewScorePlanBuilder().
				ForAnswer("answer-1").
				WithResult(NewScoreBuilder().WithAnswerID("answer-1").Build()).
				Build(),
			NewScorePlanBuilder().
				ForAnswer("answer-2").
				WithError(errors.New("scoring failed"), "non-retryable").
				Build(),
		}

		activities, _, _, _, getProgressMessages := NewTestScenarioBuilder().
			WithScorePlans(plans...).
			BuildWithProgressCapture()

		input := NewInputBuilder().WithMultipleAnswers(2).Build()

		result, err := activities.ScoreAnswers(context.Background(), input)

		require.NoError(t, err)
		require.Len(t, result.Scores, 2)

		// Should record progress for both answers, even the failed one
		progressMessages := getProgressMessages()
		assert.GreaterOrEqual(t, len(progressMessages), 2, "should record progress for all answers including failed ones")

		// Verify progress messages contain scoring information for both answers
		assert.Contains(t, progressMessages[0], "Scoring answer 1/2", "first progress should show 1/2")
		assert.Contains(t, progressMessages[1], "Scoring answer 2/2", "second progress should show 2/2")

		// Verify both progress messages contain answer IDs (UUIDs)
		for i, message := range progressMessages[:2] {
			assert.Contains(t, message, "ID:", "progress message %d should contain answer ID", i+1)
		}
	})
}

// TestScoreAnswers_ContextTimeout tests timeout behavior.
func TestScoreAnswers_ContextTimeout(t *testing.T) {
	t.Run("respects context timeout during processing", func(t *testing.T) {
		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(NewScoreBuilder().WithAnswerID("answer-1").Build()).
			Build()

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		// Create context with very short timeout
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Microsecond)
		defer cancel()

		// Wait for timeout to occur
		time.Sleep(10 * time.Millisecond)

		result, err := activities.ScoreAnswers(ctx, input)

		require.Error(t, err, "should return error when context times out")
		assert.Nil(t, result, "should not return result when timed out")
		assert.Contains(t, err.Error(), "context", "error should indicate context timeout")
	})

	t.Run("completes successfully with adequate timeout", func(t *testing.T) {
		plan := NewScorePlanBuilder().
			ForAnswer("answer-1").
			WithResult(NewScoreBuilder().WithAnswerID("answer-1").Build()).
			Build()

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlan(plan).
			Build()

		input := NewInputBuilder().WithSingleAnswer("answer-1").Build()

		// Create context with generous timeout
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		result, err := activities.ScoreAnswers(ctx, input)

		require.NoError(t, err, "should succeed with adequate timeout")
		require.NotNil(t, result)
		require.Len(t, result.Scores, 1)
		assert.True(t, result.Scores[0].Valid)
	})
}

// TestScoreAnswers_LongRunningOperation tests behavior during extended operations.
func TestScoreAnswers_LongRunningOperation(t *testing.T) {
	t.Run("handles large batch without timeout", func(t *testing.T) {
		// Create a larger batch to simulate long-running operation
		var plans []ScorePlan
		batchSize := 20

		for i := 0; i < batchSize; i++ {
			answerID := fmt.Sprintf("answer-%d", i+1) // Make it consistent with WithMultipleAnswers which uses 1-based indexing
			plan := NewScorePlanBuilder().
				ForAnswer(answerID).
				WithResult(NewScoreBuilder().
					WithAnswerID(answerID).
					WithValue(float64(i) / float64(batchSize)).
					Build()).
				Build()
			plans = append(plans, plan)
		}

		activities, _, _, _ := NewTestScenarioBuilder().
			WithScorePlans(plans...).
			Build()

		input := NewInputBuilder().WithMultipleAnswers(batchSize).Build()

		// Use a reasonable timeout for the large batch
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		result, err := activities.ScoreAnswers(ctx, input)

		require.NoError(t, err, "should handle large batch successfully")
		require.NotNil(t, result)
		require.Len(t, result.Scores, batchSize, "should return scores for all answers")

		// Verify all scores are valid
		for i, score := range result.Scores {
			assert.True(t, score.Valid, "score %d should be valid", i)
			expectedAnswerID := input.Answers[i].ID
			assert.Equal(t, expectedAnswerID, score.AnswerID, "score %d should have correct answer ID", i)
		}
	})
}
