// Package worker exposes helpers to register workflows/activities with a Temporal worker.
package worker

import (
	"github.com/ahrav/go-judgy/internal/activity"
	"github.com/ahrav/go-judgy/internal/llm"
	"github.com/ahrav/go-judgy/internal/workflow"
	sdkworker "go.temporal.io/sdk/worker"
)

// RegisterAll registers all workflows and activities with the Temporal worker.
// This function must be called during worker initialization before starting
// the worker. The registration is not thread-safe and should only be called once
// during application startup.
func RegisterAll(w sdkworker.Worker, llmClient llm.Client) {
	activities := activity.NewActivities(llmClient)

	w.RegisterWorkflow(workflow.EvaluationWorkflow)
	w.RegisterActivity(activities.GenerateAnswers)
	w.RegisterActivity(activities.ScoreAnswers)
	w.RegisterActivity(activity.AggregateScores)
}
