package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScore_ComputeOverall(t *testing.T) {
	tests := []struct {
		name          string
		dimensions    []DimensionScore
		weights       map[Dimension]float64
		expectedValue float64
	}{
		{
			name: "compute with default weights",
			dimensions: []DimensionScore{
				{Name: DimCorrectness, Value: 0.9},
				{Name: DimCompleteness, Value: 0.7},
				{Name: DimSafety, Value: 1.0},
				{Name: DimClarity, Value: 0.6},
				{Name: DimRelevance, Value: 0.8},
			},
			weights:       nil,   // Use defaults
			expectedValue: 0.825, // (0.9*0.4 + 0.7*0.25 + 1.0*0.15 + 0.6*0.1 + 0.8*0.1) = 0.825
		},
		{
			name: "compute with custom weights",
			dimensions: []DimensionScore{
				{Name: DimCorrectness, Value: 0.8},
				{Name: DimCompleteness, Value: 0.6},
			},
			weights: map[Dimension]float64{
				DimCorrectness:  0.7,
				DimCompleteness: 0.3,
			},
			expectedValue: 0.74, // (0.8*0.7 + 0.6*0.3) = 0.74
		},
		{
			name:          "no dimensions",
			dimensions:    []DimensionScore{},
			weights:       nil,
			expectedValue: 0.0, // Value should remain unchanged
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := &Score{
				ScoreEvidence: ScoreEvidence{
					Dimensions: tt.dimensions,
				},
				Value: 0.0, // Start with 0
			}

			score.ComputeOverall(tt.weights)

			assert.InDelta(t, tt.expectedValue, score.Value, 0.0001,
				"ComputeOverall() should compute correct value")
		})
	}
}

func TestScore_GetDimensionScore(t *testing.T) {
	score := &Score{
		ScoreEvidence: ScoreEvidence{
			Dimensions: []DimensionScore{
				{Name: DimCorrectness, Value: 0.9},
				{Name: DimCompleteness, Value: 0.7},
				{Name: DimSafety, Value: 1.0},
			},
		},
	}

	tests := []struct {
		name           string
		dimension      Dimension
		expectedValue  float64
		expectedExists bool
	}{
		{"get correctness", DimCorrectness, 0.9, true},
		{"get completeness", DimCompleteness, 0.7, true},
		{"get safety", DimSafety, 1.0, true},
		{"get non-existent", DimClarity, 0.0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, exists := score.GetDimensionScore(tt.dimension)

			assert.Equal(t, tt.expectedValue, got, "GetDimensionScore should return correct value")
			assert.Equal(t, tt.expectedExists, exists, "GetDimensionScore should return correct exists flag")
		})
	}
}

func TestScore_HasDimensions(t *testing.T) {
	tests := []struct {
		name       string
		dimensions []DimensionScore
		expected   bool
	}{
		{"has dimensions", []DimensionScore{{Name: DimCorrectness, Value: 0.9}}, true},
		{"no dimensions", []DimensionScore{}, false},
		{"nil dimensions", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := &Score{
				ScoreEvidence: ScoreEvidence{
					Dimensions: tt.dimensions,
				},
			}
			got := score.HasDimensions()
			assert.Equal(t, tt.expected, got, "HasDimensions should return correct value")
		})
	}
}

func TestAggregateDimensionalScores(t *testing.T) {
	scores := []Score{
		{
			ScoreEvidence: ScoreEvidence{
				Dimensions: []DimensionScore{
					{Name: DimCorrectness, Value: 0.8},
					{Name: DimCompleteness, Value: 0.7},
				},
			},
			ScoreValidity: ScoreValidity{
				Valid: true,
			},
		},
		{
			ScoreEvidence: ScoreEvidence{
				Dimensions: []DimensionScore{
					{Name: DimCorrectness, Value: 0.9},
					{Name: DimCompleteness, Value: 0.6},
					{Name: DimSafety, Value: 1.0},
				},
			},
			ScoreValidity: ScoreValidity{
				Valid: true,
			},
		},
		{
			ScoreEvidence: ScoreEvidence{
				Dimensions: []DimensionScore{
					{Name: DimCorrectness, Value: 0.0},
				},
			},
			ScoreValidity: ScoreValidity{
				Valid: false, // This score should be ignored
			},
		},
	}

	result := AggregateDimensionalScores(scores)

	expectedCorrectness := 0.85  // (0.8 + 0.9) / 2
	expectedCompleteness := 0.65 // (0.7 + 0.6) / 2
	expectedSafety := 1.0        // 1.0 / 1

	require.NotNil(t, result, "AggregateDimensionalScores should return non-nil result")

	assert.InDelta(t, expectedCorrectness, result[DimCorrectness], 0.0001,
		"Aggregate correctness should be correct")
	assert.InDelta(t, expectedCompleteness, result[DimCompleteness], 0.0001,
		"Aggregate completeness should be correct")
	assert.InDelta(t, expectedSafety, result[DimSafety], 0.0001,
		"Aggregate safety should be correct")

	// Should not include dimensions from invalid scores
	_, exists := result[DimClarity]
	assert.False(t, exists, "Result should not include dimensions from invalid scores")
}

func TestAggregateDimensionalScores_EmptyInput(t *testing.T) {
	result := AggregateDimensionalScores([]Score{})
	assert.Nil(t, result, "AggregateDimensionalScores should return nil for empty input")
}

func TestAggregateDimensionalScores_NoValidScores(t *testing.T) {
	scores := []Score{
		{
			ScoreEvidence: ScoreEvidence{
				Dimensions: []DimensionScore{{Name: DimCorrectness, Value: 0.5}},
			},
			ScoreValidity: ScoreValidity{
				Valid: false,
			},
		},
		{
			ScoreEvidence: ScoreEvidence{
				Dimensions: []DimensionScore{{Name: DimCorrectness, Value: 0.6}},
			},
			ScoreValidity: ScoreValidity{
				Valid: true,
				Error: "some error",
			},
		},
	}

	result := AggregateDimensionalScores(scores)
	assert.Nil(t, result, "AggregateDimensionalScores should return nil when no valid scores")
}
