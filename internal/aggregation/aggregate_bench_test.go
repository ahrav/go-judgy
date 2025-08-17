package aggregation

import (
	"context"
	"fmt"
	"testing"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/pkg/activity"
)

// BenchmarkAggregateScores benchmarks the main AggregateScores activity with various input sizes.
func BenchmarkAggregateScores(b *testing.B) {
	testCases := []struct {
		name            string
		numAnswers      int
		scoresPerAnswer int
		method          domain.AggregationMethod
	}{
		{"Small_Mean", 2, 5, domain.AggregationMethodMean},
		{"Medium_Mean", 5, 10, domain.AggregationMethodMean},
		{"Large_Mean", 10, 20, domain.AggregationMethodMean},
		{"Small_Median", 2, 5, domain.AggregationMethodMedian},
		{"Medium_Median", 5, 10, domain.AggregationMethodMedian},
		{"Large_Median", 10, 20, domain.AggregationMethodMedian},
		{"Small_TrimmedMean", 2, 5, domain.AggregationMethodTrimmedMean},
		{"Medium_TrimmedMean", 5, 10, domain.AggregationMethodTrimmedMean},
		{"Large_TrimmedMean", 10, 20, domain.AggregationMethodTrimmedMean},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			ctx := context.Background()
			activities := CreateTestActivities(nil, nil)
			input := CreateTestAggregateInput(tc.numAnswers, tc.scoresPerAnswer, tc.method)
			if tc.method == domain.AggregationMethodTrimmedMean {
				input.Policy.TrimFraction = 0.2
			}

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				// Use unique idempotency key for each iteration
				input.ClientIdempotencyKey = fmt.Sprintf("bench-key-%d", i)

				output, err := activities.AggregateScores(ctx, input)
				if err != nil {
					b.Fatalf("unexpected error: %v", err)
				}
				if output == nil {
					b.Fatal("unexpected nil output")
				}
			}
		})
	}
}

// BenchmarkCalculateMean benchmarks mean calculation with different score counts.
func BenchmarkCalculateMean(b *testing.B) {
	scoreCounts := []int{1, 5, 10, 50, 100, 500}

	for _, count := range scoreCounts {
		b.Run(fmt.Sprintf("Scores_%d", count), func(b *testing.B) {
			scores := CreateTestScoresForAnswers(CreateTestAnswers(1), count)

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				result := calculateMean(scores)
				// Prevent compiler optimization
				_ = result
			}
		})
	}
}

// BenchmarkCalculateMedian benchmarks median calculation with different score counts.
func BenchmarkCalculateMedian(b *testing.B) {
	scoreCounts := []int{1, 5, 10, 50, 100, 500}

	for _, count := range scoreCounts {
		b.Run(fmt.Sprintf("Scores_%d", count), func(b *testing.B) {
			scores := CreateTestScoresForAnswers(CreateTestAnswers(1), count)

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				result := calculateMedian(scores)
				// Prevent compiler optimization
				_ = result
			}
		})
	}
}

// BenchmarkCalculateTrimmedMean benchmarks trimmed mean calculation with different parameters.
func BenchmarkCalculateTrimmedMean(b *testing.B) {
	testCases := []struct {
		name         string
		scoreCount   int
		trimFraction float64
	}{
		{"Scores_10_Trim_0.1", 10, 0.1},
		{"Scores_10_Trim_0.2", 10, 0.2},
		{"Scores_10_Trim_0.3", 10, 0.3},
		{"Scores_50_Trim_0.1", 50, 0.1},
		{"Scores_50_Trim_0.2", 50, 0.2},
		{"Scores_100_Trim_0.1", 100, 0.1},
		{"Scores_100_Trim_0.2", 100, 0.2},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			scores := CreateTestScoresForAnswers(CreateTestAnswers(1), tc.scoreCount)

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				result := calculateTrimmedMean(scores, tc.trimFraction)
				// Prevent compiler optimization
				_ = result
			}
		})
	}
}

// BenchmarkDetermineWinnerWithPolicyAggregation benchmarks winner determination with different scenarios.
func BenchmarkDetermineWinnerWithPolicyAggregation(b *testing.B) {
	testCases := []struct {
		name            string
		numAnswers      int
		scoresPerAnswer int
		method          domain.AggregationMethod
	}{
		{"2_Answers_5_Scores_Mean", 2, 5, domain.AggregationMethodMean},
		{"5_Answers_10_Scores_Mean", 5, 10, domain.AggregationMethodMean},
		{"10_Answers_20_Scores_Mean", 10, 20, domain.AggregationMethodMean},
		{"2_Answers_5_Scores_Median", 2, 5, domain.AggregationMethodMedian},
		{"5_Answers_10_Scores_Median", 5, 10, domain.AggregationMethodMedian},
		{"2_Answers_5_Scores_TrimmedMean", 2, 5, domain.AggregationMethodTrimmedMean},
		{"5_Answers_10_Scores_TrimmedMean", 5, 10, domain.AggregationMethodTrimmedMean},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			answers := CreateTestAnswers(tc.numAnswers)
			scores := CreateTestScoresForAnswers(answers, tc.scoresPerAnswer)
			policy := domain.AggregationPolicy{Method: tc.method}
			if tc.method == domain.AggregationMethodTrimmedMean {
				policy.TrimFraction = 0.2
			}

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				winnerID, tiedIDs := determineWinnerWithPolicyAggregation(scores, answers, policy)
				// Prevent compiler optimization
				_ = winnerID
				_ = tiedIDs
			}
		})
	}
}

// BenchmarkFilterValidScores benchmarks score filtering with different validity ratios.
func BenchmarkFilterValidScores(b *testing.B) {
	testCases := []struct {
		name        string
		totalScores int
		validRatio  float64
	}{
		{"100_Scores_90pct_Valid", 100, 0.9},
		{"100_Scores_75pct_Valid", 100, 0.75},
		{"100_Scores_50pct_Valid", 100, 0.5},
		{"500_Scores_90pct_Valid", 500, 0.9},
		{"500_Scores_75pct_Valid", 500, 0.75},
		{"1000_Scores_90pct_Valid", 1000, 0.9},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			validCount := int(float64(tc.totalScores) * tc.validRatio)
			invalidCount := tc.totalScores - validCount
			scores := GenerateInvalidScores(TestAnswerUUID1, validCount, invalidCount)

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				filtered := filterValidScores(scores)
				// Prevent compiler optimization
				_ = filtered
			}
		})
	}
}

// BenchmarkAggregateCosts benchmarks cost aggregation with different score counts.
func BenchmarkAggregateCosts(b *testing.B) {
	scoreCounts := []int{10, 50, 100, 500, 1000}

	for _, count := range scoreCounts {
		b.Run(fmt.Sprintf("Scores_%d", count), func(b *testing.B) {
			scores := CreateTestScoresForAnswers(CreateTestAnswers(1), count)

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				total := aggregateCosts(scores)
				// Prevent compiler optimization
				_ = total
			}
		})
	}
}

// BenchmarkScoreReasoning benchmarks score reasoning validation.
func BenchmarkScoreReasoning(b *testing.B) {
	testCases := []struct {
		name       string
		scoreCount int
	}{
		{"10_Scores", 10},
		{"50_Scores", 50},
		{"100_Scores", 100},
		{"500_Scores", 500},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			scores := CreateTestScoresForAnswers(CreateTestAnswers(1), tc.scoreCount)

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				err := validateScoreReasoning(scores)
				// Prevent compiler optimization
				_ = err
			}
		})
	}
}

// BenchmarkEventEmission benchmarks event emission performance.
func BenchmarkEventEmission(b *testing.B) {
	testCases := []struct {
		name       string
		numAnswers int
		numScores  int
	}{
		{"Small_2_Answers_10_Scores", 2, 10},
		{"Medium_5_Answers_50_Scores", 5, 50},
		{"Large_10_Answers_100_Scores", 10, 100},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			ctx := context.Background()
			sink := NewCapturingEventSink()
			activities := CreateTestActivities(sink, nil)

			input := CreateTestAggregateInput(tc.numAnswers, tc.numScores/tc.numAnswers, "mean")
			output := &domain.AggregateScoresOutput{
				WinnerAnswerID:  TestAnswerUUID1,
				AggregateScore:  0.75,
				Method:          "mean",
				ValidScoreCount: tc.numScores,
				TotalScoreCount: tc.numScores,
			}
			wfCtx := activity.WorkflowContext{
				WorkflowID: testWorkflowID,
				RunID:      "test-run",
				TenantID:   testTenantID,
				ActivityID: "test-activity",
			}

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				input.ClientIdempotencyKey = fmt.Sprintf("bench-event-%d", i)
				activities.emitVerdictReachedEvent(ctx, output, input, wfCtx)
			}
		})
	}
}

// BenchmarkMetricsRecording benchmarks metrics recording performance.
func BenchmarkMetricsRecording(b *testing.B) {
	testCases := []struct {
		name       string
		metricType string
		concurrent bool
	}{
		{"Counter_Sequential", "counter", false},
		{"Histogram_Sequential", "histogram", false},
		{"Gauge_Sequential", "gauge", false},
		{"Counter_Concurrent", "counter", true},
		{"Histogram_Concurrent", "histogram", true},
		{"Gauge_Concurrent", "gauge", true},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			metrics := NewEnhancedMockMetricsRecorder()
			tags := map[string]string{"tenant": testTenantID}

			b.ResetTimer()
			b.ReportAllocs()

			if tc.concurrent {
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						switch tc.metricType {
						case "counter":
							metrics.IncrementCounter("test_counter", tags, 1.0)
						case "histogram":
							metrics.RecordHistogram("test_histogram", tags, 0.75)
						case "gauge":
							metrics.SetGauge("test_gauge", tags, 0.5)
						}
					}
				})
			} else {
				for i := 0; i < b.N; i++ {
					switch tc.metricType {
					case "counter":
						metrics.IncrementCounter("test_counter", tags, 1.0)
					case "histogram":
						metrics.RecordHistogram("test_histogram", tags, 0.75)
					case "gauge":
						metrics.SetGauge("test_gauge", tags, 0.5)
					}
				}
			}
		})
	}
}

// BenchmarkApplyAggregationPolicy benchmarks policy application with different methods.
func BenchmarkApplyAggregationPolicy(b *testing.B) {
	testCases := []struct {
		name       string
		method     domain.AggregationMethod
		scoreCount int
	}{
		{"Mean_10_Scores", domain.AggregationMethodMean, 10},
		{"Mean_100_Scores", domain.AggregationMethodMean, 100},
		{"Median_10_Scores", domain.AggregationMethodMedian, 10},
		{"Median_100_Scores", domain.AggregationMethodMedian, 100},
		{"TrimmedMean_10_Scores", domain.AggregationMethodTrimmedMean, 10},
		{"TrimmedMean_100_Scores", domain.AggregationMethodTrimmedMean, 100},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			scores := CreateTestScoresForAnswers(CreateTestAnswers(1), tc.scoreCount)
			policy := domain.AggregationPolicy{Method: tc.method}
			if tc.method == domain.AggregationMethodTrimmedMean {
				policy.TrimFraction = 0.2
			}

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				result, err := applyAggregationPolicy(scores, policy)
				if err != nil {
					b.Fatalf("unexpected error: %v", err)
				}
				// Prevent compiler optimization
				_ = result
			}
		})
	}
}

// BenchmarkStatisticalCalculationsRaw benchmarks raw value statistical calculations.
func BenchmarkStatisticalCalculationsRaw(b *testing.B) {
	testCases := []struct {
		name       string
		method     string
		valueCount int
	}{
		{"MeanValues_10", "mean", 10},
		{"MeanValues_100", "mean", 100},
		{"MedianValues_10", "median", 10},
		{"MedianValues_100", "median", 100},
		{"TrimmedMeanValues_10", "trimmed_mean", 10},
		{"TrimmedMeanValues_100", "trimmed_mean", 100},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			values := make([]float64, tc.valueCount)
			for i := range values {
				values[i] = 0.5 + float64(i%5)*0.1
			}

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				var result float64
				switch tc.method {
				case "mean":
					result = calculateMeanValues(values)
				case "median":
					result = calculateMedianValues(values)
				case "trimmed_mean":
					result = calculateTrimmedMeanValues(values, 0.2)
				}
				// Prevent compiler optimization
				_ = result
			}
		})
	}
}

// BenchmarkMemoryAllocation benchmarks memory allocation patterns.
func BenchmarkMemoryAllocation(b *testing.B) {
	testCases := []struct {
		name            string
		numAnswers      int
		scoresPerAnswer int
	}{
		{"Small_2x5", 2, 5},
		{"Medium_5x10", 5, 10},
		{"Large_10x20", 10, 20},
		{"XLarge_20x50", 20, 50},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				// Test full input creation allocation pattern
				input := CreateTestAggregateInput(tc.numAnswers, tc.scoresPerAnswer, "mean")
				// Prevent compiler optimization
				_ = input
			}
		})
	}
}

// BenchmarkCompleteWorkflow benchmarks the complete aggregation workflow end-to-end.
func BenchmarkCompleteWorkflow(b *testing.B) {
	testCases := []struct {
		name            string
		numAnswers      int
		scoresPerAnswer int
		withEvents      bool
		withMetrics     bool
	}{
		{"Complete_NoObservability", 3, 10, false, false},
		{"Complete_WithEvents", 3, 10, true, false},
		{"Complete_WithMetrics", 3, 10, false, true},
		{"Complete_FullObservability", 3, 10, true, true},
		{"Large_Complete_FullObservability", 10, 20, true, true},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			ctx := context.Background()

			var sink *CapturingEventSink
			var metrics *EnhancedMockMetricsRecorder

			if tc.withEvents {
				sink = NewCapturingEventSink()
			}
			if tc.withMetrics {
				metrics = NewEnhancedMockMetricsRecorder()
			}

			activities := CreateTestActivities(sink, metrics)

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				input := CreateTestAggregateInput(tc.numAnswers, tc.scoresPerAnswer, "mean")
				input.ClientIdempotencyKey = fmt.Sprintf("bench-complete-%d", i)

				output, err := activities.AggregateScores(ctx, input)
				if err != nil {
					b.Fatalf("unexpected error: %v", err)
				}
				if output == nil {
					b.Fatal("unexpected nil output")
				}
			}
		})
	}
}
