// Package domain provides core types and business logic for LLM evaluation processes.
// It defines evaluation requests, configurations, and supporting types used
// throughout the system. The types are designed to support reproducible,
// auditable evaluation processes with resource constraints.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// Default configuration values for evaluations.
const (
	defaultMaxAnswers      = int64(3)
	defaultMaxAnswerTokens = int64(1000)
	defaultTemperature     = 0.7
	defaultScoreThreshold  = 0.7
	defaultTimeout         = int64(60)
)

// EvaluationRequest initiates a process for evaluating LLM-generated answers
// to a given question. It includes the question, evaluation configuration,
// resource limits, and metadata for tracking and auditing purposes.
// The request serves as the primary input to evaluation processes.
type EvaluationRequest struct {
	// ID uniquely identifies this evaluation request using UUID format.
	// Generated automatically by NewEvaluationRequest or provided explicitly.
	ID string `json:"id" validate:"required,uuid"`

	// Question contains the prompt to be evaluated.
	// Length constraints ensure reasonable processing time and token usage (10-1000 chars).
	Question string `json:"question" validate:"required,min=10,max=1000"`

	// Config contains the evaluation configuration parameters.
	// Must be a valid EvalConfig with all required fields.
	Config EvalConfig `json:"config" validate:"required"`

	// BudgetLimits defines resource constraints for this evaluation.
	// Prevents runaway costs and ensures predictable resource usage.
	BudgetLimits BudgetLimits `json:"budget_limits" validate:"required"`

	// Metadata contains optional key-value pairs for tracking and auditing.
	// Commonly used for request context, source system, or correlation IDs.
	// For thread safety, use WithMeta() or WithMetadata() methods to modify.
	Metadata map[string]string `json:"metadata,omitempty"`

	// RequestedBy identifies the user or service that initiated this request.
	// Supports both human users and automated service principals.
	RequestedBy Principal `json:"requested_by" validate:"required"`

	// RequestedAt records when this evaluation request was created.
	// Used for audit trails and request ordering.
	RequestedAt time.Time `json:"requested_at" validate:"required"`
}

// NewEvaluationRequest creates a new evaluation request with validation.
// It generates a UUID for the ID and sets the current time for RequestedAt.
//
// WARNING: Do not call this function inside evaluation processes as it uses
// nondeterministic operations (uuid.New() and time.Now()).
// Use MakeEvaluationRequest instead for process-safe operations.
//
// Returns the created request or an error if validation fails.
func NewEvaluationRequest(
	question string,
	requestedBy Principal,
	config EvalConfig,
	limits BudgetLimits,
) (*EvaluationRequest, error) {
	req := &EvaluationRequest{
		ID:           uuid.New().String(),
		Question:     question,
		Config:       config,
		BudgetLimits: limits,
		RequestedBy:  requestedBy,
		RequestedAt:  time.Now(),
		Metadata:     make(map[string]string),
	}

	if err := validate.Struct(req); err != nil {
		return nil, err
	}

	return req, nil
}

// MakeEvaluationRequest creates a new evaluation request with provided ID and timestamp.
// This function is safe to use inside evaluation processes as it does not generate
// any nondeterministic values. The ID and timestamp must be provided by the caller,
// typically using process.Now(ctx) for time and a pre-generated UUID for the ID.
//
// Returns the created request or an error if validation fails.
func MakeEvaluationRequest(
	id string,
	requestedAt time.Time,
	question string,
	requestedBy Principal,
	config EvalConfig,
	limits BudgetLimits,
) (*EvaluationRequest, error) {
	req := &EvaluationRequest{
		ID:           id,
		Question:     question,
		Config:       config,
		BudgetLimits: limits,
		RequestedBy:  requestedBy,
		RequestedAt:  requestedAt,
		Metadata:     make(map[string]string),
	}

	if err := validate.Struct(req); err != nil {
		return nil, err
	}

	return req, nil
}

// Validate checks if the evaluation request meets all requirements.
// Returns nil if valid, or a validation error describing the first constraint violation.
func (r *EvaluationRequest) Validate() error { return validate.Struct(r) }

// WithMeta returns a copy of the EvaluationRequest with the specified metadata key-value pair added.
// This method preserves immutability by creating a new request with cloned metadata.
// If the key already exists, it will be overwritten in the returned copy.
func (r *EvaluationRequest) WithMeta(key, value string) *EvaluationRequest {
	// Clone the current metadata and add the new key-value pair
	newMetadata := cloneStringMap(r.Metadata)
	if newMetadata == nil {
		newMetadata = make(map[string]string)
	}
	newMetadata[key] = value

	// Return a copy with the updated metadata
	reqCopy := *r
	reqCopy.Metadata = newMetadata
	return &reqCopy
}

// WithMetadata returns a copy of the EvaluationRequest with the specified metadata merged in.
// This method preserves immutability by creating a new request with cloned metadata.
// Existing keys will be overwritten by values from the provided metadata map.
func (r *EvaluationRequest) WithMetadata(metadata map[string]string) *EvaluationRequest {
	// Start with a clone of existing metadata
	newMetadata := cloneStringMap(r.Metadata)
	if newMetadata == nil {
		newMetadata = make(map[string]string)
	}

	// Merge in the new metadata
	for k, v := range metadata {
		newMetadata[k] = v
	}

	// Return a copy with the updated metadata
	reqCopy := *r
	reqCopy.Metadata = newMetadata
	return &reqCopy
}

// EvalConfig defines the configuration parameters for an evaluation.
// It controls how many answers to generate, token limits, temperature settings,
// and which LLM provider to use. The configuration is vendor-agnostic,
// allowing different LLM providers to be used interchangeably.
type EvalConfig struct {
	// MaxAnswers specifies how many answers to generate (1-10).
	// More answers increase cost but provide better evaluation coverage.
	MaxAnswers int64 `json:"max_answers" validate:"required,min=1,max=10"`

	// MaxAnswerTokens limits the token count per answer (50-4000).
	// Higher limits allow longer responses but increase cost and latency.
	MaxAnswerTokens int64 `json:"max_answer_tokens" validate:"required,min=50,max=4000"`

	// Temperature controls randomness in generation (0-2).
	// Lower values (0.0-0.3) are deterministic, higher values (0.7-2.0) are creative.
	Temperature float64 `json:"temperature" validate:"required,min=0,max=2"`

	// ScoreThreshold defines the minimum acceptable score (0-1).
	// Scores below this threshold may trigger human review or retries.
	ScoreThreshold float64 `json:"score_threshold" validate:"required,min=0,max=1"`

	// Provider specifies which LLM provider to use.
	// Common values: "openai", "anthropic", "google", "aws".
	// Validation is performed at the LLM client layer for vendor independence.
	Provider string `json:"provider" validate:"required,min=1"`

	// Model optionally specifies a particular model from the provider.
	// If empty, the provider's default model is used.
	// Examples: "gpt-4", "claude-3-sonnet", "gemini-pro".
	Model string `json:"model,omitempty"`

	// Timeout specifies the maximum time in seconds for each LLM call (10-300).
	// Should account for model complexity and expected response length.
	Timeout int64 `json:"timeout" validate:"required,min=10,max=300"`
}

// DefaultEvalConfig returns a default evaluation configuration with sensible values.
// The defaults are:
//   - MaxAnswers: 3 (balance between quality and cost)
//   - MaxAnswerTokens: 1000 (reasonable response length)
//   - Temperature: 0.7 (balanced creativity and consistency)
//   - ScoreThreshold: 0.7 (quality threshold for acceptance)
//   - Provider: "openai" (default provider)
//   - Timeout: 60 seconds (reasonable response time)
func DefaultEvalConfig() EvalConfig {
	return EvalConfig{
		MaxAnswers:      defaultMaxAnswers,
		MaxAnswerTokens: defaultMaxAnswerTokens,
		Temperature:     defaultTemperature,
		ScoreThreshold:  defaultScoreThreshold,
		Provider:        "openai",
		Timeout:         defaultTimeout,
	}
}

// Validate checks if the evaluation configuration meets all requirements.
// Returns nil if valid, or a validation error describing the first constraint violation.
func (c *EvalConfig) Validate() error { return validate.Struct(c) }

// IsValidProvider checks if the provider string is not empty.
// The domain layer maintains vendor agnosticism; specific provider validation
// should be performed at the LLM client layer.
func (c *EvalConfig) IsValidProvider() bool { return c.Provider != "" }
