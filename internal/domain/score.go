// Package domain scoring provides evaluation score types and aggregation algorithms
// for LLM answer assessment. It defines score structures, operation contracts for
// evaluation processes, and statistical aggregation functions that enable reliable,
// auditable evaluation of generated content.
//
// Scoring Architecture:
//   - Judge-based evaluation with provider abstraction
//   - Artifact-based reasoning storage for audit trails
//   - Statistical aggregation with validity filtering
//   - Resource tracking for budget compliance
//   - Comprehensive error handling and recovery
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

	// ErrEmptyScores indicates that no scores were provided for aggregation.
	ErrEmptyScores = errors.New("no scores provided")
)

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

// NewScore creates a new Score with validation of critical fields using current time.
// It ensures the ReasonRef.Kind is set to "judge_rationale" and clamps
// Value and Confidence to the valid range [0,1].
// Uses time.Now() which makes it non-deterministic for Temporal workflows.
// For deterministic scoring in workflows, use MakeScore instead.
func NewScore(id, answerID, judgeID, provider, model string, reasonRef ArtifactRef) (*Score, error) {
	return MakeScore(id, answerID, time.Now(), ScoreProvenance{
		JudgeID:  judgeID,
		Provider: provider,
		Model:    model,
		ScoredAt: time.Now(),
	}, ScoreEvidence{
		ReasonRef: reasonRef,
	})
}

// MakeScore creates a new Score with deterministic inputs for Temporal workflows.
// This constructor accepts explicit timestamp and ID parameters to ensure
// reproducible behavior in workflow executions. The caller is responsible
// for providing valid timestamps and unique IDs.
func MakeScore(id, answerID string, scoredAt time.Time, provenance ScoreProvenance, evidence ScoreEvidence) (*Score, error) {
	// Validate that ReasonRef has the correct kind
	if evidence.ReasonRef.Kind != ArtifactJudgeRationale {
		return nil, ErrInvalidReasonKind
	}

	score := &Score{
		ID:            id,
		AnswerID:      answerID,
		Value:         0,
		Confidence:    0,
		ScoreEvidence: evidence,
		ScoreProvenance: ScoreProvenance{
			JudgeID:  provenance.JudgeID,
			Provider: provenance.Provider,
			Model:    provenance.Model,
			ScoredAt: scoredAt,
		},
		ScoreUsage: ScoreUsage{},
		ScoreValidity: ScoreValidity{
			Valid: true,
		},
	}

	return score, score.Validate()
}

// SetScoreValues sets Value and Confidence, clamping them to [0,1].
func (s *Score) SetScoreValues(value, confidence float64) {
	s.Value = clamp01(value)
	s.Confidence = clamp01(confidence)
}

// ScoreEvidence groups evidence and reasoning fields for better cohesion.
type ScoreEvidence struct {
	// ReasonRef references the judge's reasoning stored as an artifact.
	// The artifact Kind must be "judge_rationale" for proper categorization.
	ReasonRef ArtifactRef `json:"reason_ref" validate:"required"`

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

// Validate checks if the score meets all structural and business requirements.
// Returns nil if valid, or a validation error describing the first constraint violation.
func (s *Score) Validate() error {
	if err := validate.Struct(s); err != nil {
		return err
	}

	// Additional business logic validation
	if s.ReasonRef.Kind != ArtifactJudgeRationale {
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
func (s *Score) HasDimensions() bool {
	return len(s.Dimensions) > 0
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
}

// Validate checks if the score answers input meets all operation contract requirements.
// Returns nil if valid, or a validation error describing the constraint violation.
func (s *ScoreAnswersInput) Validate() error {
	return validate.Struct(s)
}

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

	// Error contains any critical error that prevented operation completion.
	// Individual score failures are captured in each Score's Error field.
	Error string `json:"error,omitempty"`
}

// Validate checks if the score answers output meets all operation contract requirements.
// Returns nil if valid, or a validation error describing the constraint violation.
func (s *ScoreAnswersOutput) Validate() error { return validate.Struct(s) }

// TotalTokens calculates the total token consumption by summing tokens
// from all individual scores. This is a pure function that returns the
// total without modifying any state. Use this for accurate resource usage
// reporting for budget tracking and billing purposes.
func TotalTokens(scores []Score) int64 {
	var total int64
	for _, score := range scores {
		total += score.TokensUsed
	}
	return total
}

// AggregateScoresInput represents the input for the AggregateScores evaluation operation.
// It contains the scores to be statistically aggregated and the original answers
// for reference and winner determination. This operation contract enables the
// final evaluation step that produces a single aggregate assessment.
type AggregateScoresInput struct {
	// Scores are the individual evaluation scores to be statistically aggregated.
	// Only valid scores (IsValid() returns true) are included in calculations.
	Scores []Score `json:"scores" validate:"required,min=1"`

	// Answers are the original generated answers for winner determination.
	// Used to identify which answer achieved the highest aggregate score.
	Answers []Answer `json:"answers" validate:"required,min=1"`
}

// Validate checks if the aggregate scores input meets all operation contract requirements.
// Returns nil if valid, or a validation error describing the constraint violation.
func (a *AggregateScoresInput) Validate() error { return validate.Struct(a) }

// AggregateScoresOutput represents the output from the AggregateScores evaluation operation.
// It contains the final aggregate score calculated from all valid individual scores
// and optionally identifies the highest-scoring answer as the winner. This represents
// the final evaluation result for decision-making and quality assessment.
type AggregateScoresOutput struct {
	// WinnerAnswer is a pointer to the answer with the highest individual score.
	// Nil if no valid scores exist or all scores are equal (tie condition).
	WinnerAnswer *Answer `json:"winner_answer,omitempty"`

	// AggregateScore is the arithmetic mean of all valid individual scores.
	// Represents the overall quality assessment for the evaluation session.
	AggregateScore float64 `json:"aggregate_score" validate:"min=0,max=1"`

	// AggregateDimensions contains per-dimension aggregate scores when available.
	// Populated only when input scores contain dimension breakdowns.
	AggregateDimensions map[Dimension]float64 `json:"aggregate_dimensions,omitempty"`
}

// Validate checks if the aggregate scores output meets all operation contract requirements.
// Returns nil if valid, or a validation error describing the constraint violation.
func (a *AggregateScoresOutput) Validate() error {
	return validate.Struct(a)
}

// AggregateScores calculates the arithmetic mean score from a slice of scores.
// Only valid scores (IsValid() returns true) are included in the calculation.
// This function implements the core statistical aggregation algorithm used
// throughout the evaluation system.
//
// Algorithm:
//   - Filters input scores to include only valid ones (Valid=true, Error="")
//   - Calculates arithmetic mean of valid score values
//   - Returns 0.0 if no valid scores are provided
//   - Handles edge cases: empty input, all invalid scores
//
// The mean aggregation provides a balanced assessment when multiple judges
// evaluate the same content, reducing bias from individual judge variations.
func AggregateScores(scores []Score) float64 {
	if len(scores) == 0 {
		return 0
	}

	var sum float64
	var count int
	for _, score := range scores {
		if score.IsValid() {
			sum += score.Value
			count++
		}
	}

	if count == 0 {
		return 0
	}

	return sum / float64(count)
}

// DetermineWinner finds the answer with the highest score among valid scores.
// Returns nil if no valid scores exist. Uses deterministic tie-breaking by
// selecting the answer with the lowest ID when multiple answers have the same
// highest score. This ensures consistent results across evaluations.
func DetermineWinner(scores []Score, answers []Answer) *Answer {
	if len(scores) == 0 || len(answers) == 0 {
		return nil
	}

	// Create a map for quick answer lookup by ID
	answerMap := make(map[string]*Answer)
	for i := range answers {
		answerMap[answers[i].ID] = &answers[i]
	}

	var highestScore float64
	var winnerAnswer *Answer
	hasValidScore := false

	for _, score := range scores {
		if !score.IsValid() {
			continue
		}

		answer, exists := answerMap[score.AnswerID]
		if !exists {
			continue
		}

		hasValidScore = true

		// New highest score
		if score.Value > highestScore {
			highestScore = score.Value
			winnerAnswer = answer
		} else if score.Value == highestScore && winnerAnswer != nil {
			// Tie-breaking: choose answer with lowest ID
			if answer.ID < winnerAnswer.ID {
				winnerAnswer = answer
			}
		}
	}

	if !hasValidScore {
		return nil
	}

	return winnerAnswer
}

// AggregateDimensionalScores calculates per-dimension aggregates from multiple scores.
// Returns a map of dimension names to their aggregate values (mean).
// Only includes dimensions that appear in at least one valid score.
func AggregateDimensionalScores(scores []Score) map[Dimension]float64 {
	if len(scores) == 0 {
		return nil
	}

	dimensionSums := make(map[Dimension]float64)
	dimensionCounts := make(map[Dimension]int)

	for _, score := range scores {
		if !score.IsValid() {
			continue
		}

		for _, dim := range score.Dimensions {
			dimensionSums[dim.Name] += dim.Value
			dimensionCounts[dim.Name]++
		}
	}

	if len(dimensionSums) == 0 {
		return nil
	}

	result := make(map[Dimension]float64)
	for dimension, sum := range dimensionSums {
		if count := dimensionCounts[dimension]; count > 0 {
			result[dimension] = sum / float64(count)
		}
	}

	return result
}
