// Package llm provides a unified, resilient HTTP client for Large Language Model providers.
package llm

import (
	"net/http"
	"time"

	"github.com/ahrav/go-judgy/internal/domain"
)

// OperationType differentiates between generation and scoring operations.
// Affects rate limiting quotas, cache key namespacing, metrics labeling,
// and timeout configuration for operation-specific resource management.
type OperationType string

const (
	// OpGeneration indicates answer generation from a question.
	OpGeneration OperationType = "generation"

	// OpScoring indicates evaluation of answers by a judge.
	OpScoring OperationType = "scoring"
)

// Request represents a normalized request across all LLM providers.
// Contains all information needed for provider-specific HTTP request construction,
// middleware processing, and response correlation with proper tracing context.
type Request struct {
	// Operation type affects routing, metrics, and rate limiting.
	Operation OperationType `json:"operation"`

	// Provider identifies which LLM service to use.
	Provider string `json:"provider"` // "openai"|"anthropic"|"google"

	// Model specifies the exact model version to use.
	Model string `json:"model"`

	// TenantID enables per-tenant isolation and tracking.
	TenantID string `json:"tenant_id"`

	// Question is the prompt for generation operations.
	Question string `json:"question,omitempty"`

	// Answers provides context for scoring operations.
	Answers []domain.Answer `json:"answers,omitempty"`

	// Generation parameters control model behavior.
	MaxTokens   int64   `json:"max_tokens"`
	Temperature float64 `json:"temperature"`
	Seed        *int64  `json:"seed,omitempty"`

	// System prompt provides instructions to the model.
	SystemPrompt string `json:"system_prompt,omitempty"`

	// Control fields for resilience and observability.
	Timeout        time.Duration     `json:"timeout"`
	IdempotencyKey string            `json:"idempotency_key"`
	TraceID        string            `json:"trace_id"`
	Metadata       map[string]string `json:"metadata,omitempty"`

	// Artifact store for content retrieval (not serialized).
	ArtifactStore ArtifactStore `json:"-"`
}

// LLMResponse represents normalized output from any LLM provider.
// Provides consistent response structure across providers that activities
// translate into domain-specific types with usage tracking and cost attribution.
type LLMResponse struct {
	// Content is the generated text or JSON for scoring.
	Content string `json:"content"`

	// FinishReason indicates why generation stopped.
	FinishReason domain.FinishReason `json:"finish_reason"`

	// ProviderRequestIDs enables cross-system correlation.
	ProviderRequestIDs []string `json:"provider_request_ids"`

	// Usage tracks resource consumption.
	Usage NormalizedUsage `json:"usage"`

	// EstimatedCost in milli-cents for precise accounting.
	EstimatedCostMilliCents int64 `json:"estimated_cost_milli_cents"`

	// Headers preserves raw response headers for debugging.
	Headers http.Header `json:"-"`

	// RawBody preserves the original response for audit.
	RawBody []byte `json:"raw_body"`
}

// NormalizedUsage provides consistent usage metrics across all providers.
// Normalizes provider-specific token counting and timing into standard format
// for cost calculation, monitoring, and resource management.
type NormalizedUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	LatencyMs        int64 `json:"latency_ms"`
}
