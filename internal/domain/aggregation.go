// Package domain aggregation provides statistical aggregation algorithms
// for combining individual evaluation scores into final assessment results.
// It defines aggregation operation contracts, winner determination logic,
// and dimensional score processing for comprehensive evaluation workflows.
//
// Aggregation Architecture:
//   - Statistical combination of multiple judge scores
//   - Winner determination with deterministic tie-breaking
//   - Dimensional score aggregation for rubric-based evaluation
//   - Pure functions for deterministic workflow execution
//   - Comprehensive input/output validation
//
// Integration with Evaluation Processes:
//   - AggregateScores operation: Statistical combination of individual scores
//   - Winner determination: Identifies highest-scoring answer with tie-breaking
//   - Dimensional aggregation: Per-dimension score combination for rubrics
//   - Resource tracking: Token consumption and cost calculation
//   - Validation: Comprehensive input/output contract enforcement
package domain

// AggregationMethod represents the statistical method used to aggregate scores.
type AggregationMethod string

const (
	// AggregationMethodMean calculates the arithmetic average of all valid scores.
	AggregationMethodMean AggregationMethod = "mean"

	// AggregationMethodMedian finds the middle value when scores are sorted.
	AggregationMethodMedian AggregationMethod = "median"

	// AggregationMethodTrimmedMean calculates the mean after removing outliers from both ends.
	AggregationMethodTrimmedMean AggregationMethod = "trimmed_mean"
)

// String returns the string representation of the aggregation method.
func (m AggregationMethod) String() string { return string(m) }

// AggregationPolicy defines how scores are combined during aggregation.
// It supports multiple statistical methods including mean, median, and trimmed mean,
// allowing flexibility in how judge scores are consolidated into a final verdict.
type AggregationPolicy struct {
	// Method specifies the aggregation algorithm to use.
	// Supported values: AggregationMethodMean, AggregationMethodMedian, AggregationMethodTrimmedMean
	Method AggregationMethod `json:"method" validate:"required,oneof=mean median trimmed_mean"`

	// TrimFraction specifies the fraction of scores to trim from each end
	// when using trimmed_mean method (e.g., 0.1 for 10% trim from each end).
	// Ignored for other aggregation methods.
	TrimFraction float64 `json:"trim_fraction" validate:"min=0,max=0.5"`
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

	// Policy defines the aggregation method and parameters to use.
	// Determines how individual scores are combined into a final verdict.
	Policy AggregationPolicy `json:"policy" validate:"required"`

	// MinValidScores specifies the minimum number of valid scores required.
	// Aggregation fails if fewer than this many scores are valid.
	MinValidScores int `json:"min_valid_scores" validate:"min=1"`

	// ClientIdempotencyKey enables deterministic event generation.
	// Used to create consistent idempotency keys for emitted events.
	ClientIdempotencyKey string `json:"client_idempotency_key" validate:"required"`
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
	// Deprecated: Use WinnerAnswerID instead for cleaner data contracts.
	WinnerAnswer *Answer `json:"winner_answer,omitempty"`

	// WinnerAnswerID identifies the answer with the highest score.
	// Empty if no valid scores exist or winner cannot be determined.
	WinnerAnswerID string `json:"winner_answer_id,omitempty"`

	// AggregateScore is the final aggregated score using the specified method.
	// Represents the overall quality assessment for the evaluation session.
	AggregateScore float64 `json:"aggregate_score" validate:"min=0,max=1"`

	// Method indicates which aggregation method was actually used.
	// Confirms the policy that was applied (mean, median, or trimmed_mean).
	Method AggregationMethod `json:"method" validate:"required"`

	// ValidScoreCount is the number of valid scores used in aggregation.
	// Excludes scores where Valid=false or Error is non-empty.
	ValidScoreCount int `json:"valid_score_count" validate:"min=0"`

	// TotalScoreCount is the total number of scores provided.
	// Includes both valid and invalid scores for completeness.
	TotalScoreCount int `json:"total_score_count" validate:"min=0"`

	// TiedWithIDs lists answer IDs that tied with the winner.
	// Empty if there was a clear winner or no valid scores.
	TiedWithIDs []string `json:"tied_with_ids,omitempty"`

	// CostCents is the total cost aggregated from all scores.
	// Includes costs from both valid and invalid scores.
	CostCents Cents `json:"cost_cents" validate:"min=0"`

	// AggregateDimensions contains per-dimension aggregate scores when available.
	// Populated only when input scores contain dimension breakdowns.
	AggregateDimensions map[Dimension]float64 `json:"aggregate_dimensions,omitempty"`
}

// Validate checks if the aggregate scores output meets all operation contract requirements.
// Returns nil if valid, or a validation error describing the constraint violation.
func (a *AggregateScoresOutput) Validate() error {
	return validate.Struct(a)
}

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

	// Create lookup map for answer retrieval by ID.
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

		if score.Value > highestScore {
			highestScore = score.Value
			winnerAnswer = answer
		} else if score.Value == highestScore && winnerAnswer != nil {
			// Deterministic tie-breaking using lexicographic ordering.
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
