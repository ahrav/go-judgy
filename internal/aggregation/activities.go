// Package aggregation implements Temporal activities for statistical score aggregation.
// It provides domain-specific activities that combine individual judge evaluation scores
// into final verdicts using configurable statistical methods (mean, median, trimmed mean).
//
// The aggregation system serves as the final stage in multi-judge evaluation workflows,
// taking disparate individual assessments and producing statistically sound consensus results.
// This enables consistent evaluation outcomes despite variations in individual judge scoring.
//
// Core Operations:
//   - AggregateScores: Statistical combination of judge scores with winner determination
//   - Winner selection: Policy-aware aggregation with deterministic tie-breaking
//   - Metrics collection: Observability into aggregation performance and patterns
//   - Event emission: VerdictReached events for downstream processing
//
// Statistical Methods:
//   - Mean: Arithmetic average providing balanced consensus
//   - Median: Robust central tendency resistant to outliers
//   - Trimmed Mean: Outlier-resistant average with configurable trimming
//
// Quality Guarantees:
//   - Deterministic results for identical inputs
//   - Robust validation of all inputs and outputs
//   - Comprehensive error handling with non-retryable failures
//   - Thread-safe operations suitable for concurrent Temporal execution
package aggregation

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/pkg/activity"
)

// epsilon defines the tolerance for floating-point equality comparisons.
// Set to 1e-9 to handle IEEE 754 double precision arithmetic limitations
// when comparing aggregate scores for tie detection and winner determination.
// This prevents false negatives in tie detection due to accumulated rounding errors
// during statistical calculations.
const epsilon = 1e-9

// MetricsRecorder provides an interface for recording aggregation metrics.
// This allows for observability into the aggregation process without
// tight coupling to a specific metrics implementation.
type MetricsRecorder interface {
	// IncrementCounter increments a counter metric.
	IncrementCounter(name string, tags map[string]string, value float64)
	// RecordHistogram records a value in a histogram metric.
	RecordHistogram(name string, tags map[string]string, value float64)
	// SetGauge sets a gauge metric to a specific value.
	SetGauge(name string, tags map[string]string, value float64)
}

// ErrNotImplemented indicates that a requested activity has not been implemented.
// This error is returned for activities that are planned but not yet available,
// allowing graceful degradation in development environments.
var ErrNotImplemented = errors.New("activity not implemented")

// Activities implements Temporal activities for score aggregation operations.
// All operations are thread-safe and suitable for concurrent execution within
// Temporal workflows. The Activities struct encapsulates statistical aggregation
// logic, event emission, and metrics collection for observability.
//
// Concurrency: All methods are safe for concurrent use.
// Dependencies: Requires BaseActivities for infrastructure and optional MetricsRecorder.
type Activities struct {
	activity.BaseActivities
	events  *EventEmitter
	metrics MetricsRecorder
}

// NewActivities creates aggregation activities with the provided base infrastructure.
// The base activities provide common Temporal workflow infrastructure including
// logging, context management, and event emission capabilities. Metrics recording
// defaults to NoOpMetrics and can be configured via WithMetrics.
//
// The returned Activities instance is safe for concurrent use across multiple
// Temporal activity executions.
func NewActivities(base activity.BaseActivities) *Activities {
	return &Activities{
		BaseActivities: base,
		events:         NewEventEmitter(base),
		metrics:        &NoOpMetrics{}, // Default to no-op metrics
	}
}

// WithMetrics configures observability by setting the metrics recorder.
// Returns the same Activities instance for method chaining. The metrics
// recorder will be used to emit aggregation performance and quality metrics
// for monitoring and alerting purposes.
func (a *Activities) WithMetrics(metrics MetricsRecorder) *Activities {
	a.metrics = metrics
	return a
}

// AggregateScores combines individual judge scores according to a configured policy.
// This is the primary Temporal activity that processes multiple evaluation scores
// to produce a final statistical consensus representing the overall assessment.
// The aggregation method (mean, median, trimmed_mean) determines how individual
// scores are mathematically combined.
//
// Operation Contract:
//   - Input validation: Ensures all required fields are present and valid
//   - Score filtering: Processes only valid scores (Valid=true, Error="")
//   - Minimum threshold: Fails if valid score count below MinValidScores
//   - Policy application: Applies statistical method per AggregationPolicy
//   - Winner determination: Identifies highest-scoring answer with tie-breaking
//   - Event emission: Emits VerdictReached event for downstream processing
//   - Metrics recording: Collects observability data for monitoring
//
// Error Handling:
//   - Returns non-retryable errors for validation failures
//   - Logs warnings for edge cases without failing
//   - Gracefully handles missing or malformed data
func (a *Activities) AggregateScores(
	ctx context.Context,
	input domain.AggregateScoresInput,
) (*domain.AggregateScoresOutput, error) {
	startTime := time.Now()

	if err := input.Validate(); err != nil {
		return nil, nonRetryable("AggregateScores", err, "invalid input")
	}

	wfCtx := a.GetWorkflowContext(ctx)
	activity.SafeLog(ctx, "Starting AggregateScores activity",
		"workflow_id", wfCtx.WorkflowID,
		"activity_id", wfCtx.ActivityID,
		"total_scores", len(input.Scores),
		"aggregation_method", input.Policy.Method)

	// Validate reasoning fields before filtering for aggregation.
	if err := validateScoreReasoning(input.Scores); err != nil {
		return nil, nonRetryable("AggregateScores", err, "reasoning validation failed")
	}

	validScores := filterValidScores(input.Scores)
	activity.SafeLog(ctx, "Filtered valid scores",
		"valid_count", len(validScores),
		"invalid_count", len(input.Scores)-len(validScores))

	// Record invalid score rate for quality monitoring.
	if a.metrics != nil {
		invalidRate := float64(len(input.Scores)-len(validScores)) / float64(len(input.Scores))
		a.metrics.SetGauge("invalid_score_rate", map[string]string{
			"tenant": wfCtx.TenantID,
		}, invalidRate)
	}

	if len(validScores) < input.MinValidScores {
		return nil, nonRetryable("AggregateScores",
			fmt.Errorf("insufficient valid scores: got %d, need %d", len(validScores), input.MinValidScores),
			"minimum valid scores not met")
	}

	aggregateScore, err := applyAggregationPolicy(validScores, input.Policy)
	if err != nil {
		return nil, nonRetryable("AggregateScores", err, "aggregation failed")
	}

	// Record aggregation method for observability.
	if a.metrics != nil {
		a.metrics.IncrementCounter("aggregation_method_used", map[string]string{
			"tenant": wfCtx.TenantID,
			"method": input.Policy.Method.String(),
		}, 1)
	}

	winnerID, tiedIDs := determineWinnerWithPolicyAggregation(validScores, input.Answers, input.Policy)

	// Record tie frequency for aggregation quality analysis.
	if a.metrics != nil && len(tiedIDs) > 0 {
		a.metrics.IncrementCounter("tie_frequency", map[string]string{
			"tenant": wfCtx.TenantID,
			"method": input.Policy.Method.String(),
		}, 1)
	}

	// Record score distribution for statistical analysis.
	if a.metrics != nil {
		a.metrics.RecordHistogram("score_distribution", map[string]string{
			"tenant": wfCtx.TenantID,
			"method": input.Policy.Method.String(),
		}, aggregateScore)
	}

	// Aggregate costs from ALL scores including invalid ones for complete accounting.
	totalCost := aggregateCosts(input.Scores)

	// Calculate per-dimension aggregates when dimensional scores are present.
	dimensionAggregates := domain.AggregateDimensionalScores(validScores)
	output := &domain.AggregateScoresOutput{
		WinnerAnswerID:      winnerID,
		AggregateScore:      aggregateScore,
		Method:              input.Policy.Method,
		ValidScoreCount:     len(validScores),
		TotalScoreCount:     len(input.Scores),
		TiedWithIDs:         tiedIDs,
		CostCents:           totalCost,
		AggregateDimensions: dimensionAggregates,
	}

	// Maintain backward compatibility by populating deprecated WinnerAnswer field.
	if winnerID != "" {
		for i := range input.Answers {
			if input.Answers[i].ID == winnerID {
				output.WinnerAnswer = &input.Answers[i]
				break
			}
		}
	}

	if err := output.Validate(); err != nil {
		return nil, nonRetryable("AggregateScores", err, "invalid output")
	}

	// Emit VerdictReached event for downstream processing (best-effort).
	processingTimeMs := time.Since(startTime).Milliseconds()
	a.emitVerdictReachedEvent(ctx, output, input, wfCtx)

	activity.SafeLog(ctx, "AggregateScores completed",
		"aggregate_score", aggregateScore,
		"winner_id", winnerID,
		"method", input.Policy.Method,
		"valid_scores", len(validScores),
		"processing_time_ms", processingTimeMs)

	return output, nil
}

// validateScoreReasoning ensures valid scores have exactly one reasoning source.
// For scores marked as valid, either InlineReasoning must contain text OR
// ReasonRef must reference an artifact, but not both. Invalid scores may
// have neither reasoning field set.
func validateScoreReasoning(scores []domain.Score) error {
	for i, score := range scores {
		if !score.Valid {
			continue
		}

		hasReasonRef := !score.ReasonRef.IsZero()
		hasInlineReasoning := score.InlineReasoning != ""

		// Exactly one reasoning source must be set for valid scores.
		if hasReasonRef == hasInlineReasoning {
			return fmt.Errorf("score %d: exactly one of ReasonRef or InlineReasoning must be set for valid scores", i)
		}
	}
	return nil
}

// filterValidScores extracts scores suitable for statistical aggregation.
// Returns only scores that pass validation (Valid=true and Error="").
// Invalid scores are excluded from calculations but included in cost totals.
func filterValidScores(scores []domain.Score) []domain.Score {
	var valid []domain.Score
	for _, score := range scores {
		if score.IsValid() {
			valid = append(valid, score)
		}
	}
	return valid
}

// applyAggregationPolicy calculates the aggregate score using the specified method.
// Supports mean (arithmetic average), median (robust central tendency), and
// trimmed_mean (outlier-resistant average). Returns 0.0 for empty input.
func applyAggregationPolicy(validScores []domain.Score, policy domain.AggregationPolicy) (float64, error) {
	if len(validScores) == 0 {
		return 0, nil
	}

	switch policy.Method {
	case domain.AggregationMethodMean:
		return calculateMean(validScores), nil
	case domain.AggregationMethodMedian:
		return calculateMedian(validScores), nil
	case domain.AggregationMethodTrimmedMean:
		return calculateTrimmedMean(validScores, policy.TrimFraction), nil
	default:
		return 0, fmt.Errorf("unsupported aggregation method: %s", policy.Method)
	}
}

// calculateMean computes the arithmetic average of score values.
// Delegates to domain.AggregateScores for consistent calculation logic.
// Returns 0.0 for empty input.
func calculateMean(scores []domain.Score) float64 {
	if len(scores) == 0 {
		return 0
	}
	return domain.AggregateScores(scores)
}

// calculateMedian computes the middle value of sorted scores.
// For odd-length inputs, returns the middle value. For even-length inputs,
// returns the arithmetic mean of the two middle values. This provides
// robust central tendency resistant to outlier scores.
func calculateMedian(scores []domain.Score) float64 {
	if len(scores) == 0 {
		return 0
	}

	values := make([]float64, len(scores))
	for i, score := range scores {
		values[i] = score.Value
	}
	sort.Float64s(values)

	n := len(values)
	if n%2 == 1 {
		return values[n/2]
	}
	// Average of two middle values for even-length arrays.
	mid1 := values[n/2-1]
	mid2 := values[n/2]
	return (mid1 + mid2) / 2
}

// calculateTrimmedMean computes the mean after removing outliers from both ends.
// Trims the specified fraction from each end before calculating the mean,
// providing outlier resistance while maintaining more data than median.
// Clamps trimFraction to [0, 0.5] and falls back to regular mean if trimming
// would remove too many values.
func calculateTrimmedMean(scores []domain.Score, trimFraction float64) float64 {
	if len(scores) == 0 {
		return 0
	}

	// Clamp trim fraction to valid range [0, 0.5].
	if trimFraction < 0 {
		trimFraction = 0
	}
	if trimFraction > 0.5 {
		trimFraction = 0.5
	}

	values := make([]float64, len(scores))
	for i, score := range scores {
		values[i] = score.Value
	}
	sort.Float64s(values)

	n := len(values)
	trimCount := int(math.Floor(float64(n) * trimFraction))

	// Fallback to regular mean if trimming would remove too many values.
	if trimCount*2 >= n {
		return calculateMean(scores)
	}

	var sum float64
	for i := trimCount; i < n-trimCount; i++ {
		sum += values[i]
	}
	return sum / float64(n-trimCount*2)
}

// determineWinnerWithPolicyAggregation identifies the highest-scoring answer using policy-aware aggregation.
// Applies the aggregation policy to each answer's scores independently, then compares
// the resulting aggregate scores. Uses lexicographic sorting on AnswerID for deterministic
// tie-breaking. Returns the winner ID and any tied answer IDs.
//
// Algorithm:
//  1. Groups scores by AnswerID
//  2. Applies aggregation policy to each answer's score set
//  3. Identifies highest aggregate score(s) within epsilon tolerance
//  4. Breaks ties lexicographically by AnswerID
func determineWinnerWithPolicyAggregation(
	validScores []domain.Score,
	answers []domain.Answer,
	policy domain.AggregationPolicy,
) (winnerID string, tiedIDs []string) {
	if len(validScores) == 0 || len(answers) == 0 {
		return "", nil
	}

	answerMap := make(map[string]*domain.Answer)
	for i := range answers {
		answerMap[answers[i].ID] = &answers[i]
	}

	scoresPerAnswer := make(map[string][]float64)
	for _, score := range validScores {
		if _, exists := answerMap[score.AnswerID]; !exists {
			continue
		}
		scoresPerAnswer[score.AnswerID] = append(scoresPerAnswer[score.AnswerID], score.Value)
	}

	aggregatedScores := make(map[string]float64)
	for answerID, scores := range scoresPerAnswer {
		var aggregated float64
		switch policy.Method {
		case domain.AggregationMethodMean:
			aggregated = calculateMeanValues(scores)
		case domain.AggregationMethodMedian:
			aggregated = calculateMedianValues(scores)
		case domain.AggregationMethodTrimmedMean:
			aggregated = calculateTrimmedMeanValues(scores, policy.TrimFraction)
		default:
			// Fallback to mean for unknown aggregation methods.
			aggregated = calculateMeanValues(scores)
		}
		aggregatedScores[answerID] = aggregated
	}

	if len(aggregatedScores) == 0 {
		return "", nil
	}

	var highestScore float64
	first := true
	for _, score := range aggregatedScores {
		if first || score > highestScore {
			highestScore = score
			first = false
		}
	}

	// Collect all answers within epsilon tolerance for tie detection.
	var topAnswers []string
	for answerID, score := range aggregatedScores {
		if math.Abs(score-highestScore) <= epsilon {
			topAnswers = append(topAnswers, answerID)
		}
	}

	if len(topAnswers) == 0 {
		return "", nil
	}

	// Sort lexicographically for deterministic tie-breaking
	sort.Strings(topAnswers)

	// Lexicographic winner selection with remaining answers as tied.
	winnerID = topAnswers[0]
	if len(topAnswers) > 1 {
		tiedIDs = topAnswers[1:]
	}

	return winnerID, tiedIDs
}

// Helper functions for aggregating raw float64 values

// calculateMeanValues computes the arithmetic average of raw values.
// Used internally by winner determination logic for per-answer aggregation.
func calculateMeanValues(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

// calculateMedianValues computes the median of raw values.
// Used internally by winner determination for robust per-answer aggregation.
func calculateMedianValues(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)

	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// calculateTrimmedMeanValues computes the trimmed mean of raw values.
// Used internally by winner determination for outlier-resistant per-answer aggregation.
// Clamps trimFraction and falls back to mean when trimming would remove too many values.
func calculateTrimmedMeanValues(values []float64, trimFraction float64) float64 {
	if len(values) == 0 {
		return 0
	}

	// Clamp trim fraction to valid range.
	if trimFraction < 0 {
		trimFraction = 0
	}
	if trimFraction > 0.5 {
		trimFraction = 0.5
	}

	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)

	n := len(sorted)
	trimCount := int(math.Floor(float64(n) * trimFraction))

	// Fallback to mean if trimming would remove too many values.
	if trimCount*2 >= n {
		return calculateMeanValues(values)
	}

	var sum float64
	for i := trimCount; i < n-trimCount; i++ {
		sum += sorted[i]
	}
	return sum / float64(n-trimCount*2)
}

// aggregateCosts computes the total cost from all scores including invalid ones.
// Provides complete cost accounting for budgeting and billing purposes regardless
// of score validity.
func aggregateCosts(scores []domain.Score) domain.Cents {
	var total domain.Cents
	for _, score := range scores {
		total += score.CostCents
	}
	return total
}

// emitVerdictReachedEvent publishes a VerdictReached domain event.
// Collects all relevant metadata including score IDs and artifact references
// for downstream event processing. Event emission is best-effort and will not
// fail the aggregation operation.
func (a *Activities) emitVerdictReachedEvent(
	ctx context.Context,
	output *domain.AggregateScoresOutput,
	input domain.AggregateScoresInput,
	wfCtx activity.WorkflowContext,
) {
	scoreIDs := make([]string, len(input.Scores))
	for i, score := range input.Scores {
		scoreIDs[i] = score.ID
	}

	// Collect ReasonRef keys for event metadata.
	var artifactRefs []string
	for _, score := range input.Scores {
		if !score.ReasonRef.IsZero() {
			artifactRefs = append(artifactRefs, score.ReasonRef.Key)
		}
	}

	verdictID := uuid.New().String()

	a.events.EmitVerdictReached(ctx,
		verdictID,
		output,
		scoreIDs,
		artifactRefs,
		wfCtx,
		input.ClientIdempotencyKey)
}

// Error helpers - wrap errors as Temporal application errors

// nonRetryable creates a Temporal non-retryable application error.
// Used for validation failures and business logic errors that should not
// be retried automatically by the Temporal workflow engine.
func nonRetryable(tag string, cause error, msg string) error {
	return temporal.NewNonRetryableApplicationError(msg, tag, cause)
}

// NoOpMetrics provides a no-op implementation of MetricsRecorder.
// Used as the default metrics recorder when no observability backend is configured,
// allowing the system to operate without metrics collection. All method calls
// are safe and have no side effects.
type NoOpMetrics struct{}

// IncrementCounter discards counter increments.
func (n *NoOpMetrics) IncrementCounter(name string, tags map[string]string, value float64) {}

// RecordHistogram discards histogram recordings.
func (n *NoOpMetrics) RecordHistogram(name string, tags map[string]string, value float64) {}

// SetGauge discards gauge settings.
func (n *NoOpMetrics) SetGauge(name string, tags map[string]string, value float64) {}
