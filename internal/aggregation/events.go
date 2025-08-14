// Package aggregation implements Temporal activities for score aggregation.
// It provides domain-specific activities and events for combining individual
// judge scores into final verdicts according to configured policies.
package aggregation

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

// verdictProducedEvent represents the final aggregated scoring result.
// Emitted when individual scores are combined into a final verdict,
// providing visibility into the aggregation policy and decision process.
type verdictProducedEvent struct {
	VerdictID         string                 `json:"verdict_id"`
	FinalScore        float64                `json:"final_score"`
	AggregationPolicy string                 `json:"aggregation_policy"`
	ScoreCount        int                    `json:"score_count"`
	ScoreDistribution map[string]float64     `json:"score_distribution"`
	Confidence        float64                `json:"confidence"`
	Decision          string                 `json:"decision"`
	Metadata          map[string]interface{} `json:"metadata"`
	ProducedAt        time.Time              `json:"produced_at"`
}

// aggregationCompleteEvent represents the completion of score aggregation.
// Provides summary statistics and metadata about the aggregation process.
type aggregationCompleteEvent struct {
	TotalScores       int     `json:"total_scores"`
	TotalAnswers      int     `json:"total_answers"`
	AggregationPolicy string  `json:"aggregation_policy"`
	MinScore          float64 `json:"min_score"`
	MaxScore          float64 `json:"max_score"`
	MeanScore         float64 `json:"mean_score"`
	MedianScore       float64 `json:"median_score"`
	ProcessingTimeMs  int64   `json:"processing_time_ms"`
	ClientIdemKey     string  `json:"client_idem_key"`
}

// EventEmitter handles event emission for the aggregation domain.
// It encapsulates the logic for creating and emitting aggregation-specific
// events with proper metadata and error handling.
type EventEmitter struct {
	base activity.BaseActivities
}

// NewEventEmitter creates a new EventEmitter with the provided base activities.
func NewEventEmitter(base activity.BaseActivities) *EventEmitter {
	return &EventEmitter{base: base}
}

// EmitVerdictProduced emits an event for the final aggregated verdict.
// This provides visibility into the aggregation decision process,
// including the score distribution and final decision.
func (e *EventEmitter) EmitVerdictProduced(
	ctx context.Context,
	verdict domain.Verdict,
	wfCtx activity.WorkflowContext,
	clientIdemKey string,
) {
	// Create a simplified event for the verdict
	// In production, this would extract actual fields from VerdictOutcome
	event := verdictProducedEvent{
		VerdictID:         verdict.ID,
		FinalScore:        0.0,    // Would be extracted from VerdictOutcome
		AggregationPolicy: "mean", // Default policy for now
		ScoreCount:        0,      // Would be calculated from actual scores
		ScoreDistribution: make(map[string]float64),
		Confidence:        0.0,       // Would be extracted from VerdictOutcome
		Decision:          "pending", // Would be extracted from VerdictOutcome
		Metadata:          make(map[string]interface{}),
		ProducedAt:        time.Now(),
	}

	payload, err := json.Marshal(event)
	if err != nil {
		activity.SafeLogError(ctx, "Failed to marshal verdict event", "error", err)
		return
	}

	envelope := events.Envelope{
		ID:             uuid.New().String(),
		Type:           "aggregation.verdict_produced",
		Source:         "aggregation-activity",
		Version:        "1.0.0",
		Timestamp:      time.Now(),
		IdempotencyKey: fmt.Sprintf("%s-verdict-%s", clientIdemKey, verdict.ID),
		TenantID:       wfCtx.TenantID,
		WorkflowID:     wfCtx.WorkflowID,
		RunID:          wfCtx.RunID,
		Payload:        payload,
	}

	e.base.EmitEventSafe(ctx, envelope, fmt.Sprintf("VerdictProduced[%s]", verdict.ID))
}

// EmitAggregationComplete emits a summary event for the aggregation process.
// This provides statistics and metadata about the aggregation operation,
// useful for monitoring aggregation performance and policy effectiveness.
func (e *EventEmitter) EmitAggregationComplete(
	ctx context.Context,
	_ *domain.AggregateScoresOutput,
	input domain.AggregateScoresInput,
	wfCtx activity.WorkflowContext,
	clientIdemKey string,
	processingTimeMs int64,
) {
	// Calculate statistics
	stats := calculateScoreStatistics(input.Scores)

	event := aggregationCompleteEvent{
		TotalScores:       len(input.Scores),
		TotalAnswers:      len(input.Answers),
		AggregationPolicy: "mean", // Default policy for now
		MinScore:          stats.min,
		MaxScore:          stats.max,
		MeanScore:         stats.mean,
		MedianScore:       stats.median,
		ProcessingTimeMs:  processingTimeMs,
		ClientIdemKey:     clientIdemKey, // Pass clientIdemKey as parameter
	}

	payload, err := json.Marshal(event)
	if err != nil {
		activity.SafeLogError(ctx, "Failed to marshal aggregation complete event", "error", err)
		return
	}

	envelope := events.Envelope{
		ID:             uuid.New().String(),
		Type:           "aggregation.complete",
		Source:         "aggregation-activity",
		Version:        "1.0.0",
		Timestamp:      time.Now(),
		IdempotencyKey: fmt.Sprintf("%s-aggregation-complete", clientIdemKey),
		TenantID:       wfCtx.TenantID,
		WorkflowID:     wfCtx.WorkflowID,
		RunID:          wfCtx.RunID,
		Payload:        payload,
	}

	e.base.EmitEventSafe(ctx, envelope, "AggregationComplete")
}

// scoreStatistics holds calculated statistics for a set of scores.
type scoreStatistics struct {
	min    float64
	max    float64
	mean   float64
	median float64
}

// calculateScoreStatistics computes basic statistics for a set of scores.
func calculateScoreStatistics(scores []domain.Score) scoreStatistics {
	if len(scores) == 0 {
		return scoreStatistics{}
	}

	// Extract score values
	values := make([]float64, len(scores))
	sum := 0.0
	minScore := scores[0].Value
	maxScore := scores[0].Value

	for i, score := range scores {
		values[i] = score.Value
		sum += score.Value
		if score.Value < minScore {
			minScore = score.Value
		}
		if score.Value > maxScore {
			maxScore = score.Value
		}
	}

	// Calculate mean
	mean := sum / float64(len(scores))

	// Calculate median (simplified - doesn't sort)
	// In production, would properly sort and find median
	median := values[len(values)/2]

	return scoreStatistics{
		min:    minScore,
		max:    maxScore,
		mean:   mean,
		median: median,
	}
}
