package activity

import (
	"context"

	"github.com/ahrav/go-judgy/internal/domain"
)

// AggregateScores combines individual judge scores according to a configured policy.
// This activity must be pure with respect to external I/O operations.
//
// The function currently returns ErrNotImplemented as this is a stub
// implementation for Story 1.2 registration and testing purposes.
func AggregateScores(
	_ context.Context,
	_ domain.AggregateScoresInput,
) (*domain.AggregateScoresOutput, error) {
	return nil, nonRetryable("AggregateScores", ErrNotImplemented, "AggregateScores not implemented")
}
