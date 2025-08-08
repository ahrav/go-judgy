// Package domain provides core types and business logic for LLM evaluation processes.
// It defines evaluation requests, configurations, budget limits, and artifact references
// used throughout the system. The types are designed to support reproducible,
// auditable evaluation processes with resource constraints.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
)

// ArtifactKind represents the type of content stored in an artifact.
// Using typed constants instead of raw strings provides compile-time safety
// and prevents typos that could bypass validation.
type ArtifactKind string

const (
	// ArtifactAnswer represents LLM-generated answer content.
	ArtifactAnswer ArtifactKind = "answer"

	// ArtifactJudgeRationale represents scoring rationale from evaluation judges.
	ArtifactJudgeRationale ArtifactKind = "judge_rationale"

	// ArtifactRawPrompt represents rendered prompt text sent to LLMs.
	ArtifactRawPrompt ArtifactKind = "raw_prompt"
)

// BudgetType represents the type of budget limit that can be exceeded.
// Using typed constants provides compile-time safety and enables exhaustive
// switch statements for budget violation handling.
type BudgetType uint8

const (
	// BudgetTokens represents token consumption limits.
	BudgetTokens BudgetType = iota

	// BudgetCalls represents API call count limits.
	BudgetCalls

	// BudgetCost represents monetary cost limits.
	BudgetCost

	// BudgetTime represents execution time limits.
	BudgetTime
)

// String returns the string representation of a BudgetType.
func (b BudgetType) String() string {
	switch b {
	case BudgetTokens:
		return "tokens"
	case BudgetCalls:
		return "calls"
	case BudgetCost:
		return "cost"
	case BudgetTime:
		return "time"
	default:
		return "unknown"
	}
}

// PrincipalType represents the type of entity that can initiate evaluation requests.
// This supports both human users and automated service principals.
type PrincipalType string

const (
	// PrincipalUser represents human users who initiate evaluations.
	PrincipalUser PrincipalType = "user"

	// PrincipalService represents automated services or systems.
	PrincipalService PrincipalType = "service"
)

// Principal represents an entity (user or service) that can request evaluations.
// This flexible design supports both human users and service principals,
// avoiding the limitation of email-only identification.
type Principal struct {
	// Type indicates whether this is a user or service principal.
	Type PrincipalType `json:"type" validate:"required,oneof=user service"`

	// ID uniquely identifies the principal.
	// For users: email address, username, or user ID
	// For services: service name, URN, or service account identifier
	ID string `json:"id" validate:"required,min=1"`
}

// String returns a human-readable representation of the principal.
func (p Principal) String() string { return fmt.Sprintf("%s:%s", p.Type, p.ID) }

// Default configuration values for evaluations and budget limits.
const (
	defaultMaxAnswers      = int64(3)
	defaultMaxAnswerTokens = int64(1000)
	defaultTemperature     = 0.7
	defaultScoreThreshold  = 0.7
	defaultTimeout         = int64(60)

	defaultMaxTotalTokensLimit = 10000
	defaultMaxCallsLimit       = 10
	defaultMaxCostCentsLimit   = 100
	defaultTimeoutSecsLimit    = 300
)

// Common evaluation errors returned by domain operations.
var (
	// ErrInvalidRequest indicates that an evaluation request contains invalid data.
	ErrInvalidRequest = errors.New("invalid evaluation request")

	// ErrInvalidConfig indicates that the evaluation configuration is invalid.
	ErrInvalidConfig = errors.New("invalid evaluation configuration")

	// ErrInvalidBudget indicates that budget limits are invalid or insufficient.
	ErrInvalidBudget = errors.New("invalid budget limits")

	// ErrInvalidArtifactKind indicates that the artifact kind is not valid for the operation.
	ErrInvalidArtifactKind = errors.New("invalid artifact kind")

	// ErrInvalidPromptSpec indicates that the prompt specification validation failed.
	ErrInvalidPromptSpec = errors.New("prompt spec validation failed")
)

// validate is the package-level validator instance used for struct validation.
var validate = validator.New(validator.WithRequiredStructEnabled())

// ArtifactRef represents a reference to content stored in the blob/artifact store.
// This design keeps evaluation process audit trail lightweight by storing large text content
// externally while maintaining references for audit trails and content retrieval.
type ArtifactRef struct {
	// Key is the unique identifier for the stored artifact (e.g., "answers/2025/08/<id>.txt").
	// Must be a valid storage key path for the configured blob store.
	Key string `json:"key" validate:"required"`

	// Size is the size of the stored content in bytes.
	// Used for storage accounting and retrieval optimization.
	Size int64 `json:"size" validate:"min=0"`

	// Kind categorizes the type of content stored.
	// Must be one of the defined ArtifactKind constants.
	Kind ArtifactKind `json:"kind" validate:"required,oneof=answer judge_rationale raw_prompt"`
}

// Validate checks if the artifact reference meets all requirements.
// Returns nil if valid, or a validation error describing the first constraint violation.
func (a *ArtifactRef) Validate() error { return validate.Struct(a) }

// PromptSpec defines a reproducible prompt configuration for auditing and debugging.
// It ensures prompt reproducibility by capturing both the template and rendered output,
// enabling audit trails and debugging of evaluation results.
type PromptSpec struct {
	// Template is the base prompt template with placeholders.
	// Should use standard templating syntax for variable substitution.
	Template string `json:"template" validate:"required"`

	// Variables contains the values used to fill template placeholders.
	// Keys must match placeholder names in the template.
	Variables map[string]string `json:"variables"`

	// Hash is a deterministic hash of the final rendered prompt.
	// Used to ensure reproducibility and detect template changes.
	Hash string `json:"hash" validate:"required"`

	// RenderedRef references the final rendered prompt stored as an artifact.
	// The artifact Kind must be "raw_prompt".
	RenderedRef ArtifactRef `json:"rendered_ref" validate:"required"`
}

// Validate checks if the prompt specification meets all requirements.
// Returns nil if valid, or a validation error describing the first constraint violation.
func (p *PromptSpec) Validate() error { return validate.Struct(p) }

// NewPromptSpec creates a new PromptSpec with computed hash and validation.
// It ensures integrity by computing the hash from the rendered content and
// validates that the RenderedRef.Kind is ArtifactRawPrompt.
//
// The hash is computed as SHA-256 of the rendered prompt content for reproducibility.
// Variables map is cloned to prevent aliasing and mutation issues.
//
// Returns an error if:
//   - RenderedRef.Kind is not ArtifactRawPrompt
//   - Rendered content cannot be accessed (future implementation)
//   - Validation constraints are violated
func NewPromptSpec(template string, variables map[string]string, renderedContent string, renderedRef ArtifactRef) (PromptSpec, error) {
	// Validate that the artifact reference is for a raw prompt
	if renderedRef.Kind != ArtifactRawPrompt {
		return PromptSpec{}, fmt.Errorf("rendered_ref.kind must be %q, got %q: %w", ArtifactRawPrompt, renderedRef.Kind, ErrInvalidArtifactKind)
	}

	// Compute deterministic hash from rendered content
	hasher := sha256.New()
	hasher.Write([]byte(renderedContent))
	hash := hex.EncodeToString(hasher.Sum(nil))

	// Clone variables to prevent aliasing issues
	varsCopy := cloneStringMap(variables)

	spec := PromptSpec{
		Template:    template,
		Variables:   varsCopy,
		Hash:        hash,
		RenderedRef: renderedRef,
	}

	// Validate the constructed spec
	if err := spec.Validate(); err != nil {
		return PromptSpec{}, fmt.Errorf("%w: %w", ErrInvalidPromptSpec, err)
	}

	return spec, nil
}

// cloneStringMap creates a deep copy of a string map to prevent aliasing.
// Returns nil for nil input to maintain consistency.
func cloneStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	result := make(map[string]string, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}

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
func NewEvaluationRequest(question string, requestedBy Principal, config EvalConfig, limits BudgetLimits) (*EvaluationRequest, error) {
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
func MakeEvaluationRequest(id string, requestedAt time.Time, question string, requestedBy Principal, config EvalConfig, limits BudgetLimits) (*EvaluationRequest, error) {
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
func (r *EvaluationRequest) Validate() error {
	return validate.Struct(r)
}

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
func (c *EvalConfig) Validate() error {
	return validate.Struct(c)
}

// IsValidProvider checks if the provider string is not empty.
// The domain layer maintains vendor agnosticism; specific provider validation
// should be performed at the LLM client layer.
func (c *EvalConfig) IsValidProvider() bool {
	return c.Provider != ""
}

// BudgetLimits defines resource constraints for an evaluation.
// These limits prevent runaway costs and ensure predictable resource usage
// across token consumption, API calls, financial costs, and execution time.
type BudgetLimits struct {
	// MaxTotalTokens limits the total tokens that can be consumed (minimum 100).
	// Includes input and output tokens across all API calls.
	MaxTotalTokens int64 `json:"max_total_tokens" validate:"required,min=100"`

	// MaxCalls limits the number of LLM API calls (minimum 1).
	// Includes both answer generation and scoring/judging calls.
	MaxCalls int64 `json:"max_calls" validate:"required,min=1"`

	// MaxCostCents limits the total cost using the Cents type (minimum 1).
	// Calculated based on provider pricing for tokens consumed.
	MaxCostCents Cents `json:"max_cost_cents" validate:"required,min=1"`

	// TimeoutSecs sets the overall timeout in seconds (minimum 30).
	// Covers the entire evaluation process, not individual API calls.
	TimeoutSecs int64 `json:"timeout_secs" validate:"required,min=30"`
}

// DefaultBudgetLimits returns default budget limits with reasonable constraints.
// The defaults are:
//   - MaxTotalTokens: 10000 (generous token allowance)
//   - MaxCalls: 10 (reasonable API call limit)
//   - MaxCostCents: 100 (1 USD cost limit)
//   - TimeoutSecs: 300 (5 minute timeout)
func DefaultBudgetLimits() BudgetLimits {
	return BudgetLimits{
		MaxTotalTokens: defaultMaxTotalTokensLimit,
		MaxCalls:       defaultMaxCallsLimit,
		MaxCostCents:   Cents(defaultMaxCostCentsLimit),
		TimeoutSecs:    defaultTimeoutSecsLimit,
	}
}

// Validate checks if the budget limits meet all requirements.
// Returns nil if valid, or a validation error describing the first constraint violation.
func (b *BudgetLimits) Validate() error {
	return validate.Struct(b)
}

// BudgetExceededError indicates that an operation would exceed budget limits.
// It provides detailed information about which limit was exceeded and by how much,
// enabling precise error reporting and recovery strategies.
type BudgetExceededError struct {
	// Type indicates which budget limit was exceeded.
	Type BudgetType

	// Limit is the budget limit that would be exceeded.
	Limit int64

	// Current is the current usage before the attempted operation.
	Current int64

	// Required is the amount required for the attempted operation.
	Required int64
}

// Error returns a formatted error message describing the budget violation.
// The message includes the budget type, limit, current usage, and required amount.
func (e BudgetExceededError) Error() string {
	return fmt.Sprintf("budget exceeded for %s: limit=%d, current=%d, required=%d",
		e.Type, e.Limit, e.Current, e.Required)
}

// OverBy returns how much the operation would exceed the budget limit.
// Useful for determining the magnitude of budget violations and planning recovery.
func (e BudgetExceededError) OverBy() int64 {
	return e.Current + e.Required - e.Limit
}

// NewBudgetExceededError creates a new budget exceeded error with detailed context.
// The budgetType should be one of the defined BudgetType constants.
func NewBudgetExceededError(budgetType BudgetType, limit, current, required int64) BudgetExceededError {
	return BudgetExceededError{
		Type:     budgetType,
		Limit:    limit,
		Current:  current,
		Required: required,
	}
}
