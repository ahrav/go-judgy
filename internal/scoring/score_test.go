//nolint:testpackage // Tests need access to unexported functions like nonRetryable
package scoring

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/business"
	"github.com/ahrav/go-judgy/pkg/activity"
)

// TestScoreAnswers verifies that ScoreAnswers returns proper error handling
// for the stub implementation in Story 1.2. Tests validate non-retryable error types,
// parameter handling, and consistent behavior required for Temporal activity execution.
func TestScoreAnswers(t *testing.T) {
	t.Run("handles llm client errors gracefully", func(t *testing.T) {
		// Create activities with mock client that returns "not implemented" error.
		base := activity.BaseActivities{}
		mockClient := newMockLLMClient()
		// mockClient.scoreReturnsError is true by default
		artifactStore := business.NewInMemoryArtifactStore()
		activities := NewActivities(base, mockClient, artifactStore, domain.DefaultBlobThresholdBytes, nil)

		ctx := context.Background()
		input := createValidScoreAnswersInput()

		// Execute the function.
		result, err := activities.ScoreAnswers(ctx, input)

		// ScoreAnswers should succeed even when LLM client fails (partial failure handling)
		require.NoError(t, err, "ScoreAnswers should handle LLM errors gracefully")
		require.NotNil(t, result, "ScoreAnswers should return result even with LLM errors")

		// Verify we get one score (one answer in input)
		assert.Len(t, result.Scores, 1, "Should return one score for one answer")

		// Verify the score is marked as invalid with error details
		score := result.Scores[0]
		assert.False(t, score.Valid, "Score should be marked as invalid")
		assert.Contains(t, score.Error, "not implemented", "Score error should contain LLM error")
		assert.Contains(t, score.Error, "ScoreAnswers not implemented", "Score error should contain specific error")

		// Verify activity-level metrics are still tracked
		assert.Equal(t, int64(0), result.TokensUsed, "No tokens should be used on error")
		assert.Equal(t, int64(0), result.CallsMade, "No calls should be counted on error")
	})

	t.Run("ignores context and input parameters", func(t *testing.T) {
		// Create activities with mock client for parameter testing.
		base := activity.BaseActivities{}
		mockClient := newMockLLMClient()
		artifactStore := business.NewInMemoryArtifactStore()
		activities := NewActivities(base, mockClient, artifactStore, domain.DefaultBlobThresholdBytes, nil)

		// Test that the stub function ignores its parameters as expected.
		// This verifies the function signature is correct.

		// Test with nil context (should not panic).
		result1, err1 := activities.ScoreAnswers(context.TODO(), domain.ScoreAnswersInput{})
		require.Error(t, err1, "should return error with nil context")
		assert.Nil(t, result1, "should return nil result")

		// Test with empty input (should not panic).
		ctx := context.Background()
		result2, err2 := activities.ScoreAnswers(ctx, domain.ScoreAnswersInput{})
		require.Error(t, err2, "should return error with empty input")
		assert.Nil(t, result2, "should return nil result")

		// Both calls should return the same error message.
		assert.Equal(t, err1.Error(), err2.Error(), "error should be consistent regardless of input")
	})

	t.Run("error details are captured in score validity", func(t *testing.T) {
		// Create activities with mock client for error validation.
		base := activity.BaseActivities{}
		mockClient := newMockLLMClient()
		// mockClient.scoreReturnsError is true by default
		artifactStore := business.NewInMemoryArtifactStore()
		activities := NewActivities(base, mockClient, artifactStore, domain.DefaultBlobThresholdBytes, nil)

		ctx := context.Background()
		input := createValidScoreAnswersInput()

		result, err := activities.ScoreAnswers(ctx, input)
		require.NoError(t, err, "activity should not fail")
		require.NotNil(t, result, "result should not be nil")

		// Verify error details are captured in the score
		require.Len(t, result.Scores, 1, "should have one score")
		score := result.Scores[0]

		// Verify error is wrapped in the score validity
		assert.False(t, score.Valid, "score should be invalid")
		assert.Contains(t, score.Error, "not implemented", "score error should contain LLM error")
		assert.Contains(t, score.Error, "ScoreAnswers", "score error should contain function reference")
	})

	t.Run("input validation path", func(t *testing.T) {
		// Create activities with mock client for input validation testing.
		base := activity.BaseActivities{}
		mockClient := newMockLLMClient()
		artifactStore := business.NewInMemoryArtifactStore()
		activities := NewActivities(base, mockClient, artifactStore, domain.DefaultBlobThresholdBytes, nil)

		ctx := context.Background()

		// Test with empty input to trigger validation path
		emptyInput := domain.ScoreAnswersInput{}
		result, err := activities.ScoreAnswers(ctx, emptyInput)

		// Should return validation error since validation runs first
		require.Error(t, err, "should return validation error with invalid input")
		assert.Nil(t, result, "should return nil result")

		// Verify it's a validation error, not the not-implemented error
		var appErr *temporal.ApplicationError
		require.ErrorAs(t, err, &appErr, "error should be ApplicationError")
		assert.Contains(t, appErr.Error(), "invalid input", "error should indicate validation failure")
	})

	t.Run("successful mock path", func(t *testing.T) {
		// Test the mock client success path by configuring it differently
		base := activity.BaseActivities{}
		mockClient := newMockLLMClient()
		mockClient.scoreReturnsError = false // Configure for success
		artifactStore := business.NewInMemoryArtifactStore()
		activities := NewActivities(base, mockClient, artifactStore, domain.DefaultBlobThresholdBytes, nil)

		ctx := context.Background()
		input := createValidScoreAnswersInput()

		result, err := activities.ScoreAnswers(ctx, input)

		// Should succeed with the mock client configured for success
		require.NoError(t, err, "should not return error when mock succeeds")
		require.NotNil(t, result, "should return valid result")
		assert.Len(t, result.Scores, 1, "should return expected number of scores")
	})
}

// TestScoreAnswers_ConcurrentBatchProcessing tests thread safety with concurrent calls
// to verify no race conditions occur during parallel processing.
func TestScoreAnswers_ConcurrentBatchProcessing(t *testing.T) {
	t.Run("thread-safe with concurrent batches", func(t *testing.T) {
		// Use the simple mock client approach like other successful tests
		base := activity.BaseActivities{}
		mockClient := newMockLLMClient()
		mockClient.scoreReturnsError = false // Configure for success
		artifactStore := business.NewInMemoryArtifactStore()
		activities := NewActivities(base, mockClient, artifactStore, domain.DefaultBlobThresholdBytes, nil)

		const numConcurrentCalls = 10

		// Channel to collect results from concurrent operations
		type callResult struct {
			callID int
			result *domain.ScoreAnswersOutput
			err    error
		}

		resultChan := make(chan callResult, numConcurrentCalls)

		// Use WaitGroup to coordinate concurrent operations
		var wg sync.WaitGroup

		// Launch concurrent ScoreAnswers calls
		for callID := 0; callID < numConcurrentCalls; callID++ {
			wg.Add(1)

			go func(id int) {
				defer wg.Done()

				// Create a unique input for this call
				input := domain.ScoreAnswersInput{
					Question: fmt.Sprintf("What is %d+%d?", id, id),
					Answers: []domain.Answer{
						{
							ID: fmt.Sprintf("answer-%d", id),
							ContentRef: domain.ArtifactRef{
								Key:  fmt.Sprintf("answers/test-%d.txt", id),
								Size: 4,
								Kind: domain.ArtifactAnswer,
							},
						},
					},
					Config: domain.DefaultEvalConfig(),
				}

				// Execute ScoreAnswers
				result, err := activities.ScoreAnswers(context.Background(), input)

				// Send result to channel
				resultChan <- callResult{
					callID: id,
					result: result,
					err:    err,
				}
			}(callID)
		}

		// Wait for all goroutines to complete
		wg.Wait()
		close(resultChan)

		// Collect and verify all results
		results := make(map[int]*domain.ScoreAnswersOutput)
		for callResult := range resultChan {
			require.NoError(t, callResult.err, "call %d should not have errors", callResult.callID)
			require.NotNil(t, callResult.result, "call %d should have result", callResult.callID)

			results[callResult.callID] = callResult.result
		}

		// Verify we got results for all calls
		assert.Len(t, results, numConcurrentCalls, "should have results for all concurrent calls")

		// Verify each call has exactly one score (since we sent one answer per call)
		for callID := 0; callID < numConcurrentCalls; callID++ {
			result, exists := results[callID]
			require.True(t, exists, "should have result for call %d", callID)
			require.Len(t, result.Scores, 1, "call %d should have exactly 1 score", callID)

			// Verify the score is valid (since mock is configured for success)
			score := result.Scores[0]
			assert.True(t, score.Valid, "call %d score should be valid", callID)
			assert.Equal(t, 0.75, score.Value, "call %d should have expected mock value", callID)
			assert.Equal(t, 0.85, score.Confidence, "call %d should have expected mock confidence", callID)
			assert.Equal(t, "Mock reasoning", score.InlineReasoning, "call %d should have mock reasoning", callID)

			// Verify usage metrics (tokens should be tracked)
			assert.Equal(t, int64(50), result.TokensUsed, "call %d should have expected token usage", callID)
		}

		// Verify the mock client was called the correct number of times
		_, scoreCalls := mockClient.GetCallCounts()
		assert.Equal(t, int64(numConcurrentCalls), scoreCalls,
			"mock client should have been called once per concurrent call")
	})
}

// createValidScoreAnswersInput creates a minimal valid input for testing
// ScoreAnswers stub behavior. Returns test data that satisfies domain
// validation requirements without requiring full implementation logic.
func createValidScoreAnswersInput() domain.ScoreAnswersInput {
	// Since this is Story 1.2 and the function is just a stub,
	// we don't need a fully valid input - just something that compiles.
	return domain.ScoreAnswersInput{
		Question: "What is 2+2?", // Simple test question.
		Answers: []domain.Answer{
			{
				ID: "550e8400-e29b-41d4-a716-446655440000", // Test answer identifier.
				ContentRef: domain.ArtifactRef{
					Key:  "answers/test.txt",
					Size: 4,
					Kind: domain.ArtifactAnswer,
				},
			},
		},
		Config: domain.DefaultEvalConfig(), // Standard evaluation configuration.
	}
}
