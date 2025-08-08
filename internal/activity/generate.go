package activity

import (
	"context"

	"github.com/ahrav/go-judgy/internal/domain"
)

// GenerateAnswers produces candidate answers via configured LLM providers.
//
// The function currently returns ErrNotImplemented as this is a stub
// implementation for Story 1.2 registration and testing purposes.
func GenerateAnswers(
	_ context.Context,
	_ domain.GenerateAnswersInput,
) (*domain.GenerateAnswersOutput, error) {
	return nil, nonRetryable("GenerateAnswers", ErrNotImplemented, "GenerateAnswers not implemented")
}
