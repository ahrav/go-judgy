package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// EventType represents the type of event emitted by the system.
// Using typed constants provides compile-time safety and enables
// exhaustive switch statements for event handling.
type EventType string

const (
	// EventTypeCandidateProduced is emitted when a candidate answer is generated and stored.
	// One event per answer with artifact references and generation metadata.
	EventTypeCandidateProduced EventType = "CandidateProduced"

	// EventTypeLLMUsage is emitted when an LLM operation completes.
	// One event per activity with aggregated usage and cost data.
	EventTypeLLMUsage EventType = "LLMUsage"
)

// EventEnvelope wraps all events with consistent metadata for projection processing.
// Provides workflow context, idempotency, sequencing, and artifact references
// that enable reliable event-driven projections and analytics.
type EventEnvelope struct {
	// IdempotencyKey ensures events are processed exactly once during retries.
	// Generated deterministically from workflow context and event content.
	IdempotencyKey string `json:"idempotency_key" validate:"required"`

	// EventType identifies the specific type of event for routing and processing.
	EventType EventType `json:"event_type" validate:"required"`

	// Version enables event schema evolution and backward compatibility.
	// Start at 1 and increment when event structure changes.
	Version int `json:"version" validate:"required,min=1"`

	// OccurredAt records when the event occurred in the system.
	// Should use workflow.Now(ctx) for deterministic time in workflows.
	OccurredAt time.Time `json:"occurred_at" validate:"required"`

	// TenantID identifies the tenant for multi-tenant event filtering.
	TenantID uuid.UUID `json:"tenant_id" validate:"required"`

	// TeamID optionally identifies the team within a tenant.
	// Enables team-level projections and analytics.
	TeamID *uuid.UUID `json:"team_id,omitempty"`

	// WorkflowID identifies the Temporal workflow that generated this event.
	WorkflowID string `json:"workflow_id" validate:"required"`

	// RunID identifies the specific workflow execution run.
	RunID string `json:"run_id" validate:"required"`

	// Sequence enables ordered event processing for projections.
	// Set to 0 for now; true monotonic sequencing added when needed.
	Sequence int `json:"sequence" validate:"min=0"`

	// ArtifactRefs contains references to related stored content.
	// Enables projections to access answer content, rationales, etc.
	ArtifactRefs []string `json:"artifact_refs,omitempty"`

	// Payload contains the event-specific data as JSON.
	// Schema varies by EventType and Version.
	Payload json.RawMessage `json:"payload" validate:"required"`

	// Producer identifies the component that emitted this event.
	// Used for debugging and event source tracking.
	Producer string `json:"producer" validate:"required"`
}

// Validate checks if the event envelope meets all requirements.
// Returns nil if valid, or a validation error describing violations.
func (e *EventEnvelope) Validate() error {
	return validate.Struct(e)
}

// CandidateProducedPayload contains the data for CandidateProduced events.
// One event per generated answer with metadata for projections.
type CandidateProducedPayload struct {
	// AnswerID uniquely identifies the generated answer.
	AnswerID string `json:"answer_id" validate:"required,uuid"`

	// Provider identifies the LLM provider used for generation.
	Provider string `json:"provider" validate:"required"`

	// Model specifies the exact model used for generation.
	Model string `json:"model" validate:"required"`

	// LatencyMs measures the generation time in milliseconds.
	LatencyMs int64 `json:"latency_ms" validate:"min=0"`

	// PromptTokens counts the input tokens for this answer.
	PromptTokens int64 `json:"prompt_tokens" validate:"min=0"`

	// CompletionTokens counts the output tokens for this answer.
	CompletionTokens int64 `json:"completion_tokens" validate:"min=0"`

	// TotalTokens equals PromptTokens plus CompletionTokens.
	TotalTokens int64 `json:"total_tokens" validate:"min=0"`

	// FinishReason indicates why generation stopped.
	FinishReason FinishReason `json:"finish_reason,omitempty"`
}

// Validate checks if the payload meets all requirements.
func (c *CandidateProducedPayload) Validate() error {
	return validate.Struct(c)
}

// LLMUsagePayload contains aggregated usage data for LLMUsage events.
// One event per activity with total resource consumption.
type LLMUsagePayload struct {
	// TotalTokens is the aggregate tokens consumed across all answers.
	TotalTokens int64 `json:"total_tokens" validate:"min=0"`

	// TotalCalls is the aggregate API calls made during generation.
	TotalCalls int64 `json:"total_calls" validate:"min=0"`

	// CostCents is the total cost in cents for budget reconciliation.
	CostCents Cents `json:"cost_cents" validate:"min=0"`

	// Provider identifies the primary LLM provider used.
	Provider string `json:"provider" validate:"required"`

	// Models lists all models used during generation.
	// Usually one model but could be multiple in advanced scenarios.
	Models []string `json:"models" validate:"required,min=1"`

	// ProviderRequestIDs contains all provider request IDs for correlation.
	ProviderRequestIDs []string `json:"provider_request_ids,omitempty"`

	// CacheHit indicates whether the request was served from cache.
	CacheHit bool `json:"cache_hit"`
}

// Validate checks if the payload meets all requirements.
func (l *LLMUsagePayload) Validate() error {
	return validate.Struct(l)
}

// NewEventEnvelope creates a new EventEnvelope with required fields populated.
// Uses provided workflow context for deterministic IDs and timestamps.
// The payload should be marshaled JSON for the specific event type.
func NewEventEnvelope(
	eventType EventType,
	tenantID uuid.UUID,
	workflowID, runID string,
	payload json.RawMessage,
	producer string,
	artifactRefs []string,
) EventEnvelope {
	return EventEnvelope{
		EventType:    eventType,
		Version:      1, // Start with version 1
		TenantID:     tenantID,
		WorkflowID:   workflowID,
		RunID:        runID,
		Sequence:     0, // Set to 0 for now as specified
		ArtifactRefs: artifactRefs,
		Payload:      payload,
		Producer:     producer,
		OccurredAt:   time.Now(), // Will be overridden with workflow.Now(ctx) in practice
	}
}

// GenerateIdempotencyKey creates a deterministic key for event deduplication.
// Combines workflow execution context with event-specific content to ensure
// that retries and replays produce identical keys for the same logical event.
//
// For CandidateProduced events: H(client_idem_key || ":cand:" || index)
// For LLMUsage events: H(client_idem_key || ":generate:1")
//
// The client_idem_key should come from the LLM client's idempotency key generation.
func GenerateIdempotencyKey(clientIdempotencyKey, eventSuffix string) string {
	hasher := sha256.New()
	hasher.Write([]byte(clientIdempotencyKey + eventSuffix))
	return hex.EncodeToString(hasher.Sum(nil))
}

// CandidateProducedIdempotencyKey generates idempotency key for candidate events.
// Uses the pattern specified in story: H(client_idem_key || ":cand:" || index).
func CandidateProducedIdempotencyKey(clientIdempotencyKey string, index int) string {
	suffix := fmt.Sprintf(":cand:%d", index)
	return GenerateIdempotencyKey(clientIdempotencyKey, suffix)
}

// LLMUsageIdempotencyKey generates idempotency key for usage events.
// Uses the pattern specified in story: H(client_idem_key || ":generate:1").
func LLMUsageIdempotencyKey(clientIdempotencyKey string) string {
	return GenerateIdempotencyKey(clientIdempotencyKey, ":generate:1")
}

// NewCandidateProducedEvent creates a CandidateProduced event envelope.
// Includes answer metadata and artifact references for projection processing.
func NewCandidateProducedEvent(
	tenantID uuid.UUID,
	workflowID, runID string,
	answer Answer,
	clientIdempotencyKey string,
	index int,
) (EventEnvelope, error) {
	payload := CandidateProducedPayload{
		AnswerID:         answer.ID,
		Provider:         answer.Provider,
		Model:            answer.Model,
		LatencyMs:        answer.LatencyMillis,
		PromptTokens:     answer.PromptTokens,
		CompletionTokens: answer.CompletionTokens,
		TotalTokens:      answer.TotalTokens,
		FinishReason:     answer.FinishReason,
	}

	if err := payload.Validate(); err != nil {
		return EventEnvelope{}, fmt.Errorf("invalid candidate produced payload: %w", err)
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return EventEnvelope{}, fmt.Errorf("failed to marshal payload: %w", err)
	}

	envelope := NewEventEnvelope(
		EventTypeCandidateProduced,
		tenantID,
		workflowID,
		runID,
		payloadJSON,
		"activity.generate_answers",
		[]string{answer.ContentRef.Key},
	)

	envelope.IdempotencyKey = CandidateProducedIdempotencyKey(clientIdempotencyKey, index)

	if err := envelope.Validate(); err != nil {
		return EventEnvelope{}, fmt.Errorf("invalid event envelope: %w", err)
	}

	return envelope, nil
}

// NewLLMUsageEvent creates an LLMUsage event envelope.
// Includes aggregated usage data for cost tracking and analytics.
func NewLLMUsageEvent(
	tenantID uuid.UUID,
	workflowID, runID string,
	output *GenerateAnswersOutput,
	provider string,
	models []string,
	providerRequestIDs []string,
	cacheHit bool,
	clientIdempotencyKey string,
	artifactRefs []string,
) (EventEnvelope, error) {
	payload := LLMUsagePayload{
		TotalTokens:        output.TokensUsed,
		TotalCalls:         output.CallsMade,
		CostCents:          output.CostCents,
		Provider:           provider,
		Models:             models,
		ProviderRequestIDs: providerRequestIDs,
		CacheHit:           cacheHit,
	}

	if err := payload.Validate(); err != nil {
		return EventEnvelope{}, fmt.Errorf("invalid LLM usage payload: %w", err)
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return EventEnvelope{}, fmt.Errorf("failed to marshal payload: %w", err)
	}

	envelope := NewEventEnvelope(
		EventTypeLLMUsage,
		tenantID,
		workflowID,
		runID,
		payloadJSON,
		"activity.generate_answers",
		artifactRefs,
	)

	envelope.IdempotencyKey = LLMUsageIdempotencyKey(clientIdempotencyKey)

	if err := envelope.Validate(); err != nil {
		return EventEnvelope{}, fmt.Errorf("invalid event envelope: %w", err)
	}

	return envelope, nil
}
