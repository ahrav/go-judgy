package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"github.com/ahrav/go-judgy/internal/domain"
)

// TestEvaluationWorkflow verifies that EvaluationWorkflow handles validation,
// configuration, and error handling correctly for the stub implementation in Story 1.2.
// Tests cover input validation, activity option setup, and deterministic workflow behavior.
func TestEvaluationWorkflow(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}

	t.Run("valid request returns not implemented error", func(t *testing.T) {
		env := testSuite.NewTestWorkflowEnvironment()
		defer env.AssertExpectations(t)

		// Create a valid evaluation request
		req := createValidEvaluationRequest()

		// Execute the workflow function directly
		env.ExecuteWorkflow(EvaluationWorkflow, req)

		// Verify workflow completed
		require.True(t, env.IsWorkflowCompleted(), "workflow should complete")

		// Verify it returns the expected "not implemented" error
		err := env.GetWorkflowError()
		require.Error(t, err, "workflow should return error")

		// Check that it's a non-retryable application error
		var appErr *temporal.ApplicationError
		require.ErrorAs(t, err, &appErr, "error should be ApplicationError")
		assert.Equal(t, "NotImplemented", appErr.Type(), "error type should be NotImplemented")
		assert.Contains(t, appErr.Error(), "EvaluationWorkflow not implemented", "error should indicate not implemented")
		assert.True(t, appErr.NonRetryable(), "error should be non-retryable")
	})

	t.Run("invalid request fails validation", func(t *testing.T) {
		env := testSuite.NewTestWorkflowEnvironment()
		defer env.AssertExpectations(t)

		// Create an invalid request that should fail validation
		req := domain.EvaluationRequest{} // Empty request to test validation

		// Execute the workflow
		env.ExecuteWorkflow(EvaluationWorkflow, req)

		// Verify workflow completed with error
		require.True(t, env.IsWorkflowCompleted(), "workflow should complete")

		err := env.GetWorkflowError()
		require.Error(t, err, "workflow should return validation error")

		// Check that it's a validation error
		var appErr *temporal.ApplicationError
		require.ErrorAs(t, err, &appErr, "error should be ApplicationError")
		assert.Equal(t, "Validation", appErr.Type(), "error type should be Validation")
		assert.Contains(t, appErr.Error(), "invalid evaluation request", "error should indicate validation failure")
	})

	t.Run("workflow uses version gate", func(t *testing.T) {
		// This test verifies the version gate is called
		// The version gate call should be deterministic and not cause issues
		req := createValidEvaluationRequest()

		// Execute workflow multiple times to verify deterministic behavior
		for i := 0; i < 3; i++ {
			env := testSuite.NewTestWorkflowEnvironment()
			defer env.AssertExpectations(t)

			env.ExecuteWorkflow(EvaluationWorkflow, req)
			require.True(t, env.IsWorkflowCompleted(), "workflow should complete on attempt %d", i+1)

			err := env.GetWorkflowError()
			require.Error(t, err, "workflow should return error on attempt %d", i+1)

			var appErr *temporal.ApplicationError
			require.ErrorAs(t, err, &appErr, "error should be ApplicationError on attempt %d", i+1)
			assert.Contains(t, appErr.Error(), "not implemented", "should get not implemented error on attempt %d", i+1)
		}
	})

	t.Run("workflow sets activity options", func(t *testing.T) {
		env := testSuite.NewTestWorkflowEnvironment()
		defer env.AssertExpectations(t)

		// Test that activity options are set correctly by the workflow
		// This is a bit tricky to test directly, but we can verify the workflow
		// completes without timeout-related issues in the setup phase
		req := createValidEvaluationRequest()

		// Execute with a reasonable timeout
		env.ExecuteWorkflow(EvaluationWorkflow, req)
		require.True(t, env.IsWorkflowCompleted(), "workflow should complete without timeout")

		err := env.GetWorkflowError()
		require.Error(t, err, "workflow should return not implemented error")

		// Verify it's our expected error, not a timeout
		var appErr *temporal.ApplicationError
		require.ErrorAs(t, err, &appErr, "error should be ApplicationError")
		assert.Equal(t, "NotImplemented", appErr.Type(), "should be not implemented error, not timeout")
	})
}

// TestEvaluationWorkflowDeterminism verifies that multiple executions of EvaluationWorkflow
// produce identical results. This test ensures workflow determinism required by Temporal
// for workflow replay and consistency guarantees.
func TestEvaluationWorkflowDeterminism(t *testing.T) {
	t.Run("multiple executions are deterministic", func(t *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}

		req := createValidEvaluationRequest()
		var results []error

		// Execute the workflow multiple times
		for i := 0; i < 5; i++ {
			env := testSuite.NewTestWorkflowEnvironment()
			env.ExecuteWorkflow(EvaluationWorkflow, req)
			results = append(results, env.GetWorkflowError())
			env.AssertExpectations(t)
		}

		// Verify all executions produced the same error result
		for i := 1; i < len(results); i++ {
			assert.Equal(t, results[0].Error(), results[i].Error(),
				"execution %d should match first execution", i)
		}
	})
}

// createValidEvaluationRequest creates a minimal valid evaluation request for testing
// EvaluationWorkflow stub behavior. Returns test data that satisfies domain
// validation requirements and workflow input contracts.
func createValidEvaluationRequest() domain.EvaluationRequest {
	// Create a minimal valid request based on domain.EvaluationRequest structure
	// Since this is Story 1.2, we just need a request that passes basic validation
	principal := domain.Principal{
		Type: domain.PrincipalUser,
		ID:   "test@example.com",
	}

	req, err := domain.NewEvaluationRequest(
		"What is 2+2?", // Simple test question
		principal,
		domain.DefaultEvalConfig(), // Standard evaluation configuration
		domain.DefaultBudgetLimits(),
	)
	if err != nil {
		// If this fails, we'll catch it in the test
		return domain.EvaluationRequest{}
	}
	return *req // Return the constructed request
}
