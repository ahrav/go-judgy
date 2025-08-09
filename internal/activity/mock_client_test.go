package activity

import (
	"context"
	"fmt"

	"github.com/ahrav/go-judgy/internal/domain"
)

// mockLLMClient provides controllable LLM client behavior for testing activities.
// It supports configurable error conditions, call tracking, and deterministic
// responses for validating activity error handling and retry behavior.
type mockLLMClient struct {
	// Control behavior
	generateReturnsError bool
	scoreReturnsError    bool
	generateError        error
	scoreError           error

	// Track calls
	generateCalls int
	scoreCalls    int
}

// Generate simulates LLM answer generation with controllable error behavior.
// Returns configurable errors or minimal valid responses for testing activity logic.
func (m *mockLLMClient) Generate(ctx context.Context, in domain.GenerateAnswersInput) (*domain.GenerateAnswersOutput, error) {
	m.generateCalls++

	if m.generateReturnsError {
		if m.generateError != nil {
			return nil, m.generateError
		}
		// Return the expected "not implemented" error for backward compatibility
		// This will be wrapped by the activity in a temporal.ApplicationError
		return nil, fmt.Errorf("GenerateAnswers not implemented: %w", ErrNotImplemented)
	}

	// Return a valid output with minimal data
	return &domain.GenerateAnswersOutput{
		Answers: []domain.Answer{
			{
				ID: "test-answer-1",
				ContentRef: domain.ArtifactRef{
					Key:  "test/answer.txt",
					Kind: domain.ArtifactAnswer,
				},
			},
		},
		TokensUsed: 100,
		CallsMade:  1,
	}, nil
}

// Score simulates LLM answer scoring with controllable error behavior.
// Returns configurable errors or minimal valid scores for testing activity logic.
func (m *mockLLMClient) Score(ctx context.Context, in domain.ScoreAnswersInput) (*domain.ScoreAnswersOutput, error) {
	m.scoreCalls++

	if m.scoreReturnsError {
		if m.scoreError != nil {
			return nil, m.scoreError
		}
		// Return the expected "not implemented" error for backward compatibility
		// This will be wrapped by the activity in a temporal.ApplicationError
		return nil, fmt.Errorf("ScoreAnswers not implemented: %w", ErrNotImplemented)
	}

	// Return a valid output with minimal data
	scores := make([]domain.Score, 0, len(in.Answers))
	for _, answer := range in.Answers {
		scores = append(scores, domain.Score{
			ID:         fmt.Sprintf("score-%s", answer.ID),
			AnswerID:   answer.ID,
			Value:      75,
			Confidence: 0.85,
			ScoreValidity: domain.ScoreValidity{
				Valid: true,
			},
		})
	}

	return &domain.ScoreAnswersOutput{
		Scores:     scores,
		TokensUsed: 50,
		CallsMade:  1,
	}, nil
}

// newMockLLMClient creates a mock client configured to return "not implemented" errors.
// This supports testing stub activity behavior during development phases.
func newMockLLMClient() *mockLLMClient {
	return &mockLLMClient{
		generateReturnsError: true,
		scoreReturnsError:    true,
	}
}
