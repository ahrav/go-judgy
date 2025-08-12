//nolint:testpackage // Tests need access to unexported functions like nonRetryable
package aggregation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/pkg/activity"
)

// TestAggregateScores verifies that AggregateScores returns proper error handling
// for the stub implementation in Story 1.2. Tests cover error type validation,
// parameter handling, and deterministic behavior required for Temporal activities.
func TestAggregateScores(t *testing.T) {
	t.Run("returns not implemented error", func(t *testing.T) {
		// Create activities instance
		base := activity.BaseActivities{}
		activities := NewActivities(base)

		ctx := context.Background()
		input := createValidAggregateScoresInput()

		// Execute the function
		result, err := activities.AggregateScores(ctx, input)

		// Verify it returns error and no result
		require.Error(t, err, "AggregateScores should return error")
		assert.Nil(t, result, "AggregateScores should return nil result")

		// Verify it's a non-retryable application error
		var appErr *temporal.ApplicationError
		require.ErrorAs(t, err, &appErr, "error should be ApplicationError")
		assert.Equal(t, "AggregateScores", appErr.Type(), "error type should be AggregateScores")
		assert.Contains(t, appErr.Error(), "AggregateScores not implemented", "error should indicate not implemented")
		assert.True(t, appErr.NonRetryable(), "error should be non-retryable")
	})

	t.Run("ignores context and input parameters", func(t *testing.T) {
		// Create activities instance
		base := activity.BaseActivities{}
		activities := NewActivities(base)

		// Test that the stub function ignores its parameters as expected
		// This verifies the function signature is correct

		// Test with nil context (should not panic)
		result1, err1 := activities.AggregateScores(context.TODO(), domain.AggregateScoresInput{})
		require.Error(t, err1, "should return error with nil context")
		assert.Nil(t, result1, "should return nil result")

		// Test with empty input (should not panic)
		ctx := context.Background()
		result2, err2 := activities.AggregateScores(ctx, domain.AggregateScoresInput{})
		require.Error(t, err2, "should return error with empty input")
		assert.Nil(t, result2, "should return nil result")

		// Both calls should return the same error message
		assert.Equal(t, err1.Error(), err2.Error(), "error should be consistent regardless of input")
	})

	t.Run("error contains expected information", func(t *testing.T) {
		// Create activities instance
		base := activity.BaseActivities{}
		activities := NewActivities(base)

		ctx := context.Background()
		input := createValidAggregateScoresInput()

		_, err := activities.AggregateScores(ctx, input)
		require.Error(t, err, "should return error")

		// Verify error chain
		assert.ErrorIs(t, err, ErrNotImplemented, "error should wrap ErrNotImplemented")

		// Verify error message contains function name and implementation status
		errMsg := err.Error()
		assert.Contains(t, errMsg, "AggregateScores", "error message should contain function name")
		assert.Contains(t, errMsg, "not implemented", "error message should indicate not implemented")
	})

	t.Run("pure function property", func(t *testing.T) {
		// Create activities instance
		base := activity.BaseActivities{}
		activities := NewActivities(base)

		// Test that the function is deterministic (pure function)
		// Even though it's just a stub, it should behave consistently
		ctx := context.Background()
		input := createValidAggregateScoresInput()

		// Call multiple times and verify consistent behavior
		_, err1 := activities.AggregateScores(ctx, input)
		_, err2 := activities.AggregateScores(ctx, input)
		_, err3 := activities.AggregateScores(ctx, input)

		require.Error(t, err1, "first call should return error")
		require.Error(t, err2, "second call should return error")
		require.Error(t, err3, "third call should return error")

		// All errors should be identical (deterministic behavior)
		assert.Equal(t, err1.Error(), err2.Error(), "first and second call should return same error")
		assert.Equal(t, err2.Error(), err3.Error(), "second and third call should return same error")
	})
}

// createValidAggregateScoresInput creates a minimal valid input for testing
// AggregateScores stub behavior. Returns test data that satisfies domain
// validation requirements without requiring full implementation logic.
func createValidAggregateScoresInput() domain.AggregateScoresInput {
	// Since this is Story 1.2 and the function is just a stub,
	// we don't need a fully valid input - just something that compiles
	answerID := "550e8400-e29b-41d4-a716-446655440000"
	return domain.AggregateScoresInput{
		Scores: []domain.Score{
			{
				ID:       "660e8400-e29b-41d4-a716-446655440000",
				AnswerID: answerID, // Reference to answer being scored
				Value:    0.85,
			},
		},
		Answers: []domain.Answer{
			{
				ID: answerID,
				ContentRef: domain.ArtifactRef{
					Key:  "answers/test.txt",
					Size: 4,
					Kind: domain.ArtifactAnswer,
				},
			},
		},
	}
}
