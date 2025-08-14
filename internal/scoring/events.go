// Package scoring implements Temporal activities for answer evaluation.
// It provides domain-specific activities, event emission, and error handling
// for scoring candidate answers using judge models within workflow contexts.
//
// The package integrates with the broader evaluation system by:
//   - Executing scoring activities within Temporal workflows
//   - Emitting domain events for observability and analytics
//   - Managing blob storage for large rationale text
//   - Handling partial failures gracefully with proper error classification
//
// Key components include Activities for Temporal integration and EventEmitter
// for domain event infrastructure compatibility.
package scoring

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/pkg/activity"
	"github.com/ahrav/go-judgy/pkg/events"
)

// EventEmitter handles domain event emission for scoring operations.
// It bridges between domain event creation and the base activity event infrastructure,
// providing type-safe event emission with proper error handling and observability.
//
// The EventEmitter handles two primary event types:
//   - Individual AnswerScored events for detailed scoring observability
//   - Aggregated ScoringUsage events for cost tracking and analytics
//
// All event emission is best-effort and failures are logged without
// affecting the core scoring activity operation.
type EventEmitter struct{ base activity.BaseActivities }

// NewEventEmitter creates a new EventEmitter with base activity infrastructure.
// The base activities provide event emission and logging capabilities.
func NewEventEmitter(base activity.BaseActivities) *EventEmitter {
	return &EventEmitter{base: base}
}

// EmitAnswerScored emits individual answer scoring events for detailed observability.
// Creates AnswerScored events with score details, confidence metrics, and resource usage.
// Event emission is best-effort and failures are logged without affecting core operations.
func (e *EventEmitter) EmitAnswerScored(
	ctx context.Context,
	score domain.Score,
	wfCtx activity.WorkflowContext,
	clientIdemKey string,
	answerIndex int,
) {
	tenantID, err := parseUUID(wfCtx.TenantID, "tenant")
	if err != nil {
		activity.SafeLogError(ctx, "Failed to parse tenant ID for AnswerScored event",
			"tenant_id", wfCtx.TenantID,
			"error", err)
		return
	}

	domainEvent, err := domain.NewAnswerScoredEvent(
		tenantID,
		wfCtx.WorkflowID,
		wfCtx.RunID,
		score,
		clientIdemKey,
		answerIndex,
	)
	if err != nil {
		activity.SafeLogError(ctx, "Failed to create AnswerScored event",
			"score_id", score.ID,
			"error", err)
		return
	}

	envelope := convertDomainEventToEnvelope(domainEvent)

	e.base.EmitEventSafe(ctx, envelope, "AnswerScored")
}

// EmitScoringUsage emits aggregated usage metrics for cost tracking and optimization.
// Aggregates resource consumption data across all scores in the batch operation.
// Event emission is best-effort and failures are logged without affecting core operations.
func (e *EventEmitter) EmitScoringUsage(
	ctx context.Context,
	output *domain.ScoreAnswersOutput,
	wfCtx activity.WorkflowContext,
	clientIdemKey string,
	judgeModels []string,
	provider string,
	providerRequestIDs []string,
) {
	tenantID, err := parseUUID(wfCtx.TenantID, "tenant")
	if err != nil {
		activity.SafeLogError(ctx, "Failed to parse tenant ID for ScoringUsage event",
			"tenant_id", wfCtx.TenantID,
			"error", err)
		return
	}

	domainEvent, err := domain.NewScoringUsageEvent(
		tenantID,
		wfCtx.WorkflowID,
		wfCtx.RunID,
		output,
		provider,
		judgeModels,
		providerRequestIDs,
		clientIdemKey,
		collectArtifactRefs(output.Scores),
	)
	if err != nil {
		activity.SafeLogError(ctx, "Failed to create ScoringUsage event",
			"workflow_id", wfCtx.WorkflowID,
			"error", err)
		return
	}

	envelope := convertDomainEventToEnvelope(domainEvent)

	e.base.EmitEventSafe(ctx, envelope, "ScoringUsage")
}

// parseUUID safely parses a string as UUID with descriptive error messages.
func parseUUID(input, context string) (uuid.UUID, error) {
	parsed, err := uuid.Parse(input)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid %s UUID '%s': %w", context, input, err)
	}
	return parsed, nil
}

// convertDomainEventToEnvelope converts domain.EventEnvelope to pkg/events.Envelope.
// This bridges the domain event system with the base activity infrastructure.
func convertDomainEventToEnvelope(domainEvent domain.EventEnvelope) events.Envelope {
	return events.Envelope{
		ID:             domainEvent.IdempotencyKey, // Use idempotency key for deterministic IDs
		Type:           string(domainEvent.EventType),
		Source:         domainEvent.Producer,
		Version:        fmt.Sprintf("%d.0.0", domainEvent.Version),
		Timestamp:      domainEvent.OccurredAt,
		IdempotencyKey: domainEvent.IdempotencyKey,
		TenantID:       domainEvent.TenantID.String(),
		WorkflowID:     domainEvent.WorkflowID,
		RunID:          domainEvent.RunID,
		Payload:        domainEvent.Payload,
	}
}

// collectArtifactRefs extracts and deduplicates artifact references from scores for event metadata.
// Returns unique artifact keys for rationales stored in blob storage.
func collectArtifactRefs(scores []domain.Score) []string {
	if len(scores) == 0 {
		return nil
	}

	refSet := make(map[string]bool)
	for _, score := range scores {
		if !score.ReasonRef.IsZero() && score.ReasonRef.Key != "" {
			refSet[score.ReasonRef.Key] = true
		}
	}

	if len(refSet) == 0 {
		return nil
	}

	refs := make([]string, 0, len(refSet))
	for ref := range refSet {
		refs = append(refs, ref)
	}
	return refs
}
