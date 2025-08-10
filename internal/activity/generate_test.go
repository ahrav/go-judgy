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

// TestGenerateAnswers verifies that GenerateAnswers returns appropriate error handling
// for the stub implementation in Story 1.2. Tests validate non-retryable error behavior,
// parameter handling, and consistent stub responses required for activity registration.
func TestGenerateAnswers(t *testing.T) {
	t.Run("returns not implemented error", func(t *testing.T) {
		// Create activities with mock client that returns "not implemented" error.
		activities := NewActivities(newMockLLMClient())

		ctx := context.Background()
		input := createValidGenerateAnswersInput()

		// Execute the function.
		result, err := activities.GenerateAnswers(ctx, input)

		// Verify it returns error and no result.
		require.Error(t, err, "GenerateAnswers should return error")
		assert.Nil(t, result, "GenerateAnswers should return nil result")

		// Verify it's a non-retryable application error.
		var appErr *temporal.ApplicationError
		require.ErrorAs(t, err, &appErr, "error should be ApplicationError")
		assert.Equal(t, "GenerateAnswers", appErr.Type(), "error type should be GenerateAnswers")
		assert.Contains(t, appErr.Error(), "GenerateAnswers not implemented", "error should indicate not implemented")
		assert.True(t, appErr.NonRetryable(), "error should be non-retryable")
	})

	t.Run("ignores context and input parameters", func(t *testing.T) {
		// Create activities with mock client for parameter testing.
		activities := NewActivities(newMockLLMClient())

		// Test that the stub function ignores its parameters as expected.
		// This verifies the function signature is correct.

		// Test with nil context (should not panic).
		result1, err1 := activities.GenerateAnswers(context.TODO(), domain.GenerateAnswersInput{})
		require.Error(t, err1, "should return error with nil context")
		assert.Nil(t, result1, "should return nil result")

		// Test with empty input (should not panic).
		ctx := context.Background()
		result2, err2 := activities.GenerateAnswers(ctx, domain.GenerateAnswersInput{})
		require.Error(t, err2, "should return error with empty input")
		assert.Nil(t, result2, "should return nil result")

		// Both calls should return the same error message.
		assert.Equal(t, err1.Error(), err2.Error(), "error should be consistent regardless of input")
	})

	t.Run("error contains expected information", func(t *testing.T) {
		// Create activities with mock client for error validation.
		activities := NewActivities(newMockLLMClient())

		ctx := context.Background()
		input := createValidGenerateAnswersInput()

		_, err := activities.GenerateAnswers(ctx, input)
		require.Error(t, err, "should return error")

		// Verify error chain.
		assert.ErrorIs(t, err, ErrNotImplemented, "error should wrap ErrNotImplemented")

		// Verify error message contains function name and implementation status.
		errMsg := err.Error()
		assert.Contains(t, errMsg, "GenerateAnswers", "error message should contain function name")
		assert.Contains(t, errMsg, "not implemented", "error message should indicate not implemented")
	})
}

// TestGenerateAnswersErrorHelper verifies the nonRetryable error helper function
// used by GenerateAnswers. This test ensures proper Temporal ApplicationError
// construction and error wrapping behavior for activity error handling.
func TestGenerateAnswersErrorHelper(t *testing.T) {
	t.Run("nonRetryable helper works correctly", func(t *testing.T) {
		// Test the error helper function used by GenerateAnswers.
		err := nonRetryable("TestTag", ErrNotImplemented, "test message")
		require.Error(t, err, "nonRetryable should return error")

		var appErr *temporal.ApplicationError
		require.ErrorAs(t, err, &appErr, "error should be ApplicationError")
		assert.Equal(t, "TestTag", appErr.Type(), "error type should match tag")
		assert.Contains(t, appErr.Error(), "test message", "error should contain message")
		assert.True(t, appErr.NonRetryable(), "error should be non-retryable")
		assert.ErrorIs(t, err, ErrNotImplemented, "error should wrap cause")
	})
}

// createValidGenerateAnswersInput creates a minimal valid input for testing
// GenerateAnswers stub behavior. Returns test data that satisfies domain
// validation requirements without requiring full implementation logic.
func createValidGenerateAnswersInput() domain.GenerateAnswersInput {
	// Since this is Story 1.2 and the function is just a stub,
	// we don't need a fully valid input - just something that compiles.
	return domain.GenerateAnswersInput{
		Question:   "What is 2+2?", // Simple test question.
		NumAnswers: 3,
		Config:     domain.DefaultEvalConfig(), // Standard evaluation configuration.
	}
}
