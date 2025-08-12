// Package worker exposes helpers to register workflows/activities with a Temporal worker.
package worker

import (
	sdkworker "go.temporal.io/sdk/worker"

	"github.com/ahrav/go-judgy/internal/aggregation"
	"github.com/ahrav/go-judgy/internal/generation"
	"github.com/ahrav/go-judgy/internal/llm"
	"github.com/ahrav/go-judgy/internal/scoring"
	"github.com/ahrav/go-judgy/internal/workflow"
	"github.com/ahrav/go-judgy/pkg/activity"
	"github.com/ahrav/go-judgy/pkg/events"
)

// RegisterAll registers all workflows and activities with the Temporal worker.
// This function must be called during worker initialization before starting
// the worker. The registration is not thread-safe and should only be called once
// during application startup.
//
// The function creates domain-specific activity instances with shared base
// infrastructure for common concerns like event emission and logging.
func RegisterAll(w sdkworker.Worker, llmClient llm.Client) {
	artifactStore := InitializeArtifactStore()

	eventSink := events.NewNoOpEventSink()

	base := activity.NewBaseActivities(eventSink)

	// Register domain-specific activities.
	generationActivities := generation.NewActivities(base, llmClient, artifactStore)
	scoringActivities := scoring.NewActivities(base, llmClient)
	aggregationActivities := aggregation.NewActivities(base)

	// Register workflow.
	w.RegisterWorkflow(workflow.EvaluationWorkflow)

	// Register activities from each domain.
	w.RegisterActivity(generationActivities.GenerateAnswers)
	w.RegisterActivity(scoringActivities.ScoreAnswers)
	w.RegisterActivity(aggregationActivities.AggregateScores)
}
