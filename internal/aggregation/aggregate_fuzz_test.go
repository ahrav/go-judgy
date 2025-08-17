package aggregation

import (
	"context"
	"math"
	"testing"

	"github.com/google/uuid"

	"github.com/ahrav/go-judgy/internal/domain"
)

// FuzzAggregateScores tests the AggregateScores activity with fuzzed inputs
// to discover edge cases and ensure robustness against malformed data.
func FuzzAggregateScores(f *testing.F) {
	// Seed corpus with known edge cases
	f.Add(0.0, 0.0, 0.0, 1)          // All zeros with 1 score
	f.Add(1.0, 1.0, 1.0, 1)          // All ones with 1 score
	f.Add(0.5, 0.6, 0.4, 3)          // Mixed values with 3 scores
	f.Add(0.1, 0.9, 0.5, 2)          // High variance with 2 scores
	f.Add(0.3333, 0.6666, 0.9999, 3) // Repeating decimals

	f.Fuzz(func(t *testing.T, val1, val2, val3 float64, numScores int) {
		// Clamp inputs to valid ranges
		if numScores < 1 || numScores > 100 {
			t.Skip("numScores out of reasonable range")
		}

		values := []float64{val1, val2, val3}

		// Create test input with fuzzed values
		ctx := context.Background()
		activities := CreateTestActivities(nil, nil)

		answers := CreateTestAnswers(1)
		var scores []domain.Score

		for i := 0; i < numScores; i++ {
			value := values[i%len(values)]
			// Clamp to valid score range [0, 1]
			if value < 0 {
				value = 0
			}
			if value > 1 {
				value = 1
			}
			if math.IsNaN(value) || math.IsInf(value, 0) {
				value = 0.5 // Safe default
			}

			scores = append(scores, CreateTestScore(answers[0].ID, WithScoreValue(value)))
		}

		input := domain.AggregateScoresInput{
			Scores:               scores,
			Answers:              answers,
			Policy:               domain.AggregationPolicy{Method: domain.AggregationMethodMean},
			MinValidScores:       1,
			ClientIdempotencyKey: "fuzz-test-key",
		}

		output, err := activities.AggregateScores(ctx, input)

		// Basic invariants that should always hold
		if err != nil {
			// Error is acceptable for some fuzzed inputs
			return
		}

		if output == nil {
			t.Error("output should not be nil when no error")
			return
		}

		// Verify basic output invariants
		if output.AggregateScore < 0 || output.AggregateScore > 1 {
			t.Errorf("aggregate score %f outside valid range [0,1]", output.AggregateScore)
		}

		if output.ValidScoreCount < 0 {
			t.Errorf("valid score count %d cannot be negative", output.ValidScoreCount)
		}

		if output.TotalScoreCount != len(scores) {
			t.Errorf("total score count %d != input count %d", output.TotalScoreCount, len(scores))
		}

		if !math.IsInf(output.AggregateScore, 0) && !math.IsNaN(output.AggregateScore) {
			// Score should be finite and real
		} else {
			t.Errorf("aggregate score should be finite, got %f", output.AggregateScore)
		}
	})
}

// FuzzCalculateMean tests mean calculation with fuzzed score values.
func FuzzCalculateMean(f *testing.F) {
	// Seed corpus with edge cases
	f.Add(0.0, 0.0, 0.0, 1) // 1 value
	f.Add(1.0, 0.0, 0.0, 1) // 1 value
	f.Add(0.5, 0.5, 0.0, 2) // 2 values
	f.Add(0.0, 1.0, 0.0, 2) // 2 values
	f.Add(0.1, 0.2, 0.3, 3) // 3 values

	f.Fuzz(func(t *testing.T, val1, val2, val3 float64, count int) {
		if count < 1 || count > 3 {
			t.Skip("count out of range")
		}

		values := []float64{val1}
		if count >= 2 {
			values = append(values, val2)
		}
		if count >= 3 {
			values = append(values, val3)
		}
		if len(values) == 0 {
			t.Skip("empty values array")
		}
		if len(values) > 1000 {
			t.Skip("too many values")
		}

		// Clamp values to valid range and handle special values
		var scores []domain.Score
		var validCount int
		var sum float64

		for _, val := range values {
			if math.IsNaN(val) || math.IsInf(val, 0) {
				val = 0.5 // Replace invalid values
			}
			if val < 0 {
				val = 0
			}
			if val > 1 {
				val = 1
			}

			score := CreateTestScore(TestAnswerUUID1, WithScoreValue(val))
			score.ID = uuid.New().String()
			scores = append(scores, score)

			sum += val
			validCount++
		}

		if validCount == 0 {
			t.Skip("no valid scores")
		}

		result := calculateMean(scores)
		expected := sum / float64(validCount)

		// Verify invariants
		if math.IsNaN(result) || math.IsInf(result, 0) {
			t.Errorf("mean should be finite, got %f", result)
		}

		if result < 0 || result > 1 {
			t.Errorf("mean %f outside valid range [0,1]", result)
		}

		// For reasonable inputs, result should be close to expected
		if math.Abs(result-expected) > 1e-10 {
			t.Errorf("mean calculation mismatch: got %f, expected %f", result, expected)
		}
	})
}

// FuzzCalculateMedian tests median calculation with fuzzed score values.
func FuzzCalculateMedian(f *testing.F) {
	// Seed corpus with edge cases
	f.Add(0.5, 0.0, 0.0, 0.0, 1) // Single value
	f.Add(0.0, 1.0, 0.0, 0.0, 2) // Two values
	f.Add(0.1, 0.5, 0.9, 0.0, 3) // Three values (odd)
	f.Add(0.2, 0.4, 0.6, 0.8, 4) // Four values (even)
	f.Add(0.5, 0.5, 0.5, 0.5, 4) // All same value

	f.Fuzz(func(t *testing.T, val1, val2, val3, val4 float64, count int) {
		if count < 1 || count > 4 {
			t.Skip("count out of range")
		}

		values := []float64{val1}
		if count >= 2 {
			values = append(values, val2)
		}
		if count >= 3 {
			values = append(values, val3)
		}
		if count >= 4 {
			values = append(values, val4)
		}
		if len(values) == 0 {
			t.Skip("empty values array")
		}
		if len(values) > 1000 {
			t.Skip("too many values")
		}

		// Clamp values and create scores
		var scores []domain.Score
		for _, val := range values {
			if math.IsNaN(val) || math.IsInf(val, 0) {
				val = 0.5
			}
			if val < 0 {
				val = 0
			}
			if val > 1 {
				val = 1
			}

			score := CreateTestScore(TestAnswerUUID1, WithScoreValue(val))
			score.ID = uuid.New().String()
			scores = append(scores, score)
		}

		result := calculateMedian(scores)

		// Verify invariants
		if math.IsNaN(result) || math.IsInf(result, 0) {
			t.Errorf("median should be finite, got %f", result)
		}

		if result < 0 || result > 1 {
			t.Errorf("median %f outside valid range [0,1]", result)
		}

		// For single value, median should equal that value
		if len(values) == 1 {
			expected := values[0]
			if math.IsNaN(expected) || math.IsInf(expected, 0) {
				expected = 0.5
			}
			if expected < 0 {
				expected = 0
			}
			if expected > 1 {
				expected = 1
			}
			if math.Abs(result-expected) > epsilon {
				t.Errorf("single value median: got %f, expected %f", result, expected)
			}
		}
	})
}

// FuzzCalculateTrimmedMean tests trimmed mean calculation with fuzzed parameters.
func FuzzCalculateTrimmedMean(f *testing.F) {
	// Seed corpus with edge cases
	f.Add(0.1, 0.5, 0.9, 0.0, 3, 0.0) // No trimming, 3 values
	f.Add(0.1, 0.5, 0.9, 0.0, 3, 0.3) // 30% trimming, 3 values
	f.Add(0.1, 0.5, 0.9, 0.0, 3, 0.5) // 50% trimming (max), 3 values
	f.Add(0.0, 0.2, 0.4, 0.6, 4, 0.2) // 20% trimming, 4 values
	f.Add(0.5, 0.5, 0.5, 0.0, 3, 0.1) // All same values, 3 values

	f.Fuzz(func(t *testing.T, val1, val2, val3, val4 float64, count int, trimFraction float64) {
		if count < 1 || count > 4 {
			t.Skip("count out of range")
		}

		values := []float64{val1}
		if count >= 2 {
			values = append(values, val2)
		}
		if count >= 3 {
			values = append(values, val3)
		}
		if count >= 4 {
			values = append(values, val4)
		}
		if len(values) == 0 {
			t.Skip("empty values array")
		}
		if len(values) > 1000 {
			t.Skip("too many values")
		}

		// Clamp trim fraction to valid range
		if trimFraction < 0 {
			trimFraction = 0
		}
		if trimFraction > 0.5 {
			trimFraction = 0.5
		}

		// Clamp values and create scores
		var scores []domain.Score
		for _, val := range values {
			if math.IsNaN(val) || math.IsInf(val, 0) {
				val = 0.5
			}
			if val < 0 {
				val = 0
			}
			if val > 1 {
				val = 1
			}

			score := CreateTestScore(TestAnswerUUID1, WithScoreValue(val))
			score.ID = uuid.New().String()
			scores = append(scores, score)
		}

		result := calculateTrimmedMean(scores, trimFraction)

		// Verify invariants
		if math.IsNaN(result) || math.IsInf(result, 0) {
			t.Errorf("trimmed mean should be finite, got %f", result)
		}

		if result < 0 || result > 1 {
			t.Errorf("trimmed mean %f outside valid range [0,1]", result)
		}

		// Trimmed mean with trimFraction=0 should equal regular mean
		if trimFraction == 0 {
			expectedMean := calculateMean(scores)
			if math.Abs(result-expectedMean) > epsilon {
				t.Errorf("trimmed mean with 0 trimming should equal mean: got %f, expected %f", result, expectedMean)
			}
		}

		// For all same values, trimmed mean should equal that value
		allSame := true
		if len(values) > 1 {
			firstVal := values[0]
			if math.IsNaN(firstVal) || math.IsInf(firstVal, 0) {
				firstVal = 0.5
			}
			if firstVal < 0 {
				firstVal = 0
			}
			if firstVal > 1 {
				firstVal = 1
			}

			for _, val := range values[1:] {
				normalizedVal := val
				if math.IsNaN(normalizedVal) || math.IsInf(normalizedVal, 0) {
					normalizedVal = 0.5
				}
				if normalizedVal < 0 {
					normalizedVal = 0
				}
				if normalizedVal > 1 {
					normalizedVal = 1
				}
				if math.Abs(normalizedVal-firstVal) > epsilon {
					allSame = false
					break
				}
			}

			if allSame {
				if math.Abs(result-firstVal) > epsilon {
					t.Errorf("trimmed mean of same values should equal that value: got %f, expected %f", result, firstVal)
				}
			}
		}
	})
}

// FuzzDetermineWinnerWithPolicyAggregation tests winner determination with fuzzed inputs.
func FuzzDetermineWinnerWithPolicyAggregation(f *testing.F) {
	// Seed corpus with edge cases
	f.Add(0.5, 0.0, 1, 0.6, 0.0, 1, "mean")         // 1 value each
	f.Add(0.1, 0.9, 2, 0.8, 0.2, 2, "median")       // 2 values each
	f.Add(0.0, 0.5, 2, 0.3, 0.7, 2, "trimmed_mean") // 2 values each

	f.Fuzz(func(t *testing.T, val1a, val1b float64, count1 int, val2a, val2b float64, count2 int, method string) {
		if count1 < 1 || count1 > 2 || count2 < 1 || count2 > 2 {
			t.Skip("count out of range")
		}

		values1 := []float64{val1a}
		if count1 >= 2 {
			values1 = append(values1, val1b)
		}

		values2 := []float64{val2a}
		if count2 >= 2 {
			values2 = append(values2, val2b)
		}
		if len(values1) == 0 || len(values2) == 0 {
			t.Skip("empty values arrays")
		}
		if len(values1) > 100 || len(values2) > 100 {
			t.Skip("too many values")
		}

		// Normalize method
		if method != "mean" && method != "median" && method != "trimmed_mean" {
			method = "mean" // Default to mean for invalid methods
		}

		// Create test data
		answers := CreateTestAnswers(2)
		var scores []domain.Score

		// Add scores for first answer
		for _, val := range values1 {
			if math.IsNaN(val) || math.IsInf(val, 0) {
				val = 0.5
			}
			if val < 0 {
				val = 0
			}
			if val > 1 {
				val = 1
			}
			score := CreateTestScore(answers[0].ID, WithScoreValue(val))
			score.ID = uuid.New().String()
			scores = append(scores, score)
		}

		// Add scores for second answer
		for _, val := range values2 {
			if math.IsNaN(val) || math.IsInf(val, 0) {
				val = 0.5
			}
			if val < 0 {
				val = 0
			}
			if val > 1 {
				val = 1
			}
			score := CreateTestScore(answers[1].ID, WithScoreValue(val))
			score.ID = uuid.New().String()
			scores = append(scores, score)
		}

		// Convert string method to AggregationMethod
		var aggMethod domain.AggregationMethod
		switch method {
		case "mean":
			aggMethod = domain.AggregationMethodMean
		case "median":
			aggMethod = domain.AggregationMethodMedian
		case "trimmed_mean":
			aggMethod = domain.AggregationMethodTrimmedMean
		default:
			t.Skip("unknown method:", method)
		}

		policy := domain.AggregationPolicy{Method: aggMethod}
		if method == "trimmed_mean" {
			policy.TrimFraction = 0.2 // Use reasonable trim fraction
		}

		winnerID, tiedIDs := determineWinnerWithPolicyAggregation(scores, answers, policy)

		// Verify invariants
		if winnerID != "" {
			// Winner should be one of the valid answer IDs
			found := false
			for _, answer := range answers {
				if answer.ID == winnerID {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("winner ID %s not found in answers", winnerID)
			}

			// Tied IDs should not include the winner
			for _, tiedID := range tiedIDs {
				if tiedID == winnerID {
					t.Errorf("winner ID %s should not be in tied IDs", winnerID)
				}
			}

			// All tied IDs should be valid answer IDs
			for _, tiedID := range tiedIDs {
				found := false
				for _, answer := range answers {
					if answer.ID == tiedID {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("tied ID %s not found in answers", tiedID)
				}
			}
		}
	})
}

// FuzzAggregationPolicyValidation tests policy validation with fuzzed inputs.
func FuzzAggregationPolicyValidation(f *testing.F) {
	// Seed corpus with edge cases
	f.Add("mean", 0.0)
	f.Add("median", 0.0)
	f.Add("trimmed_mean", 0.1)
	f.Add("trimmed_mean", 0.5)
	f.Add("invalid_method", 0.2)

	f.Fuzz(func(t *testing.T, method string, trimFraction float64) {
		// Create test input
		scores := []domain.Score{
			CreateTestScore(TestAnswerUUID1, WithScoreValue(0.5)),
		}

		// Convert string method to AggregationMethod
		var aggMethod domain.AggregationMethod
		switch method {
		case "mean":
			aggMethod = domain.AggregationMethodMean
		case "median":
			aggMethod = domain.AggregationMethodMedian
		case "trimmed_mean":
			aggMethod = domain.AggregationMethodTrimmedMean
		default:
			aggMethod = domain.AggregationMethod(method) // Keep invalid methods for validation testing
		}

		policy := domain.AggregationPolicy{
			Method:       aggMethod,
			TrimFraction: trimFraction,
		}

		result, err := applyAggregationPolicy(scores, policy)

		// Verify behavior
		switch method {
		case "mean", "median", "trimmed_mean":
			// Valid methods should succeed
			if err != nil {
				t.Errorf("valid method %s should not error: %v", method, err)
			}
			if math.IsNaN(result) || math.IsInf(result, 0) {
				t.Errorf("result should be finite for valid method: %f", result)
			}
			if result < 0 || result > 1 {
				t.Errorf("result %f outside valid range [0,1]", result)
			}
		default:
			// Invalid methods should error
			if err == nil {
				t.Errorf("invalid method %s should error", method)
			}
		}
	})
}

// FuzzScoreValidation tests score validation with fuzzed reasoning fields.
func FuzzScoreValidation(f *testing.F) {
	// Seed corpus with edge cases
	f.Add(true, true, true)    // Valid with both reasoning types
	f.Add(true, false, true)   // Valid with inline only
	f.Add(true, true, false)   // Valid with ref only
	f.Add(true, false, false)  // Valid with neither
	f.Add(false, true, true)   // Invalid with both
	f.Add(false, false, false) // Invalid with neither

	f.Fuzz(func(t *testing.T, valid, hasInline, hasRef bool) {
		score := CreateTestScore(TestAnswerUUID1, WithScoreValue(0.5))

		// Configure score validity
		if valid {
			score.ScoreValidity = domain.ScoreValidity{Valid: true}
		} else {
			score.ScoreValidity = domain.ScoreValidity{Valid: false, Error: "test error"}
		}

		// Configure reasoning
		if hasInline {
			score.ScoreEvidence.InlineReasoning = "test reasoning"
		} else {
			score.ScoreEvidence.InlineReasoning = ""
		}

		if hasRef {
			score.ScoreEvidence.ReasonRef = domain.ArtifactRef{
				Key:  "test-ref",
				Kind: domain.ArtifactJudgeRationale,
			}
		} else {
			score.ScoreEvidence.ReasonRef = domain.ArtifactRef{}
		}

		err := validateScoreReasoning([]domain.Score{score})

		// Verify validation logic
		if valid {
			// Valid scores must have exactly one reasoning type
			if hasInline == hasRef {
				if err == nil {
					t.Error("valid score with invalid reasoning should error")
				}
			} else {
				if err != nil {
					t.Errorf("valid score with proper reasoning should not error: %v", err)
				}
			}
		} else {
			// Invalid scores can have any reasoning configuration
			if err != nil {
				t.Errorf("invalid score should not cause reasoning validation error: %v", err)
			}
		}
	})
}

// FuzzCostAggregation tests cost aggregation with fuzzed cost values.
func FuzzCostAggregation(f *testing.F) {
	// Seed corpus with edge cases
	f.Add(int64(0), int64(0), int64(0), 1)       // Single zero cost
	f.Add(int64(100), int64(0), int64(0), 1)     // Single positive cost
	f.Add(int64(-50), int64(0), int64(0), 1)     // Single negative cost (edge case)
	f.Add(int64(0), int64(100), int64(200), 3)   // Three different costs
	f.Add(int64(1000000), int64(0), int64(0), 1) // Single large cost

	f.Fuzz(func(t *testing.T, cost1, cost2, cost3 int64, count int) {
		if count < 1 || count > 3 {
			t.Skip("count out of range")
		}

		costs := []int64{cost1}
		if count >= 2 {
			costs = append(costs, cost2)
		}
		if count >= 3 {
			costs = append(costs, cost3)
		}

		var scores []domain.Score
		var expectedTotal int64

		for _, cost := range costs {
			score := CreateTestScore(TestAnswerUUID1, WithScoreCost(cost))
			score.ID = uuid.New().String()
			scores = append(scores, score)
			expectedTotal += cost
		}

		result := aggregateCosts(scores)

		// Verify cost aggregation
		if int64(result) != expectedTotal {
			t.Errorf("cost aggregation mismatch: got %d, expected %d", int64(result), expectedTotal)
		}

		// Cost should handle overflow gracefully (though unlikely in practice)
		if len(costs) > 0 {
			// Basic sanity check - result should be related to input
			if expectedTotal >= 0 && result < 0 {
				// Only error if we expect positive but got negative (potential overflow)
				maxReasonableCost := int64(len(costs)) * 1000000 // 1M cents per score max
				if expectedTotal < maxReasonableCost {
					t.Errorf("unexpected negative result %d for positive costs", result)
				}
			}
		}
	})
}
