// Package domain scoring provides evaluation score types and aggregation algorithms
// for LLM answer assessment. It defines score structures, operation contracts for
// evaluation processes, and statistical aggregation functions that enable reliable,
// auditable evaluation of generated content.
//
// Scoring Architecture:
//   - Judge-based evaluation with provider abstraction.
//   - Artifact-based reasoning storage for audit trails.
//   - Statistical aggregation with validity filtering.
//   - Resource tracking for budget compliance.
//   - Comprehensive error handling and recovery.
//
// Integration with Evaluation Processes:
//   - ScoreAnswers operation: Evaluates multiple answers using configured judges
//   - AggregateScores operation: Combines scores using statistical methods
//   - Resource tracking: Monitors token usage and API call costs
//   - Error resilience: Handles judge failures gracefully
package domain

import (
	"errors"
	"time"
)

// Score-specific errors returned by scoring operations.
var (
	// ErrInvalidScore indicates that a score contains invalid data.
	ErrInvalidScore = errors.New("invalid score")

	// ErrInvalidReasonKind indicates that the reason artifact kind is not judge_rationale.
	ErrInvalidReasonKind = errors.New("reason_ref.kind must be 'judge_rationale'")

	// ErrInvalidReasonData indicates that both ReasonRef and InlineReasoning are populated or both are empty.
	ErrInvalidReasonData = errors.New("exactly one of reason_ref or inline_reasoning must be populated")

	// ErrEmptyScores indicates that no scores were provided for aggregation.
	ErrEmptyScores = errors.New("no scores provided")
)

// ScoreValidator performs business-level validation and one-shot repair on scoring responses.
// This interface enables clean separation between transport-level concerns (handled by LLM client)
// and domain-level validation logic (owned by scoring activities).
//
// Implementations must be pure and deterministic - no network calls or side effects.
// The validator is injected by scoring activities into ScoreAnswersInput for execution by the LLM client.
type ScoreValidator interface {
	// Validate performs validation and optional repair on raw JSON from LLM responses.
	// Returns normalized JSON if valid (possibly after repair), whether repair was applied, and any error.
	//
	// The validation process should:
	// 1. Attempt strict JSON parsing and business rule validation
	// 2. If invalid, apply one-shot repair attempt
	// 3. Re-validate repaired content
	// 4. Return error if still invalid after repair
	//
	// Parameters:
	//   raw: Raw JSON bytes from LLM response
	//
	// Returns:
	//   normalized: Valid JSON bytes (possibly repaired)
	//   repaired: true if repair was applied, false if original was valid
	//   err: non-nil if validation failed after repair attempt
	Validate(raw []byte) (normalized []byte, repaired bool, err error)
}

// RepairPolicy controls validation and repair behavior for scoring operations.
// This allows fine-grained control over how aggressive repair attempts should be
// and whether transport-level fixes are permitted.
type RepairPolicy struct {
	// MaxAttempts specifies the maximum number of repair attempts.
	// Set to 1 for Story 1.5's one-shot repair policy.
	MaxAttempts int `json:"max_attempts"`

	// AllowTransportRepairs enables the LLM client to apply minimal transport-level fixes
	// such as removing markdown code fences and fixing trailing commas before
	// passing content to the domain validator.
	AllowTransportRepairs bool `json:"allow_transport_repairs"`
}

// Dimension represents a rubric aspect for per-dimension scoring.
// Kept as string type for flexibility in custom rubrics.
type Dimension string

// Standard dimension constants for common evaluation aspects.
const (
	DimCorrectness  Dimension = "correctness"  // Factual accuracy and logical correctness
	DimCompleteness Dimension = "completeness" // Coverage of required information
	DimSafety       Dimension = "safety"       // Absence of harmful content
	DimClarity      Dimension = "clarity"      // Clarity and coherence of response
	DimRelevance    Dimension = "relevance"    // Alignment with the question
)

// DimensionScore holds a single rubric aspect score.
type DimensionScore struct {
	Name  Dimension `json:"name"  validate:"required,min=1"`
	Value float64   `json:"value" validate:"required,min=0,max=1"`
}

const (
	// CorrectnessWeight is the default weight for correctness scoring dimension.
	CorrectnessWeight = 0.40
	// CompletenessWeight is the default weight for completeness scoring dimension.
	CompletenessWeight = 0.25
	// SafetyWeight is the default weight for safety scoring dimension.
	SafetyWeight = 0.15
	// ClarityWeight is the default weight for clarity scoring dimension.
	ClarityWeight = 0.10
	// RelevanceWeight is the default weight for relevance scoring dimension.
	RelevanceWeight = 0.10
)

const (
	// DefaultBlobThresholdBytes is the default size threshold for blob storage.
	// Rationales larger than this threshold are stored in the artifact store.
	// Rationales at or below this threshold are stored inline in the workflow state.
	DefaultBlobThresholdBytes = 10 << 10 // 10 KiB

	// MinBlobThresholdBytes is the minimum allowed blob threshold.
	MinBlobThresholdBytes = 4 << 10 // 4 KiB

	// MaxBlobThresholdBytes is the maximum allowed blob threshold.
	MaxBlobThresholdBytes = 1 << 20 // 1 MiB
)

// DefaultDimensionWeights returns standard weights for computing overall scores.
// Returns a fresh copy to prevent mutation. These can be overridden per evaluation configuration.
func DefaultDimensionWeights() map[Dimension]float64 {
	return map[Dimension]float64{
		DimCorrectness:  CorrectnessWeight,
		DimCompleteness: CompletenessWeight,
		DimSafety:       SafetyWeight,
		DimClarity:      ClarityWeight,
		DimRelevance:    RelevanceWeight,
	}
}

// clamp01 ensures a value is within the range [0, 1].
func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

// ValidateBlobThreshold checks if a blob threshold value is within acceptable bounds.
// Returns the clamped threshold value and whether it was modified.
func ValidateBlobThreshold(threshold int) (int, bool) {
	if threshold < MinBlobThresholdBytes {
		return MinBlobThresholdBytes, true
	}
	if threshold > MaxBlobThresholdBytes {
		return MaxBlobThresholdBytes, true
	}
	return threshold, false
}

// ShouldBlobRationale determines whether a rationale should be stored in blob storage
// based on its size and the configured threshold.
func ShouldBlobRationale(rationaleLength, threshold int) bool {
	return rationaleLength > threshold
}

// Score represents an evaluation score for an answer from a judge or scoring model.
// It includes the numerical score, confidence assessment, reasoning artifact reference,
// and comprehensive metadata about the scoring process for audit and aggregation.
// Fields are organized into embedded sub-structs for better cohesion while maintaining
// flat JSON serialization for backward compatibility.
//
// Scoring Model:
//   - Value: Normalized score between 0.0 (worst) and 1.0 (best)
//   - Confidence: Judge's confidence in the score accuracy (0.0-1.0)
//   - Reasoning: Stored as artifact to minimize process audit trail
//   - Attribution: Full provenance including judge, provider, and model
//   - Validity: Indicates whether score should be included in aggregation
type Score struct {
	// ID uniquely identifies this score within the evaluation context.
	// Generated by the scoring operation for tracking and audit purposes.
	ID string `json:"id" validate:"required,uuid"`

	// AnswerID references the answer being scored using UUID format.
	// Links this score to a specific generated answer for correlation.
	AnswerID string `json:"answer_id" validate:"required,uuid"`

	// Value is the normalized score between 0.0 (worst) and 1.0 (best).
	// Enables consistent comparison across different judges and models.
	Value float64 `json:"value" validate:"min=0,max=1"`

	// Confidence indicates the judge's confidence in this score (0.0-1.0).
	// Higher confidence scores may be weighted more heavily in aggregation.
	Confidence float64 `json:"confidence" validate:"min=0,max=1"`

	// Embedded sub-structs for better organization while maintaining flat JSON
	ScoreEvidence
	ScoreProvenance
	ScoreUsage
	ScoreValidity
}

// NewScore creates a new Score with validation of critical fields using current time.
// It ensures the ReasonRef.Kind is set to "judge_rationale" and clamps
// Value and Confidence to the valid range [0,1].
// Uses time.Now() which makes it non-deterministic for Temporal workflows.
// For deterministic scoring in workflows, use MakeScore instead.
func NewScore(id, answerID, judgeID, provider, model string, reasonRef ArtifactRef) (*Score, error) {
	return MakeScore(id, answerID, time.Now(), ScoreProvenance{
		JudgeID:            judgeID,
		Provider:           provider,
		Model:              model,
		ProviderRequestIDs: nil, // No provider request IDs available in legacy constructor
		ScoredAt:           time.Now(),
	}, ScoreEvidence{
		ReasonRef: reasonRef,
	})
}

// MakeScore creates a new Score with deterministic inputs for Temporal workflows.
// This constructor accepts explicit timestamp and ID parameters to ensure
// reproducible behavior in workflow executions. The caller is responsible
// for providing valid timestamps and unique IDs.
func MakeScore(
	id string,
	answerID string,
	scoredAt time.Time,
	provenance ScoreProvenance,
	evidence ScoreEvidence,
) (*Score, error) {
	// Create score first to determine if it will be valid.
	score := &Score{
		ID:            id,
		AnswerID:      answerID,
		Value:         0,
		Confidence:    0,
		ScoreEvidence: evidence,
		ScoreProvenance: ScoreProvenance{
			JudgeID:            provenance.JudgeID,
			Provider:           provenance.Provider,
			Model:              provenance.Model,
			ProviderRequestIDs: provenance.ProviderRequestIDs,
			ScoredAt:           scoredAt,
		},
		ScoreUsage: ScoreUsage{},
		ScoreValidity: ScoreValidity{
			Valid: true, // Assume valid initially
		},
	}

	// For valid scores, validate reasoning data constraint: exactly one of ReasonRef or InlineReasoning must be populated.
	hasReasonRef := !evidence.ReasonRef.IsZero()
	hasInlineReasoning := evidence.InlineReasoning != ""

	if hasReasonRef == hasInlineReasoning {
		return nil, ErrInvalidReasonData
	}

	// Validate artifact kind constraint for ReasonRef.
	if hasReasonRef && evidence.ReasonRef.Kind != ArtifactJudgeRationale {
		return nil, ErrInvalidReasonKind
	}

	return score, score.Validate()
}

// SetScoreValues sets Value and Confidence, clamping them to [0,1].
func (s *Score) SetScoreValues(value, confidence float64) {
	s.Value = clamp01(value)
	s.Confidence = clamp01(confidence)
}

// Validate checks if the score meets all structural and business requirements.
// Returns nil if valid, or a validation error describing the first constraint violation.
func (s *Score) Validate() error {
	if err := validate.Struct(s); err != nil {
		return err
	}

	// Invalid scores (Valid=false) are allowed to have no reasoning.
	// This accommodates scores that failed during the scoring process.
	if !s.Valid {
		// For invalid scores, allow empty reasoning but validate artifact kind if ReasonRef is present.
		if !s.ReasonRef.IsZero() && s.ReasonRef.Kind != ArtifactJudgeRationale {
			return ErrInvalidReasonKind
		}
		return nil
	}

	// For valid scores, enforce reasoning data constraint: exactly one of ReasonRef or InlineReasoning must be populated.
	hasReasonRef := !s.ReasonRef.IsZero()
	hasInlineReasoning := s.InlineReasoning != ""

	if hasReasonRef == hasInlineReasoning {
		return ErrInvalidReasonData
	}

	// Validate artifact kind requirement.
	if hasReasonRef && s.ReasonRef.Kind != ArtifactJudgeRationale {
		return ErrInvalidReasonKind
	}

	return nil
}

// IsValid determines if the score should be included in statistical aggregation.
// A score is considered valid only if both the Valid flag is true and no error occurred
// during the scoring process. Invalid scores are excluded from aggregate calculations.
func (s *Score) IsValid() bool { return s.Valid && s.Error == "" }

// ComputeOverall calculates the overall Value from Dimensions using the provided weights.
// If no dimensions exist, leaves Value unchanged. If weights is nil, uses DefaultDimensionWeights().
// Automatically clamps result to [0,1].
func (s *Score) ComputeOverall(weights map[Dimension]float64) {
	if len(s.Dimensions) == 0 {
		return
	}

	if weights == nil {
		weights = DefaultDimensionWeights()
	}

	var weightedSum, totalWeight float64
	for _, dim := range s.Dimensions {
		if weight, ok := weights[dim.Name]; ok {
			weightedSum += dim.Value * weight
			totalWeight += weight
		}
	}

	if totalWeight > 0 {
		s.Value = clamp01(weightedSum / totalWeight)
	}
}

// GetDimensionScore returns the score for a specific dimension and whether it was found.
// Returns (value, true) if the dimension exists, (0, false) otherwise.
func (s *Score) GetDimensionScore(dimension Dimension) (float64, bool) {
	for _, dim := range s.Dimensions {
		if dim.Name == dimension {
			return dim.Value, true
		}
	}
	return 0, false
}

// HasDimensions returns true if this score includes per-dimension breakdown.
func (s *Score) HasDimensions() bool { return len(s.Dimensions) > 0 }

// ScoreEvidence groups evidence and reasoning fields for better cohesion.
type ScoreEvidence struct {
	// ReasonRef references the judge's reasoning stored as an artifact.
	// The artifact Kind must be "judge_rationale" for proper categorization.
	// Optional - only populated when rationale size exceeds blob threshold.
	ReasonRef ArtifactRef `json:"reason_ref,omitempty"`

	// InlineReasoning contains the judge's reasoning text stored directly in workflow state.
	// Only populated when rationale size is at or below the blob threshold.
	// Exactly one of ReasonRef or InlineReasoning must be populated.
	InlineReasoning string `json:"inline_reasoning,omitempty"`

	// Dimensions contains per-rubric aspect scores when using dimensional evaluation.
	// Optional field - when empty, only the overall Value is used (backward compat).
	Dimensions []DimensionScore `json:"dimensions,omitempty" validate:"omitempty,dive"`

	// RubricVersion tracks the scoring rubric/prompt version for comparability.
	// Helps identify when scores use different evaluation criteria.
	RubricVersion string `json:"rubric_version,omitempty"`
}

// ScoreProvenance groups provenance and attribution fields for better cohesion.
type ScoreProvenance struct {
	// JudgeID identifies which judge or scoring model provided this evaluation.
	// Enables judge-specific analysis and bias detection across evaluations.
	JudgeID string `json:"judge_id" validate:"required"`

	// Provider identifies the LLM provider used for scoring.
	// Common values: "openai", "anthropic", "google", "aws".
	Provider string `json:"provider" validate:"required"`

	// Model specifies the exact model used for scoring.
	// Examples: "gpt-4", "claude-3-sonnet", "gemini-pro".
	Model string `json:"model" validate:"required"`

	// ProviderRequestIDs contains all provider request IDs for this score.
	// Enables cross-system correlation and debugging of scoring operations.
	ProviderRequestIDs []string `json:"provider_request_ids,omitempty"`

	// ScoredAt records when this score was generated.
	// Used for evaluation analysis and audit trail requirements.
	ScoredAt time.Time `json:"scored_at" validate:"required"`
}

// ScoreUsage groups resource consumption fields for better cohesion.
type ScoreUsage struct {
	// LatencyMs measures the scoring time in milliseconds.
	// Used for performance monitoring and SLA compliance tracking.
	LatencyMs int64 `json:"latency_ms" validate:"min=0"`

	// TokensUsed counts the tokens consumed for scoring.
	// Includes both input (question + answer) and output (reasoning) tokens.
	TokensUsed int64 `json:"tokens_used" validate:"min=0"`

	// CallsUsed tracks the number of API calls made for this score.
	// Includes retries and any intermediate calls.
	CallsUsed int64 `json:"calls_used" validate:"min=0"`

	// CostCents represents the cost of generating this score in cents.
	// Used for accurate cost tracking across evaluation operations.
	CostCents Cents `json:"cost_cents" validate:"min=0"`
}

// ScoreValidity groups validity and error state fields for better cohesion.
type ScoreValidity struct {
	// Valid indicates whether this score should be included in aggregation.
	// Set to false for scores that failed validation or quality checks.
	Valid bool `json:"valid"`

	// Error contains any error message if scoring failed.
	// Non-empty Error automatically makes the score invalid for aggregation.
	Error string `json:"error,omitempty"`
}

// ScoreAnswersInput represents the input for the ScoreAnswers evaluation operation.
// It provides the original question for context, the answers to be evaluated,
// and the configuration specifying which judge/model to use for scoring.
// This operation contract enables batch scoring of multiple answers efficiently.
type ScoreAnswersInput struct {
	// Question is the original question providing context for scoring.
	// Length constraints (10-1000 chars) ensure reasonable processing overhead.
	Question string `json:"question" validate:"required,min=10,max=1000"`

	// Answers are the generated answers to be evaluated by the judge.
	// Must contain at least one answer for the scoring operation to proceed.
	Answers []Answer `json:"answers" validate:"required,min=1"`

	// Config contains the evaluation configuration specifying judge and model.
	// The Provider and Model fields determine which scoring system to use.
	Config EvalConfig `json:"config" validate:"required"`

	// SchemaVersion identifies the scoring schema version for validation.
	// Used for metrics, debugging, and ensuring validator compatibility.
	// Optional field - if empty, validator should use its default version.
	SchemaVersion string `json:"schema_version,omitempty"`

	// Validator provides business-level validation and repair logic.
	// Injected by scoring activities for execution by the LLM client.
	// If nil, client will perform basic validation only.
	// This field is not serialized to avoid interface serialization issues.
	Validator ScoreValidator `json:"-"`

	// RepairPolicy controls how aggressive validation and repair should be.
	// If nil, validator should use conservative defaults.
	// Allows fine-grained control over repair behavior.
	RepairPolicy *RepairPolicy `json:"repair_policy,omitempty"`
}

// Validate checks if the score answers input meets all operation contract requirements.
// Returns nil if valid, or a validation error describing the constraint violation.
func (s *ScoreAnswersInput) Validate() error { return validate.Struct(s) }

// ScoreAnswersOutput represents the output from the ScoreAnswers evaluation operation.
// It contains the evaluation scores for each input answer, along with resource
// usage tracking for budget compliance and error information for failed operations.
// The output enables downstream aggregation and resource management decisions.
type ScoreAnswersOutput struct {
	// Scores contains the evaluation scores for each input answer.
	// Array length should match input answers, with invalid scores for failures.
	Scores []Score `json:"scores" validate:"required,min=1"`

	// TokensUsed tracks the total tokens consumed across all scoring operations.
	// Calculated by summing TokensUsed from all individual scores.
	TokensUsed int64 `json:"tokens_used" validate:"min=0"`

	// CallsMade tracks the number of API calls made to the scoring provider.
	// Used for rate limiting and cost calculation purposes.
	CallsMade int64 `json:"calls_made" validate:"min=0"`

	// CostCents represents the total cost of scoring operation in cents.
	// Required for accurate budget reconciliation in workflow operations.
	CostCents Cents `json:"cost_cents" validate:"min=0"`

	// Error contains any critical error that prevented operation completion.
	// Individual score failures are captured in each Score's Error field.
	Error string `json:"error,omitempty"`
}

// Validate checks if the score answers output meets all operation contract requirements.
// Returns nil if valid, or a validation error describing the constraint violation.
func (s *ScoreAnswersOutput) Validate() error { return validate.Struct(s) }
