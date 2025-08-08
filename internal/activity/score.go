package activity

import (
	"context"

	"github.com/ahrav/go-judgy/internal/domain"
)

// ScoreAnswers evaluates candidate answers using configured judge models with schema validation.
// Returns ErrNotImplemented as this is a stub implementation for Story 1.2 registration and testing.
func ScoreAnswers(
	_ context.Context,
	_ domain.ScoreAnswersInput,
) (*domain.ScoreAnswersOutput, error) {
	return nil, nonRetryable("ScoreAnswers", ErrNotImplemented, "ScoreAnswers not implemented")
}
