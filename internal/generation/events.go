// Package generation implements Temporal activities for LLM answer generation.
// It provides domain-specific activities, events, and error handling for the
// generation phase of the evaluation workflow.
package generation

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/pkg/activity"
	"github.com/ahrav/go-judgy/pkg/events"
)

// candidateProducedEvent represents a single answer generation completion.
// Emitted for each successfully generated and stored answer to enable
// fine-grained tracking and analytics of generation performance.
type candidateProducedEvent struct {
	AnswerID           string                 `json:"answer_id"`
	Model              string                 `json:"model"`
	Provider           string                 `json:"provider"`
	ContentRef         string                 `json:"content_ref"`
	ProviderRequestIDs []string               `json:"provider_request_ids"`
	LatencyMillis      int64                  `json:"latency_millis"`
	Metadata           map[string]interface{} `json:"metadata"`
	ProducedAt         time.Time              `json:"produced_at"`
	Index              int                    `json:"index"`
}

// llmUsageEvent represents aggregated resource consumption for a generation activity.
// Emitted once per GenerateAnswers activity to track total costs, token usage,
// and cache utilization for budget management and optimization.
type llmUsageEvent struct {
	TokensUsed         int64    `json:"tokens_used"`
	CallsMade          int64    `json:"calls_made"`
	CostCents          float64  `json:"cost_cents"`
	Models             []string `json:"models"`
	ProviderRequestIDs []string `json:"provider_request_ids"`
	Provider           string   `json:"provider"`
	CacheHit           bool     `json:"cache_hit"`
	ArtifactRefs       []string `json:"artifact_refs"`
	ClientIdemKey      string   `json:"client_idem_key"`
}

// EventEmitter handles event emission for the generation domain.
// It encapsulates the logic for creating and emitting domain-specific
// events with proper metadata and error handling.
type EventEmitter struct {
	base activity.BaseActivities
}

// NewEventEmitter creates a new EventEmitter with the provided base activities.
func NewEventEmitter(base activity.BaseActivities) *EventEmitter {
	return &EventEmitter{base: base}
}

// EmitCandidateProduced emits an event for each generated answer.
// This provides fine-grained observability into individual answer generation,
// including latency, model used, and artifact storage references.
func (e *EventEmitter) EmitCandidateProduced(
	ctx context.Context,
	answer domain.Answer,
	index int,
	wfCtx activity.WorkflowContext,
	clientIdemKey string,
) {
	event := candidateProducedEvent{
		AnswerID:           answer.ID,
		Model:              answer.Model,
		Provider:           answer.Provider,
		ContentRef:         answer.ContentRef.Key,
		ProviderRequestIDs: answer.ProviderRequestIDs,
		LatencyMillis:      answer.LatencyMillis,
		Metadata:           answer.Metadata,
		ProducedAt:         time.Now(),
		Index:              index,
	}

	payload, err := json.Marshal(event)
	if err != nil {
		activity.SafeLogError(ctx, "Failed to marshal candidate event",
			"answer_id", answer.ID,
			"error", err)
		return
	}

	envelope := events.Envelope{
		ID:             uuid.New().String(),
		Type:           "generation.candidate_produced",
		Source:         "generation-activity",
		Version:        "1.0.0",
		Timestamp:      time.Now(),
		IdempotencyKey: domain.CandidateProducedIdempotencyKey(clientIdemKey, index),
		TenantID:       wfCtx.TenantID,
		WorkflowID:     wfCtx.WorkflowID,
		RunID:          wfCtx.RunID,
		Payload:        payload,
	}

	e.base.EmitEventSafe(ctx, envelope, fmt.Sprintf("CandidateProduced[%s]", answer.ID))
}

// EmitLLMUsage emits an aggregated usage event for the generation activity.
// This provides cost tracking, token consumption metrics, and cache hit rates
// for budget management and provider optimization.
func (e *EventEmitter) EmitLLMUsage(
	ctx context.Context,
	result *domain.GenerateAnswersOutput,
	wfCtx activity.WorkflowContext,
	models []string,
	providerRequestIDs []string,
	provider string,
	artifactRefs []string,
) {
	event := llmUsageEvent{
		TokensUsed:         result.TokensUsed,
		CallsMade:          result.CallsMade,
		CostCents:          float64(result.CostCents),
		Models:             models,
		ProviderRequestIDs: providerRequestIDs,
		Provider:           provider,
		CacheHit:           isLikelyCacheHit(result),
		ArtifactRefs:       artifactRefs,
		ClientIdemKey:      result.ClientIdemKey,
	}

	payload, err := json.Marshal(event)
	if err != nil {
		activity.SafeLogError(ctx, "Failed to marshal usage event", "error", err)
		return
	}

	envelope := events.Envelope{
		ID:             uuid.New().String(),
		Type:           "generation.llm_usage",
		Source:         "generation-activity",
		Version:        "1.0.0",
		Timestamp:      time.Now(),
		IdempotencyKey: domain.LLMUsageIdempotencyKey(result.ClientIdemKey),
		TenantID:       wfCtx.TenantID,
		WorkflowID:     wfCtx.WorkflowID,
		RunID:          wfCtx.RunID,
		Payload:        payload,
	}

	e.base.EmitEventSafe(ctx, envelope, "LLMUsage")
}

// isLikelyCacheHit provides a heuristic to determine if the request was cached.
// Returns true if any answer has latency below the cache hit threshold (100ms).
// This is a temporary implementation until proper cache hit tracking is available
// from the LLM client layer.
func isLikelyCacheHit(result *domain.GenerateAnswersOutput) bool {
	// Responses faster than 100ms likely indicate cache hits from provider.
	const cacheHitThresholdMs = 100
	for _, answer := range result.Answers {
		if answer.LatencyMillis < cacheHitThresholdMs {
			return true
		}
	}
	return false
}
