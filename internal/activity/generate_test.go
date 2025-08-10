//nolint:testpackage // Tests need access to unexported functions like nonRetryable
package activity

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/business"
)

// TestGenerateAnswers verifies that GenerateAnswers returns appropriate error handling
// for the stub implementation in Story 1.2. Tests validate non-retryable error behavior,
// parameter handling, and consistent stub responses required for activity registration.
func TestGenerateAnswers(t *testing.T) {
	t.Run("successful generation with artifact storage", func(t *testing.T) {
		// Create activities with mock client and artifact store
		mockClient := newMockLLMClient()
		mockClient.generateReturnsError = false // Enable successful generation
		artifactStore := business.NewInMemoryArtifactStore()
		activities := NewActivities(mockClient, artifactStore, domain.NewNoOpEventSink())

		ctx := context.Background()
		input := createValidGenerateAnswersInput()

		// Execute the function.
		result, err := activities.GenerateAnswers(ctx, input)

		// Verify successful execution
		require.NoError(t, err, "GenerateAnswers should succeed")
		require.NotNil(t, result, "GenerateAnswers should return result")

		// Verify result structure
		assert.Len(t, result.Answers, 1, "should return one answer")
		assert.NotEmpty(t, result.Answers[0].ContentRef.Key, "answer should have artifact reference")
		assert.Equal(t, domain.ArtifactAnswer, result.Answers[0].ContentRef.Kind, "artifact should be answer type")
		assert.Greater(t, result.TokensUsed, int64(0), "should report token usage")
		assert.Greater(t, result.CallsMade, int64(0), "should report API calls")
	})

	t.Run("returns error when LLM client fails", func(t *testing.T) {
		// Create activities with mock client that returns error.
		mockClient := newMockLLMClient()
		mockClient.generateReturnsError = true // Force error
		artifactStore := business.NewInMemoryArtifactStore()
		activities := NewActivities(mockClient, artifactStore, domain.NewNoOpEventSink())

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
		assert.True(t, appErr.NonRetryable(), "error should be non-retryable")
	})

	t.Run("input validation", func(t *testing.T) {
		// Create activities with mock client for parameter testing.
		mockClient := newMockLLMClient()
		artifactStore := business.NewInMemoryArtifactStore()
		activities := NewActivities(mockClient, artifactStore, domain.NewNoOpEventSink())

		// Test with empty input (should validate and return error).
		ctx := context.Background()
		result, err := activities.GenerateAnswers(ctx, domain.GenerateAnswersInput{})
		require.Error(t, err, "should return validation error with empty input")
		assert.Nil(t, result, "should return nil result")

		// Verify it's a non-retryable validation error
		var appErr *temporal.ApplicationError
		require.ErrorAs(t, err, &appErr, "error should be ApplicationError")
		assert.Equal(t, "GenerateAnswers", appErr.Type(), "error type should be GenerateAnswers")
		assert.True(t, appErr.NonRetryable(), "validation error should be non-retryable")
		assert.Contains(t, appErr.Error(), "invalid input", "error should indicate invalid input")
	})

	t.Run("artifact storage integration", func(t *testing.T) {
		// Create activities with successful mock client
		mockClient := newMockLLMClient()
		mockClient.generateReturnsError = false
		artifactStore := business.NewInMemoryArtifactStore()
		activities := NewActivities(mockClient, artifactStore, domain.NewNoOpEventSink())

		ctx := context.Background()
		input := createValidGenerateAnswersInput()

		result, err := activities.GenerateAnswers(ctx, input)
		require.NoError(t, err, "should succeed")
		require.NotNil(t, result, "should return result")

		// Verify artifact was stored
		answer := result.Answers[0]
		require.NotEmpty(t, answer.ContentRef.Key, "answer should have artifact key")

		// Verify we can retrieve the stored content
		content, err := artifactStore.Get(ctx, answer.ContentRef)
		require.NoError(t, err, "should be able to retrieve stored content")
		assert.NotEmpty(t, content, "stored content should not be empty")
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

	t.Run("retryable helper works correctly", func(t *testing.T) {
		// Test the retryable error helper function
		err := retryable("TestTag", ErrProviderUnavailable, "test message")
		require.Error(t, err, "retryable should return error")

		var appErr *temporal.ApplicationError
		require.ErrorAs(t, err, &appErr, "error should be ApplicationError")
		assert.Equal(t, "TestTag", appErr.Type(), "error type should match tag")
		assert.Contains(t, appErr.Error(), "test message", "error should contain message")
		assert.False(t, appErr.NonRetryable(), "error should be retryable")
	})
}

// TestErrorTypeMethods tests the custom error type methods
func TestErrorTypeMethods(t *testing.T) {
	t.Run("error methods work correctly", func(t *testing.T) {
		// Create a custom activity error
		originalErr := errors.New("original cause")
		activityErr := &Error{
			Type:      ErrorValidation,
			Message:   "test error message",
			Cause:     originalErr,
			Retryable: false,
		}

		// Test Error() method
		errorStr := activityErr.Error()
		assert.Contains(t, errorStr, "validation")
		assert.Contains(t, errorStr, "test error message")
		assert.Contains(t, errorStr, "non-retryable")
		assert.Contains(t, errorStr, originalErr.Error())

		// Test Unwrap() method
		unwrappedErr := activityErr.Unwrap()
		assert.Equal(t, originalErr, unwrappedErr)
	})

	t.Run("error methods work correctly for retryable errors", func(t *testing.T) {
		// Create a retryable activity error
		originalErr := errors.New("provider error")
		activityErr := &Error{
			Type:      ErrorProvider,
			Message:   "provider unavailable",
			Cause:     originalErr,
			Retryable: true,
		}

		// Test Error() method
		errorStr := activityErr.Error()
		assert.Contains(t, errorStr, "provider")
		assert.Contains(t, errorStr, "provider unavailable")
		assert.Contains(t, errorStr, "retryable")

		// Test Unwrap() method
		unwrappedErr := activityErr.Unwrap()
		assert.Equal(t, originalErr, unwrappedErr)
	})

	t.Run("error methods work correctly without cause", func(t *testing.T) {
		// Create an error without underlying cause
		activityErr := &Error{
			Type:      ErrorBudget,
			Message:   "insufficient budget",
			Cause:     nil,
			Retryable: false,
		}

		// Test Error() method
		errorStr := activityErr.Error()
		assert.Contains(t, errorStr, "budget")
		assert.Contains(t, errorStr, "insufficient budget")
		assert.Contains(t, errorStr, "non-retryable")

		// Test Unwrap() method returns nil
		unwrappedErr := activityErr.Unwrap()
		assert.Nil(t, unwrappedErr)
	})
}

// TestCleanupArtifacts tests the cleanup functionality
func TestCleanupArtifacts(t *testing.T) {
	t.Run("cleanup handles existing artifacts", func(t *testing.T) {
		mockClient := newMockLLMClient()
		artifactStore := business.NewInMemoryArtifactStore()
		activities := NewActivities(mockClient, artifactStore, domain.NewNoOpEventSink())

		// Create some test artifacts
		ref1, err := artifactStore.Put(context.Background(), "content1", domain.ArtifactAnswer, "test-key-1")
		require.NoError(t, err)

		ref2, err := artifactStore.Put(context.Background(), "content2", domain.ArtifactAnswer, "test-key-2")
		require.NoError(t, err)

		answers := []domain.Answer{
			{
				ID:         "answer1",
				ContentRef: ref1,
			},
			{
				ID:         "answer2",
				ContentRef: ref2,
			},
		}

		// Test cleanup (should not panic or error)
		activities.cleanupArtifacts(context.Background(), answers)
	})

	t.Run("cleanup handles empty artifacts list", func(t *testing.T) {
		mockClient := newMockLLMClient()
		artifactStore := business.NewInMemoryArtifactStore()
		activities := NewActivities(mockClient, artifactStore, domain.NewNoOpEventSink())

		// Test cleanup with empty list (should not panic)
		activities.cleanupArtifacts(context.Background(), []domain.Answer{})
	})
}

// TestAdditionalCoverage adds tests for remaining uncovered paths
func TestAdditionalCoverage(t *testing.T) {
	t.Run("storeAnswerContent with existing artifact reference", func(t *testing.T) {
		mockClient := newMockLLMClient()
		artifactStore := business.NewInMemoryArtifactStore()
		activities := NewActivities(mockClient, artifactStore, domain.NewNoOpEventSink())

		// First, actually store content with the expected key
		existingRef, err := artifactStore.Put(context.Background(), "existing content", domain.ArtifactAnswer, "existing-key")
		require.NoError(t, err)

		// Create an answer with existing content reference
		answer := domain.Answer{
			ID:         "existing-answer",
			ContentRef: existingRef, // Use the actual stored reference
		}

		// Should return existing reference without storing new content
		mockOutput := &domain.GenerateAnswersOutput{}
		ref, err := activities.storeAnswerContent(context.Background(), answer, "test-workflow", mockOutput)
		require.NoError(t, err)
		assert.Equal(t, "existing-key", ref.Key)
		assert.Equal(t, domain.ArtifactAnswer, ref.Kind)
	})

	t.Run("storeAnswerContent fallback path without content metadata", func(t *testing.T) {
		mockClient := newMockLLMClient()
		artifactStore := business.NewInMemoryArtifactStore()
		activities := NewActivities(mockClient, artifactStore, domain.NewNoOpEventSink())

		// Create an answer without content metadata
		answer := domain.Answer{
			ID:       "no-content-answer",
			Metadata: map[string]any{}, // Empty metadata
		}

		// Should use fallback content generation
		mockOutput := &domain.GenerateAnswersOutput{}
		ref, err := activities.storeAnswerContent(context.Background(), answer, "test-workflow", mockOutput)
		require.NoError(t, err)
		assert.NotEmpty(t, ref.Key)

		// Verify fallback content was stored
		content, err := artifactStore.Get(context.Background(), ref)
		require.NoError(t, err)
		assert.Contains(t, content, "no-content-answer")
	})
}

// createValidGenerateAnswersInput creates a minimal valid input for testing
// GenerateAnswers behavior. Returns test data that satisfies domain
// validation requirements for full implementation testing.
func createValidGenerateAnswersInput() domain.GenerateAnswersInput {
	return domain.GenerateAnswersInput{
		Question:   "What is 2+2?", // Simple test question.
		NumAnswers: 3,
		Config:     domain.DefaultEvalConfig(), // Standard evaluation configuration.
	}
}

// TestGenerateAnswersComprehensive provides comprehensive testing of the GenerateAnswers activity
// including concurrency scenarios, failure handling, artifact management, and edge cases.
func TestGenerateAnswersComprehensive(t *testing.T) {
	t.Run("concurrent access safety", func(t *testing.T) {
		// Test concurrent access to the same activity instance
		mockClient := newMockLLMClient()
		mockClient.generateReturnsError = false
		artifactStore := business.NewInMemoryArtifactStore()
		activities := NewActivities(mockClient, artifactStore, domain.NewNoOpEventSink())

		const numRoutines = 10
		var wg sync.WaitGroup
		results := make([]*domain.GenerateAnswersOutput, numRoutines)
		errors := make([]error, numRoutines)

		ctx := context.Background()
		input := createValidGenerateAnswersInput()

		for i := 0; i < numRoutines; i++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				result, err := activities.GenerateAnswers(ctx, input)
				results[index] = result
				errors[index] = err
			}(i)
		}

		wg.Wait()

		// Verify all calls succeeded
		for i := 0; i < numRoutines; i++ {
			require.NoError(t, errors[i], "concurrent call %d should succeed", i)
			require.NotNil(t, results[i], "concurrent call %d should return result", i)
			assert.Len(t, results[i].Answers, 1, "concurrent call %d should return one answer", i)
		}

		// Verify artifacts were stored uniquely
		uniqueKeys := make(map[string]bool)
		for i, result := range results {
			key := result.Answers[0].ContentRef.Key
			assert.NotEmpty(t, key, "result %d should have artifact key", i)
			assert.False(t, uniqueKeys[key], "artifact key should be unique for result %d", i)
			uniqueKeys[key] = true
		}
	})

	t.Run("artifact storage failure handling", func(t *testing.T) {
		// Create a failing artifact store
		failingStore := &failingArtifactStore{failures: 1}
		mockClient := newMockLLMClient()
		mockClient.generateReturnsError = false
		activities := NewActivities(mockClient, failingStore, domain.NewNoOpEventSink())

		ctx := context.Background()
		input := createValidGenerateAnswersInput()

		result, err := activities.GenerateAnswers(ctx, input)

		// Should fail with non-retryable error
		require.Error(t, err)
		assert.Nil(t, result)

		var appErr *temporal.ApplicationError
		require.ErrorAs(t, err, &appErr)
		assert.True(t, appErr.NonRetryable())
		assert.Contains(t, appErr.Error(), "failed to store answer content")
	})

	t.Run("budget and usage metrics validation", func(t *testing.T) {
		mockClient := newMockLLMClient()
		mockClient.generateReturnsError = false
		artifactStore := business.NewInMemoryArtifactStore()
		activities := NewActivities(mockClient, artifactStore, domain.NewNoOpEventSink())

		ctx := context.Background()
		input := createValidGenerateAnswersInput()

		result, err := activities.GenerateAnswers(ctx, input)

		require.NoError(t, err)
		require.NotNil(t, result)

		// Verify usage metrics are properly reported
		assert.Greater(t, result.TokensUsed, int64(0), "should report token usage")
		assert.Greater(t, result.CallsMade, int64(0), "should report API calls")
		assert.Greater(t, int64(result.CostCents), int64(0), "should report cost in cents")

		// Verify cost calculation consistency
		assert.Equal(t, domain.Cents(25), result.CostCents, "should match mock client cost")
	})

	t.Run("multiple answers artifact storage", func(t *testing.T) {
		// Test with multiple answers to verify batch processing
		mockClient := &multiAnswerMockClient{numAnswers: 3}
		artifactStore := business.NewInMemoryArtifactStore()
		activities := NewActivities(mockClient, artifactStore, domain.NewNoOpEventSink())

		ctx := context.Background()
		input := createValidGenerateAnswersInput()
		input.NumAnswers = 3

		result, err := activities.GenerateAnswers(ctx, input)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Len(t, result.Answers, 3, "should return three answers")

		// Verify each answer has unique artifact reference
		keys := make(map[string]bool)
		for i, answer := range result.Answers {
			assert.NotEmpty(t, answer.ContentRef.Key, "answer %d should have artifact key", i)
			assert.False(t, keys[answer.ContentRef.Key], "answer %d should have unique artifact key", i)
			keys[answer.ContentRef.Key] = true

			// Verify content was stored
			content, err := artifactStore.Get(ctx, answer.ContentRef)
			require.NoError(t, err, "should be able to retrieve content for answer %d", i)
			assert.Contains(t, content, fmt.Sprintf("answer #%d", i+1), "content should match for answer %d", i)
		}
	})

	t.Run("context cancellation handling", func(t *testing.T) {
		mockClient := &slowMockLLMClient{delay: time.Millisecond * 100}
		artifactStore := business.NewInMemoryArtifactStore()
		activities := NewActivities(mockClient, artifactStore, domain.NewNoOpEventSink())

		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*10)
		defer cancel()

		input := createValidGenerateAnswersInput()

		result, err := activities.GenerateAnswers(ctx, input)

		// Should handle cancellation gracefully
		require.Error(t, err, "should return error on cancellation")
		assert.Nil(t, result, "should return nil result on cancellation")

		// Context cancellation should be handled gracefully and classified as a retryable timeout
		var appErr *temporal.ApplicationError
		if errors.As(err, &appErr) {
			// Should be classified as GenerateAnswers error with timeout behavior
			assert.Equal(t, "GenerateAnswers", appErr.Type(), "error type should be GenerateAnswers")
			assert.True(t, !appErr.NonRetryable(), "timeout errors should be retryable")
			assert.Contains(t, appErr.Error(), "timeout", "error should indicate timeout")
		} else {
			// Fallback: direct context error is also acceptable
			assert.True(t, errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled),
				"error should be context cancellation, got: %v", err)
		}
	})
}

// TestGenerateAnswersTemporalIntegration tests the activity using Temporal's test environment
func TestGenerateAnswersTemporalIntegration(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	// Register activity with dependencies
	mockClient := newMockLLMClient()
	mockClient.generateReturnsError = false
	artifactStore := business.NewInMemoryArtifactStore()
	activities := NewActivities(mockClient, artifactStore, domain.NewNoOpEventSink())

	env.RegisterActivity(activities.GenerateAnswers)

	// Execute activity
	input := createValidGenerateAnswersInput()
	val, err := env.ExecuteActivity(activities.GenerateAnswers, input)

	require.NoError(t, err)

	var result *domain.GenerateAnswersOutput
	err = val.Get(&result)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Len(t, result.Answers, 1)
	assert.NotEmpty(t, result.Answers[0].ContentRef.Key)
	assert.Greater(t, result.TokensUsed, int64(0))
}

// Test helper: failing artifact store for error scenarios
type failingArtifactStore struct {
	failures int
	calls    int
}

func (f *failingArtifactStore) Get(ctx context.Context, ref domain.ArtifactRef) (string, error) {
	return "", errors.New("artifact store get failed")
}

func (f *failingArtifactStore) Put(ctx context.Context, content string, kind domain.ArtifactKind, key string) (domain.ArtifactRef, error) {
	f.calls++
	if f.calls <= f.failures {
		return domain.ArtifactRef{}, errors.New("artifact store put failed")
	}
	return domain.ArtifactRef{Key: key, Size: int64(len(content)), Kind: kind}, nil
}

func (f *failingArtifactStore) Exists(ctx context.Context, ref domain.ArtifactRef) (bool, error) {
	return false, errors.New("artifact store exists failed")
}

func (f *failingArtifactStore) Delete(ctx context.Context, ref domain.ArtifactRef) error {
	return errors.New("artifact store delete failed")
}

// Test helper: slow mock client for cancellation testing
type slowMockLLMClient struct {
	delay time.Duration
}

func (s *slowMockLLMClient) Generate(ctx context.Context, input domain.GenerateAnswersInput) (*domain.GenerateAnswersOutput, error) {
	select {
	case <-time.After(s.delay):
		return &domain.GenerateAnswersOutput{
			Answers: []domain.Answer{
				{
					ID:         "slow-answer-1",
					ContentRef: domain.ArtifactRef{Kind: domain.ArtifactAnswer},
					Metadata: map[string]any{
						"content": "Slow generated answer",
					},
				},
			},
			TokensUsed:    100,
			CallsMade:     1,
			CostCents:     domain.Cents(25),
			ClientIdemKey: "slow-mock-client-idem-key",
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *slowMockLLMClient) Score(ctx context.Context, input domain.ScoreAnswersInput) (*domain.ScoreAnswersOutput, error) {
	return nil, errors.New("not implemented")
}

// Test helper: multi-answer mock client for testing multiple answers processing
type multiAnswerMockClient struct {
	numAnswers int
}

func (m *multiAnswerMockClient) Generate(ctx context.Context, input domain.GenerateAnswersInput) (*domain.GenerateAnswersOutput, error) {
	answers := make([]domain.Answer, input.NumAnswers)
	for i := 0; i < input.NumAnswers; i++ {
		answers[i] = domain.Answer{
			ID: fmt.Sprintf("test-answer-%d", i+1),
			ContentRef: domain.ArtifactRef{
				Key:  "", // Empty so storeAnswerContent will handle it
				Kind: domain.ArtifactAnswer,
			},
			Metadata: map[string]any{
				"content": fmt.Sprintf("This is test answer #%d", i+1),
			},
		}
	}
	return &domain.GenerateAnswersOutput{
		Answers:       answers,
		TokensUsed:    int64(100 * input.NumAnswers),
		CallsMade:     1,
		CostCents:     domain.Cents(25 * input.NumAnswers),
		ClientIdemKey: "multi-mock-client-idem-key",
	}, nil
}

func (m *multiAnswerMockClient) Score(ctx context.Context, input domain.ScoreAnswersInput) (*domain.ScoreAnswersOutput, error) {
	return nil, errors.New("not implemented")
}
