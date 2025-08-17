// Package aggregation implements Temporal activities for score aggregation.
// It provides domain-specific activities and events for combining individual
// judge scores into final verdicts according to configured policies.
package aggregation

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/pkg/activity"
	"github.com/ahrav/go-judgy/pkg/events"
)

// EventEmitter handles event emission for the aggregation domain.
// It encapsulates the logic for creating and emitting aggregation-specific
// events with proper metadata and error handling.
type EventEmitter struct {
	base activity.BaseActivities
}

// NewEventEmitter creates a new EventEmitter with the provided base activities.
// The base activities provide event emission and logging infrastructure.
func NewEventEmitter(base activity.BaseActivities) *EventEmitter {
	return &EventEmitter{base: base}
}

// EmitVerdictReached emits a VerdictReached event with aggregation results.
// It creates events with complete aggregation metadata for downstream projections.
// Event emission is best-effort; failures are logged without affecting core operations.
func (e *EventEmitter) EmitVerdictReached(
	ctx context.Context,
	verdictID string,
	output *domain.AggregateScoresOutput,
	scoreIDs []string,
	artifactRefs []string,
	wfCtx activity.WorkflowContext,
	clientIdemKey string,
) {
	tenantID, err := parseUUID(wfCtx.TenantID, "tenant")
	if err != nil {
		activity.SafeLogError(ctx, "Failed to parse tenant ID for VerdictReached event",
			"tenant_id", wfCtx.TenantID,
			"error", err)
		return
	}

	domainEvent, err := domain.NewVerdictReachedEvent(
		tenantID,
		wfCtx.WorkflowID,
		wfCtx.RunID,
		verdictID,
		output,
		scoreIDs,
		artifactRefs,
		clientIdemKey,
	)
	if err != nil {
		activity.SafeLogError(ctx, "Failed to create VerdictReached event",
			"verdict_id", verdictID,
			"error", err)
		return
	}

	envelope := convertDomainEventToEnvelope(domainEvent)

	e.base.EmitEventSafe(ctx, envelope, fmt.Sprintf("VerdictReached[%s]", verdictID))
}

// parseUUID safely parses a string as UUID with descriptive error messages.
// It handles the special case of "default" for test contexts, returning a deterministic UUID.
func parseUUID(input, context string) (uuid.UUID, error) {
	parsed, err := uuid.Parse(input)
	if err != nil {
		// For test contexts where TenantID might be "default"
		if input == "default" {
			return uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"), nil
		}
		return uuid.Nil, fmt.Errorf("invalid %s UUID '%s': %w", context, input, err)
	}
	return parsed, nil
}

// convertDomainEventToEnvelope converts domain.EventEnvelope to events.Envelope.
// This bridges the domain event system with the base activity infrastructure,
// mapping domain-specific fields to the generic event envelope format.
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
