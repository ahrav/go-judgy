// Package activity provides common infrastructure for all Temporal activity implementations.
// It includes base types, context extraction, safe logging, and event emission utilities
// that are shared across all domain-specific activity packages.
package activity

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"

	"github.com/ahrav/go-judgy/pkg/events"
)

// WorkflowContext contains metadata extracted from the Temporal activity context.
// This provides a consistent way to access workflow execution information across
// all activities, with fallback values for testing scenarios.
type WorkflowContext struct {
	WorkflowID string
	RunID      string
	TenantID   string
	ActivityID string
}

// BaseActivities provides common infrastructure for all activity types.
// It handles event emission, context extraction, and safe logging in a way
// that works both in Temporal activity contexts and test environments.
type BaseActivities struct {
	eventSink events.EventSink
}

// NewBaseActivities creates a new BaseActivities instance with the provided event sink.
// The event sink can be nil for testing scenarios where event emission is not needed.
func NewBaseActivities(sink events.EventSink) BaseActivities {
	return BaseActivities{eventSink: sink}
}

// GetWorkflowContext safely extracts workflow context from the activity context.
// In a Temporal activity context, it returns the actual workflow execution details.
// In test contexts (where activity.GetInfo would panic), it generates test IDs.
// This allows activities to work seamlessly in both production and test environments.
func (b *BaseActivities) GetWorkflowContext(ctx context.Context) WorkflowContext {
	var wfCtx WorkflowContext

	func() {
		defer func() {
			if r := recover(); r != nil {
				// Test context - generate deterministic test IDs for idempotency testing.
				// Use a fixed UUID workflow ID for consistency across test runs.
				wfCtx.WorkflowID = "550e8400-e29b-41d4-a716-446655440000"
				wfCtx.RunID = "test-run-" + uuid.New().String()[:8]
				wfCtx.TenantID = "550e8400-e29b-41d4-a716-446655440000" // Valid test UUID
				wfCtx.ActivityID = "test-activity"
			}
		}()

		info := activity.GetInfo(ctx)
		wfCtx.WorkflowID = info.WorkflowExecution.ID
		wfCtx.RunID = info.WorkflowExecution.RunID
		wfCtx.ActivityID = info.ActivityID
		wfCtx.TenantID = "default" // TODO: Extract from workflow metadata when multi-tenancy is implemented
	}()

	return wfCtx
}

// EmitEventSafe provides best-effort event emission with automatic retry.
// Events are critical for system observability and projections, but their
// emission should not fail the primary activity operation. This method
// implements a short retry with exponential backoff for transient failures.
//
// The method will:
// - Skip emission if eventSink is nil (testing scenario)
// - Retry up to 2 times with 200ms delay
// - Log success or failure without propagating errors
// - Return quickly to avoid blocking the caller.
func (b *BaseActivities) EmitEventSafe(
	ctx context.Context,
	envelope events.Envelope,
	description string,
) {
	if b.eventSink == nil {
		return // Testing scenario without event sink
	}

	const maxAttempts = 2
	const retryDelay = 200 * time.Millisecond

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(retryDelay):
			case <-ctx.Done():
				SafeLogError(ctx, fmt.Sprintf("Event emission cancelled: %s", description),
					"event_type", envelope.Type)
				return
			}
		}

		if err := b.eventSink.Append(ctx, envelope); err != nil {
			lastErr = err
			continue
		}

		SafeLog(ctx, fmt.Sprintf("Event emitted: %s", description),
			"event_type", envelope.Type,
			"idempotency_key", envelope.IdempotencyKey)
		return
	}

	SafeLogError(ctx, fmt.Sprintf("Failed to emit %s after %d attempts", description, maxAttempts),
		"event_type", envelope.Type,
		"error", lastErr)
}

// RecordHeartbeat safely records a heartbeat in the Temporal activity context.
// This method is safe to call in non-activity contexts where it will be ignored.
func (b *BaseActivities) RecordHeartbeat(ctx context.Context, details ...any) {
	RecordHeartbeat(ctx, details...)
}

// SafeLog performs context-safe logging that works in both activity and test contexts.
// In a Temporal activity context, it uses the activity logger for structured logging.
// In test contexts, it silently ignores the log call to avoid panics.
// This allows activities to include comprehensive logging without breaking tests.
func SafeLog(ctx context.Context, msg string, keyvals ...any) {
	defer func() {
		if recover() != nil {
			// Not an activity context, ignore
		}
	}()
	activity.GetLogger(ctx).Info(msg, keyvals...)
}

// SafeLogError performs context-safe error logging that works in both activity and test contexts.
// Similar to SafeLog, but logs at ERROR level for operational visibility of problems.
func SafeLogError(ctx context.Context, msg string, keyvals ...any) {
	defer func() {
		if recover() != nil {
			// Not an activity context, ignore
		}
	}()
	activity.GetLogger(ctx).Error(msg, keyvals...)
}

// RecordHeartbeat safely records activity heartbeat with details.
// Heartbeats are important for long-running activities to indicate progress
// and prevent timeouts. This method safely handles non-activity contexts.
func RecordHeartbeat(ctx context.Context, details ...any) {
	defer func() {
		if recover() != nil {
			// Not an activity context, ignore
		}
	}()
	activity.RecordHeartbeat(ctx, details...)
}
