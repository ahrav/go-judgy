// Package events provides the generic event infrastructure for domain event emission.
// It defines the Envelope type for wrapping domain events with consistent metadata
// and the EventSink interface for event storage/transmission.
package events

import (
	"context"
	"encoding/json"
	"time"
)

// Envelope wraps domain events with consistent metadata for reliable event processing.
// This provides a generic container that can hold any domain-specific event payload
// while maintaining standard fields for routing, idempotency, and observability.
//
// The envelope pattern enables:
// - Schema evolution through versioning
// - Event deduplication via idempotency keys
// - Multi-tenant event filtering
// - Workflow execution tracking
// - Cross-domain event correlation.
type Envelope struct {
	// ID uniquely identifies this event instance.
	// Generated as a UUID for each event emission.
	ID string `json:"id"`

	// Type identifies the event for routing and processing.
	// Examples: "generation.candidate_produced", "scoring.answer_scored"
	Type string `json:"type"`

	// Source identifies the component that emitted this event.
	// Examples: "generation-activity", "scoring-activity"
	Source string `json:"source"`

	// Version enables schema evolution and backward compatibility.
	// Start at "1.0.0" and increment following semantic versioning.
	Version string `json:"version"`

	// Timestamp records when the event was emitted.
	// Use time.Now() for wall clock time in activities.
	Timestamp time.Time `json:"timestamp"`

	// IdempotencyKey ensures exactly-once processing during retries.
	// Generated deterministically from workflow context and event content.
	IdempotencyKey string `json:"idempotency_key"`

	// TenantID identifies the tenant for multi-tenant filtering.
	// Enables tenant-specific projections and analytics.
	TenantID string `json:"tenant_id"`

	// WorkflowID identifies the Temporal workflow that triggered this event.
	// Used for correlation and debugging.
	WorkflowID string `json:"workflow_id"`

	// RunID identifies the specific workflow execution run.
	// Distinguishes between retries of the same workflow.
	RunID string `json:"run_id"`

	// Payload contains the domain-specific event data as JSON.
	// Schema varies by Type and Version.
	Payload json.RawMessage `json:"payload"`
}

// EventSink defines the interface for emitting events to downstream consumers.
// Implementations could include database outbox patterns, message queues,
// event streaming platforms, or even simple file/log outputs.
//
// The interface is designed to be:
// - Simple to implement and test
// - Async-friendly with context support
// - Failure-tolerant (errors don't break workflows)
// - Extensible for different sink types.
type EventSink interface {
	// Append adds an event to the sink with best-effort delivery.
	// Implementations should handle idempotency (duplicate events are no-ops)
	// and return quickly to avoid blocking the caller.
	//
	// Returns error if the event cannot be queued, but callers should
	// not fail their primary operation due to event sink failures.
	// Events are important for observability but not critical for correctness.
	Append(ctx context.Context, envelope Envelope) error
}

// NoOpEventSink is a null implementation of EventSink for testing or when events are disabled.
// All Append calls succeed immediately without side effects.
type NoOpEventSink struct{}

// Append implements EventSink.Append with no-op behavior.
func (n *NoOpEventSink) Append(_ context.Context, _ Envelope) error {
	return nil // Always succeeds
}

// NewNoOpEventSink creates a new no-op event sink.
// Useful for testing or when event emission should be disabled.
func NewNoOpEventSink() EventSink {
	return &NoOpEventSink{}
}
