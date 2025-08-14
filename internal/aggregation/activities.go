package aggregation

import (
	"context"
	"errors"

	"go.temporal.io/sdk/temporal"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/pkg/activity"
)

// ErrNotImplemented indicates the activity has not been implemented yet.
var ErrNotImplemented = errors.New("activity not implemented")

// Activities handles aggregation-specific Temporal activities.
// It encapsulates the logic for combining individual scores into
// final verdicts according to configured aggregation policies.
type Activities struct {
	activity.BaseActivities
	events *EventEmitter
}

// NewActivities creates aggregation activities with the provided dependencies.
// The base activities provide common infrastructure for logging and event emission.
func NewActivities(base activity.BaseActivities) *Activities {
	return &Activities{
		BaseActivities: base,
		events:         NewEventEmitter(base),
	}
}

// AggregateScores combines individual judge scores according to a configured policy.
// This activity processes multiple scores to produce a final verdict that represents
// the overall evaluation result. The aggregation policy determines how scores are
// combined (e.g., mean, median, weighted average, majority vote).
//
// The operation:
// 1. Validates input parameters and scores
// 2. Applies the specified aggregation policy
// 3. Generates a verdict with confidence metrics
// 4. Emits events for observability
// 5. Returns the aggregated result
//
// This is currently a stub implementation that returns ErrNotImplemented.
// The full implementation will support various aggregation strategies including:
// - Simple averaging (mean, median)
// - Weighted averaging based on judge confidence
// - Majority voting for classification tasks
// - Outlier detection and removal
// - Ensemble methods for improved accuracy.
func (a *Activities) AggregateScores(
	ctx context.Context,
	input domain.AggregateScoresInput,
) (*domain.AggregateScoresOutput, error) {
	// Input validation
	if err := input.Validate(); err != nil {
		return nil, nonRetryable("AggregateScores", err, "invalid input")
	}

	wfCtx := a.GetWorkflowContext(ctx)
	activity.SafeLog(ctx, "Starting AggregateScores activity",
		"workflow_id", wfCtx.WorkflowID,
		"activity_id", wfCtx.ActivityID,
		"scores_to_aggregate", len(input.Scores))

	// TODO: Implement actual aggregation logic
	// For now, return not implemented error as this is a stub
	// The full implementation will:
	// 1. Apply aggregation policy to scores
	// 2. Calculate confidence metrics
	// 3. Determine final decision (pass/fail/uncertain)
	// 4. Generate verdict with metadata

	// Stub implementation for now
	return nil, nonRetryable("AggregateScores", ErrNotImplemented, "AggregateScores not implemented")

	// Example of what the full implementation would look like:
	/*
		// Apply aggregation policy
		verdict, err := a.applyAggregationPolicy(ctx, input)
		if err != nil {
			return nil, nonRetryable("AggregateScores", err, "aggregation failed")
		}

		// Build output
		output := &domain.AggregateScoresOutput{
			Verdict:       verdict,
			ClientIdemKey: generateIdempotencyKey(input),
		}

		// Validate output
		if err := output.Validate(); err != nil {
			return nil, nonRetryable("AggregateScores", err, "invalid output")
		}

		// Emit events (best-effort)
		processingTimeMs := time.Since(startTime).Milliseconds()
		a.emitAggregationEvents(ctx, output, input, wfCtx, processingTimeMs)

		activity.SafeLog(ctx, "AggregateScores completed",
			"final_score", verdict.FinalScore,
			"decision", verdict.Decision,
			"processing_time_ms", processingTimeMs)

		return output, nil
	*/
}

// // generateIdempotencyKey creates a deterministic key for the aggregation operation.
// func generateIdempotencyKey(_ domain.AggregateScoresInput) string {
// 	// In production, this would create a deterministic key based on input
// 	// For now, return a UUID
// 	return uuid.New().String()
// }

// Error helpers - wrap errors as Temporal application errors

func nonRetryable(tag string, cause error, msg string) error {
	return temporal.NewNonRetryableApplicationError(msg, tag, cause)
}
