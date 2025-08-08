// Package workflow orchestrates LLM evaluation using Temporal workflows.
// It defines deterministic control flow with budget awareness and
// clean separation of concerns: Generate → Score → Aggregate.
package workflow

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/ahrav/go-judgy/internal/domain"
)

// EvaluationWorkflow orchestrates answer generation, scoring, and aggregation
// with deterministic execution. All workflow code must use workflow-safe APIs only.
//
// The current implementation validates input and establishes activity options
// but returns ErrNotImplemented as business logic is deferred to later stories.
// Story 1.2 provides the skeleton framework with proper error handling patterns.
func EvaluationWorkflow(
	ctx workflow.Context,
	req domain.EvaluationRequest,
) (*domain.Verdict, error) {
	// Version gate enables safe evolution and backward compatibility.
	const currentVersion = 1
	_ = workflow.GetVersion(ctx, "evaluation.v", workflow.DefaultVersion, currentVersion)

	// Validate request early to fail fast on invalid input.
	if err := req.Validate(); err != nil {
		return nil, temporal.NewNonRetryableApplicationError(
			"invalid evaluation request",
			"Validation",
			err,
		)
	}

	// Configure standard timeouts and retry policy for all activities.
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(req.Config.Timeout) * time.Second,
		HeartbeatTimeout:    30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    time.Minute,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Future stories will implement the complete evaluation pipeline:
	// 1. Budget reservation and management
	// 2. Activity execution (GenerateAnswers → ScoreAnswers → AggregateScores)
	// 3. Verdict construction and validation
	return nil, temporal.NewNonRetryableApplicationError(
		"EvaluationWorkflow not implemented (Story 1.2 contract only)",
		"NotImplemented",
		nil,
	)
}
