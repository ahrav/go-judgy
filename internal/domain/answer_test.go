package domain //nolint:testpackage // Need access to unexported validate

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAnswer_Validate verifies that Answer.Validate() correctly enforces
// struct validation tags including UUID format, required fields, and numeric constraints.
func TestAnswer_Validate(t *testing.T) {
	validAnswer := &Answer{
		ID: "123e4567-e89b-12d3-a456-426614174000",
		ContentRef: ArtifactRef{
			Key:  "answers/2025/08/123e4567-e89b-12d3-a456-426614174000.txt",
			Size: 32,
			Kind: ArtifactAnswer,
		},
		AnswerProvenance: AnswerProvenance{
			Provider:    "openai",
			Model:       "gpt-4",
			GeneratedAt: time.Now(),
		},
		AnswerUsage: AnswerUsage{
			LatencyMillis:    150,
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
		},
		AnswerCost: AnswerCost{
			EstimatedCost: Cents(5),
		},
		AnswerState: AnswerState{
			Truncated:  false,
			Error:      "",
			RetryCount: 0,
		},
	}

	tests := []struct {
		name    string
		modify  func(*Answer)
		wantErr bool
	}{
		{
			name:    "valid answer",
			modify:  func(_ *Answer) {},
			wantErr: false,
		},
		{
			name: "empty ID",
			modify: func(a *Answer) {
				a.ID = ""
			},
			wantErr: true,
		},
		{
			name: "invalid UUID",
			modify: func(a *Answer) {
				a.ID = "not-a-uuid"
			},
			wantErr: true,
		},
		{
			name: "empty content ref key",
			modify: func(a *Answer) {
				a.ContentRef.Key = ""
			},
			wantErr: true,
		},
		{
			name: "empty provider",
			modify: func(a *Answer) {
				a.Provider = ""
			},
			wantErr: true,
		},
		{
			name: "empty model",
			modify: func(a *Answer) {
				a.Model = ""
			},
			wantErr: true,
		},
		{
			name: "zero time",
			modify: func(a *Answer) {
				a.GeneratedAt = time.Time{}
			},
			wantErr: true,
		},
		{
			name: "negative latency",
			modify: func(a *Answer) {
				a.LatencyMillis = -1
			},
			wantErr: true,
		},
		{
			name: "negative tokens",
			modify: func(a *Answer) {
				a.PromptTokens = -1
			},
			wantErr: true,
		},
		{
			name: "negative retry count",
			modify: func(a *Answer) {
				a.RetryCount = -1
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			answer := *validAnswer
			tt.modify(&answer)

			err := answer.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestAnswer_IsValid verifies that IsValid() correctly identifies answers
// suitable for scoring by checking content availability and error status.
func TestAnswer_IsValid(t *testing.T) {
	tests := []struct {
		name   string
		answer Answer
		want   bool
	}{
		{
			name: "valid answer",
			answer: Answer{
				ContentRef: ArtifactRef{Key: "answers/valid.txt", Size: 100, Kind: ArtifactAnswer},
				AnswerState: AnswerState{
					Error: "",
				},
			},
			want: true,
		},
		{
			name: "answer with error",
			answer: Answer{
				ContentRef: ArtifactRef{Key: "answers/content.txt", Size: 100, Kind: ArtifactAnswer},
				AnswerState: AnswerState{
					Error: "API error",
				},
			},
			want: false,
		},
		{
			name: "empty content",
			answer: Answer{
				ContentRef: ArtifactRef{Key: "", Size: 0, Kind: ArtifactAnswer},
				AnswerState: AnswerState{
					Error: "",
				},
			},
			want: false,
		},
		{
			name: "empty content with error",
			answer: Answer{
				ContentRef: ArtifactRef{Key: "", Size: 0, Kind: ArtifactAnswer},
				AnswerState: AnswerState{
					Error: "Failed to generate",
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.answer.IsValid())
		})
	}
}

// TestAnswer_CalculateTotalTokens verifies that CalculateTotalTokens()
// correctly sums prompt and completion tokens and updates TotalTokens field.
func TestAnswer_CalculateTotalTokens(t *testing.T) {
	answer := Answer{
		AnswerUsage: AnswerUsage{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      0,
		},
	}

	answer.CalculateTotalTokens()
	assert.Equal(t, int64(150), answer.TotalTokens)

	answer.PromptTokens = 200
	answer.CompletionTokens = 75
	answer.CalculateTotalTokens()
	assert.Equal(t, int64(275), answer.TotalTokens)
}

// TestGenerateAnswersInput_Validate verifies that input validation enforces
// question length constraints, answer count limits, and embedded config validation.
func TestGenerateAnswersInput_Validate(t *testing.T) {
	tests := []struct {
		name    string
		input   GenerateAnswersInput
		wantErr bool
	}{
		{
			name: "valid input",
			input: GenerateAnswersInput{
				Question:   "What is the capital of France?",
				NumAnswers: 3,
				Config:     DefaultEvalConfig(),
			},
			wantErr: false,
		},
		{
			name: "question too short",
			input: GenerateAnswersInput{
				Question:   "Hi",
				NumAnswers: 3,
				Config:     DefaultEvalConfig(),
			},
			wantErr: true,
		},
		{
			name: "question too long",
			input: GenerateAnswersInput{
				Question:   string(make([]byte, 1001)),
				NumAnswers: 3,
				Config:     DefaultEvalConfig(),
			},
			wantErr: true,
		},
		{
			name: "num answers too low",
			input: GenerateAnswersInput{
				Question:   "What is the capital of France?",
				NumAnswers: 0,
				Config:     DefaultEvalConfig(),
			},
			wantErr: true,
		},
		{
			name: "num answers too high",
			input: GenerateAnswersInput{
				Question:   "What is the capital of France?",
				NumAnswers: 11,
				Config:     DefaultEvalConfig(),
			},
			wantErr: true,
		},
		{
			name: "invalid config",
			input: GenerateAnswersInput{
				Question:   "What is the capital of France?",
				NumAnswers: 3,
				Config: EvalConfig{
					MaxAnswers:      3,
					MaxAnswerTokens: 10, // Too low
					Temperature:     0.7,
					Provider:        "openai",
					Timeout:         60,
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.input.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestGenerateAnswersOutput_Validate verifies that output validation enforces
// at least one answer, non-negative metrics, and proper structure requirements.
func TestGenerateAnswersOutput_Validate(t *testing.T) {
	validAnswer := Answer{
		ID: "123e4567-e89b-12d3-a456-426614174000",
		ContentRef: ArtifactRef{
			Key:  "answers/valid-content.txt",
			Size: 100,
			Kind: ArtifactAnswer,
		},
		AnswerProvenance: AnswerProvenance{
			Provider:    "openai",
			Model:       "gpt-4",
			GeneratedAt: time.Now(),
		},
	}

	tests := []struct {
		name    string
		output  GenerateAnswersOutput
		wantErr bool
	}{
		{
			name: "valid output",
			output: GenerateAnswersOutput{
				Answers:    []Answer{validAnswer},
				TokensUsed: 100,
				CallsMade:  1,
			},
			wantErr: false,
		},
		{
			name: "empty answers",
			output: GenerateAnswersOutput{
				Answers:    []Answer{},
				TokensUsed: 100,
				CallsMade:  1,
			},
			wantErr: true,
		},
		{
			name: "negative tokens",
			output: GenerateAnswersOutput{
				Answers:    []Answer{validAnswer},
				TokensUsed: -1,
				CallsMade:  1,
			},
			wantErr: true,
		},
		{
			name: "negative calls",
			output: GenerateAnswersOutput{
				Answers:    []Answer{validAnswer},
				TokensUsed: 100,
				CallsMade:  -1,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.output.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestGenerateAnswersOutput_CalculateTotalTokens verifies that token calculation
// correctly sums TotalTokens across all answers and handles edge cases.
func TestGenerateAnswersOutput_CalculateTotalTokens(t *testing.T) {
	output := GenerateAnswersOutput{
		Answers: []Answer{
			{AnswerUsage: AnswerUsage{TotalTokens: 100}},
			{AnswerUsage: AnswerUsage{TotalTokens: 150}},
			{AnswerUsage: AnswerUsage{TotalTokens: 75}},
		},
		TokensUsed: 0,
	}

	output.CalculateTotalTokens()
	assert.Equal(t, int64(325), output.TokensUsed)

	output2 := GenerateAnswersOutput{
		Answers:    []Answer{},
		TokensUsed: 0,
	}
	output2.CalculateTotalTokens()
	assert.Equal(t, int64(0), output2.TokensUsed)

	output3 := GenerateAnswersOutput{
		Answers: []Answer{
			{AnswerUsage: AnswerUsage{TotalTokens: 500}},
		},
		TokensUsed: 0,
	}
	output3.CalculateTotalTokens()
	assert.Equal(t, int64(500), output3.TokensUsed)
}

// TestAnswer_Metadata verifies that the Metadata field correctly stores
// provider-specific key-value pairs and handles nil maps appropriately.
func TestAnswer_Metadata(t *testing.T) {
	answer := Answer{
		ID: "223e4567-e89b-12d3-a456-426614174000",
		ContentRef: ArtifactRef{
			Key:  "answers/test-content.txt",
			Size: 100,
			Kind: ArtifactAnswer,
		},
		Metadata: make(map[string]any),
	}

	answer.Metadata["key1"] = "value1"
	answer.Metadata["key2"] = 42
	answer.Metadata["key3"] = true

	assert.Equal(t, "value1", answer.Metadata["key1"])
	assert.Equal(t, 42, answer.Metadata["key2"])
	assert.Equal(t, true, answer.Metadata["key3"])

	answer2 := Answer{
		ID: "323e4567-e89b-12d3-a456-426614174000",
		ContentRef: ArtifactRef{
			Key:  "answers/test-content2.txt",
			Size: 100,
			Kind: ArtifactAnswer,
		},
	}
	assert.Nil(t, answer2.Metadata)
}

// TestAnswer_ErrorHandling verifies that error scenarios are properly
// tracked through Error and RetryCount fields, affecting IsValid() results.
func TestAnswer_ErrorHandling(t *testing.T) {
	answer := Answer{
		ID: "423e4567-e89b-12d3-a456-426614174000",
		ContentRef: ArtifactRef{
			Key:  "",
			Size: 0,
			Kind: ArtifactAnswer,
		},
		AnswerState: AnswerState{
			Error:      "Rate limit exceeded",
			RetryCount: 3,
		},
	}

	assert.False(t, answer.IsValid())
	assert.Equal(t, "Rate limit exceeded", answer.Error)
	assert.Equal(t, 3, answer.RetryCount)

	answer2 := Answer{
		ID: "523e4567-e89b-12d3-a456-426614174000",
		ContentRef: ArtifactRef{
			Key:  "answers/valid-response.txt",
			Size: 100,
			Kind: ArtifactAnswer,
		},
		AnswerState: AnswerState{
			Error:      "",
			RetryCount: 0,
		},
	}

	assert.True(t, answer2.IsValid())
	assert.Empty(t, answer2.Error)
	assert.Equal(t, 0, answer2.RetryCount)
}

// TestAnswer_Truncation verifies that the Truncated field correctly
// indicates when responses exceeded length limits during generation.
func TestAnswer_Truncation(t *testing.T) {
	answer := Answer{
		ID: "623e4567-e89b-12d3-a456-426614174000",
		ContentRef: ArtifactRef{
			Key:  "answers/truncated.txt",
			Size: 100,
			Kind: ArtifactAnswer,
		},
		AnswerState: AnswerState{
			Truncated: true,
		},
	}

	assert.True(t, answer.Truncated)

	answer2 := Answer{
		ID: "723e4567-e89b-12d3-a456-426614174000",
		ContentRef: ArtifactRef{
			Key:  "answers/complete.txt",
			Size: 200,
			Kind: ArtifactAnswer,
		},
		AnswerState: AnswerState{
			Truncated: false,
		},
	}

	assert.False(t, answer2.Truncated)
}

// TestGenerateAnswersInput_ConfigValidation verifies that nested config
// validation is properly enforced when validating GenerateAnswersInput.
func TestGenerateAnswersInput_ConfigValidation(t *testing.T) {
	// Test that the config is validated when validating input
	input := GenerateAnswersInput{
		Question:   "What is the capital of France?",
		NumAnswers: 3,
		Config: EvalConfig{
			MaxAnswers:      3,
			MaxAnswerTokens: 1000,
			Temperature:     0.7,
			ScoreThreshold:  0.7,
			Provider:        "", // Empty provider is invalid
			Timeout:         60,
		},
	}

	err := input.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Provider")
}
