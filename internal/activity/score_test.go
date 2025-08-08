//nolint:testpackage // Tests need access to unexported functions like nonRetryable
package activity

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"

	"github.com/ahrav/go-judgy/internal/domain"
)

// TestScoreAnswers verifies that ScoreAnswers returns proper error handling
// for the stub implementation in Story 1.2. Tests validate non-retryable error types,
// parameter handling, and consistent behavior required for Temporal activity execution.
func TestScoreAnswers(t *testing.T) {
	t.Run("returns not implemented error", func(t *testing.T) {
		ctx := context.Background()
		input := createValidScoreAnswersInput()

		// Execute the function
		result, err := ScoreAnswers(ctx, input)

		// Verify it returns error and no result
		require.Error(t, err, "ScoreAnswers should return error")
		assert.Nil(t, result, "ScoreAnswers should return nil result")

		// Verify it's a non-retryable application error
		var appErr *temporal.ApplicationError
		require.ErrorAs(t, err, &appErr, "error should be ApplicationError")
		assert.Equal(t, "ScoreAnswers", appErr.Type(), "error type should be ScoreAnswers")
		assert.Contains(t, appErr.Error(), "ScoreAnswers not implemented", "error should indicate not implemented")
		assert.True(t, appErr.NonRetryable(), "error should be non-retryable")
	})

	t.Run("ignores context and input parameters", func(t *testing.T) {
		// Test that the stub function ignores its parameters as expected
		// This verifies the function signature is correct

		// Test with nil context (should not panic)
		result1, err1 := ScoreAnswers(context.TODO(), domain.ScoreAnswersInput{})
		require.Error(t, err1, "should return error with nil context")
		assert.Nil(t, result1, "should return nil result")

		// Test with empty input (should not panic)
		ctx := context.Background()
		result2, err2 := ScoreAnswers(ctx, domain.ScoreAnswersInput{})
		require.Error(t, err2, "should return error with empty input")
		assert.Nil(t, result2, "should return nil result")

		// Both calls should return the same error message
		assert.Equal(t, err1.Error(), err2.Error(), "error should be consistent regardless of input")
	})

	t.Run("error contains expected information", func(t *testing.T) {
		ctx := context.Background()
		input := createValidScoreAnswersInput()

		_, err := ScoreAnswers(ctx, input)
		require.Error(t, err, "should return error")

		// Verify error chain
		assert.ErrorIs(t, err, ErrNotImplemented, "error should wrap ErrNotImplemented")

		// Verify error message contains function name and implementation status
		errMsg := err.Error()
		assert.Contains(t, errMsg, "ScoreAnswers", "error message should contain function name")
		assert.Contains(t, errMsg, "not implemented", "error message should indicate not implemented")
	})
}

// createValidScoreAnswersInput creates a minimal valid input for testing
// ScoreAnswers stub behavior. Returns test data that satisfies domain
// validation requirements without requiring full implementation logic.
func createValidScoreAnswersInput() domain.ScoreAnswersInput {
	// Since this is Story 1.2 and the function is just a stub,
	// we don't need a fully valid input - just something that compiles
	return domain.ScoreAnswersInput{
		Question: "What is 2+2?", // Simple test question
		Answers: []domain.Answer{
			{
				ID: "550e8400-e29b-41d4-a716-446655440000", // Test answer identifier
				ContentRef: domain.ArtifactRef{
					Key:  "answers/test.txt",
					Size: 4,
					Kind: domain.ArtifactAnswer,
				},
			},
		},
		Config: domain.DefaultEvalConfig(), // Standard evaluation configuration
	}
}
