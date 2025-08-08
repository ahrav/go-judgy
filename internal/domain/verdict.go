package domain

import (
	"errors"
	"time"
)

// Verdict-specific errors.
var (
	// ErrInvalidVerdict is returned when verdict validation fails due to missing
	// required fields, constraint violations, or business rule failures.
	ErrInvalidVerdict = errors.New("invalid verdict")
)

// VerdictStatus represents the final outcome of an evaluation process.
// Status transitions follow: pending -> (success|partial|failed|timeout).
type VerdictStatus string

// VerdictStatus enum values representing different evaluation outcomes.
const (
	// VerdictStatusSuccess indicates the evaluation completed with a winner answer
	// and all required scores generated successfully.
	VerdictStatusSuccess VerdictStatus = "success"

	// VerdictStatusPartial indicates scores were generated but no clear winner
	// was determined, requiring human review.
	VerdictStatusPartial VerdictStatus = "partial"

	// VerdictStatusFailed indicates the evaluation failed due to errors in
	// answer generation, scoring, or other critical failures.
	VerdictStatusFailed VerdictStatus = "failed"

	// VerdictStatusTimeout indicates the evaluation exceeded time limits
	// before completion.
	VerdictStatusTimeout VerdictStatus = "timeout"
)

// VerdictErrorCode represents machine-readable error codes for verdict failures.
// Using typed codes instead of magic strings provides better error handling.
type VerdictErrorCode string

// VerdictErrorCode values for different error scenarios.
const (
	// ErrCodeNone indicates no error occurred.
	ErrCodeNone VerdictErrorCode = ""

	// ErrCodeTimeout indicates the evaluation exceeded time limits.
	ErrCodeTimeout VerdictErrorCode = "timeout"

	// ErrCodeBudget indicates a budget limit was exceeded.
	ErrCodeBudget VerdictErrorCode = "budget_exceeded"

	// ErrCodeProvider indicates an LLM provider error occurred.
	ErrCodeProvider VerdictErrorCode = "provider_error"

	// ErrCodeValidation indicates validation failed.
	ErrCodeValidation VerdictErrorCode = "validation_error"
)

// ReviewReasonCode represents machine-readable codes for review requirements.
// Using typed codes enables consistent dashboard filtering and reporting.
type ReviewReasonCode string

// ReviewReasonCode values for different review triggers.
const (
	// ReviewLowAggregate indicates review needed due to low aggregate score.
	ReviewLowAggregate ReviewReasonCode = "low_aggregate_score"

	// ReviewUnsuccessful indicates review needed due to unsuccessful status.
	ReviewUnsuccessful ReviewReasonCode = "unsuccessful_status"
)

// IsValidVerdictStatus reports whether the status is a recognized verdict outcome.
// Returns true for success, partial, failed, or timeout statuses.
func IsValidVerdictStatus(status VerdictStatus) bool {
	switch status {
	case VerdictStatusSuccess, VerdictStatusPartial, VerdictStatusFailed, VerdictStatusTimeout:
		return true
	default:
		return false
	}
}

// VerdictOutcome groups outcome-related fields for better cohesion.
// Contains the winner, aggregate score, status, and error information.
type VerdictOutcome struct {
	// WinnerAnswerID references the ID of the highest-scoring answer.
	WinnerAnswerID string `json:"winner_answer_id,omitempty"`

	// AggregateScore represents the mean score across all valid evaluations (0-1).
	AggregateScore float64 `json:"aggregate_score" validate:"min=0,max=1"`

	// Status represents the final evaluation outcome (success/partial/failed/timeout).
	Status VerdictStatus `json:"status" validate:"required"`

	// ErrorCode contains the machine-readable error code if evaluation failed.
	ErrorCode VerdictErrorCode `json:"error_code,omitempty"`

	// Error contains the failure description if evaluation was unsuccessful.
	Error string `json:"error,omitempty"`
}

// VerdictUsageTotals groups resource usage and timing fields for better cohesion.
// Contains timing, token usage, API calls, and cost information.
type VerdictUsageTotals struct {
	// StartedAt marks the evaluation process initiation timestamp.
	StartedAt time.Time `json:"started_at" validate:"required"`

	// CompletedAt marks the evaluation process completion timestamp.
	CompletedAt time.Time `json:"completed_at" validate:"required"`

	// DurationMillis measures end-to-end evaluation time in milliseconds.
	DurationMillis int64 `json:"duration_ms" validate:"required,min=0"`

	// TotalTokens aggregates input and output tokens from all LLM operations.
	TotalTokens int64 `json:"total_tokens" validate:"required,min=0"`

	// TotalCalls counts LLM API requests for both answer generation and scoring.
	TotalCalls int64 `json:"total_calls" validate:"required,min=0"`

	// TotalCostCents represents the cumulative evaluation cost using the Cents type.
	TotalCostCents Cents `json:"total_cost_cents" validate:"required,min=0"`
}

// VerdictReview groups human review fields for better cohesion.
// Contains review requirements and reasoning.
type VerdictReview struct {
	// RequiresHumanReview flags verdicts needing manual review.
	RequiresHumanReview bool `json:"requires_human_review"`

	// ReviewReasonCode provides the machine-readable reason for review.
	ReviewReasonCode ReviewReasonCode `json:"review_reason_code,omitempty"`

	// ReviewReason provides the human-readable justification for review.
	ReviewReason string `json:"review_reason,omitempty"`
}

// Verdict represents the comprehensive final result of an LLM evaluation process.
// It aggregates scores, identifies the winning answer, tracks resource usage,
// and determines if human review is required.
// All monetary values are in cents to avoid floating-point precision issues.
type Verdict struct {
	// ID uniquely identifies this verdict across all evaluations.
	ID string `json:"id" validate:"required,uuid"`

	// EvaluationID links this verdict to its originating evaluation process.
	EvaluationID string `json:"evaluation_id" validate:"required,uuid"`

	// EvaluationInstanceID identifies the specific evaluation process execution instance.
	EvaluationInstanceID string `json:"evaluation_instance_id" validate:"required,uuid"`

	// Embed outcome fields for cohesion while maintaining flat JSON
	VerdictOutcome

	// Embed usage totals for cohesion while maintaining flat JSON
	VerdictUsageTotals

	// Embed review fields for cohesion while maintaining flat JSON
	VerdictReview

	// WinnerAnswer references the highest-scoring answer, nil if no clear winner.
	// Keep for backward compatibility, will be deprecated in favor of WinnerAnswerID.
	WinnerAnswer *Answer `json:"winner_answer,omitempty"`

	// AllScores contains the complete set of scores generated during evaluation.
	// Consider migrating to score references in the future to reduce payload size.
	AllScores []Score `json:"all_scores"`
}

// UsageTotals represents measured resource usage from evaluation operations.
// Used to pass actual measured values instead of estimating from data.
type UsageTotals struct {
	// Tokens is the total number of tokens consumed.
	Tokens int64

	// Calls is the total number of API calls made.
	Calls int64

	// Cost is the total cost in cents.
	Cost Cents
}

// Validate checks structural integrity and business rule compliance.
// Returns nil if all required fields are present, constraints satisfied,
// and at least one score is available.
// Returns validation error for missing fields or empty scores collection.
func (v *Verdict) Validate() error {
	if err := validate.Struct(v); err != nil {
		return err
	}

	// Business rule: verdicts must have at least one score for validity.
	if len(v.AllScores) == 0 {
		return ErrEmptyScores
	}

	// Business rule: CompletedAt must be after or equal to StartedAt.
	if v.CompletedAt.Before(v.StartedAt) {
		return ErrInvalidVerdict
	}

	return nil
}

// CalculateDuration updates DurationMillis based on StartedAt and CompletedAt timestamps.
// This method modifies the verdict in-place and should be called after setting
// both time fields but before persisting the verdict.
func (v *Verdict) CalculateDuration() {
	v.DurationMillis = v.CompletedAt.Sub(v.StartedAt).Milliseconds()
}

// ApplyTotals applies measured resource usage to the verdict.
// This is the preferred method for setting resource totals as it uses
// actual measured values rather than estimates.
func (v *Verdict) ApplyTotals(totals UsageTotals) {
	v.TotalTokens = totals.Tokens
	v.TotalCalls = totals.Calls
	v.TotalCostCents = totals.Cost
}

// DetermineStatus sets the verdict status based on evaluation outcomes.
// Status determination follows priority order:
//  1. Timeout if ErrorCode is ErrCodeTimeout
//  2. Failed if any error code is set or error message exists
//  3. Failed if no scores were generated
//  4. Partial if no winner was determined
//  5. Success if winner exists with scores
//
// This method modifies the verdict in-place.
func (v *Verdict) DetermineStatus() {
	switch {
	case v.ErrorCode == ErrCodeTimeout:
		v.Status = VerdictStatusTimeout
	case v.ErrorCode != ErrCodeNone || v.Error != "":
		v.Status = VerdictStatusFailed
	case len(v.AllScores) == 0:
		v.Status = VerdictStatusFailed
	case v.WinnerAnswer == nil && v.WinnerAnswerID == "":
		v.Status = VerdictStatusPartial
	default:
		v.Status = VerdictStatusSuccess
	}
}

// NeedsReview determines if the verdict requires human review based on quality thresholds.
// Returns true and sets RequiresHumanReview, ReviewReasonCode, and ReviewReason fields if either:
//   - AggregateScore falls below the threshold
//   - Status is not VerdictStatusSuccess
//
// This method has side effects: it modifies RequiresHumanReview, ReviewReasonCode, and ReviewReason fields.
func (v *Verdict) NeedsReview(threshold float64) bool {
	if v.AggregateScore < threshold {
		v.RequiresHumanReview = true
		v.ReviewReasonCode = ReviewLowAggregate
		v.ReviewReason = "Score below threshold"
		return true
	}
	if v.Status != VerdictStatusSuccess {
		v.RequiresHumanReview = true
		v.ReviewReasonCode = ReviewUnsuccessful
		v.ReviewReason = "Evaluation did not complete successfully"
		return true
	}
	v.RequiresHumanReview = false
	v.ReviewReasonCode = ""
	v.ReviewReason = ""
	return false
}
