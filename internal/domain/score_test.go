package domain //nolint:testpackage // Need access to unexported validate

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScore_Validate(t *testing.T) {
	validScore := &Score{
		ID:         "123e4567-e89b-12d3-a456-426614174001",
		AnswerID:   "123e4567-e89b-12d3-a456-426614174000",
		Value:      0.8,
		Confidence: 0.9,
		ScoreEvidence: ScoreEvidence{
			ReasonRef: ArtifactRef{
				Key:  "scores/reasoning-1.txt",
				Size: 35,
				Kind: ArtifactJudgeRationale,
			},
		},
		ScoreProvenance: ScoreProvenance{
			JudgeID:  "judge-1",
			Provider: "openai",
			Model:    "gpt-4",
			ScoredAt: time.Now(),
		},
		ScoreUsage: ScoreUsage{
			LatencyMs:  100,
			TokensUsed: 50,
		},
		ScoreValidity: ScoreValidity{
			Valid: true,
		},
	}

	tests := []struct {
		name    string
		modify  func(*Score)
		wantErr bool
	}{
		{
			name:    "valid score",
			modify:  func(_ *Score) {},
			wantErr: false,
		},
		{
			name: "empty ID",
			modify: func(s *Score) {
				s.ID = ""
			},
			wantErr: true,
		},
		{
			name: "invalid answer ID",
			modify: func(s *Score) {
				s.AnswerID = "not-a-uuid"
			},
			wantErr: true,
		},
		{
			name: "value below minimum",
			modify: func(s *Score) {
				s.Value = -0.1
			},
			wantErr: true,
		},
		{
			name: "value above maximum",
			modify: func(s *Score) {
				s.Value = 1.1
			},
			wantErr: true,
		},
		{
			name: "confidence below minimum",
			modify: func(s *Score) {
				s.Confidence = -0.1
			},
			wantErr: true,
		},
		{
			name: "confidence above maximum",
			modify: func(s *Score) {
				s.Confidence = 1.1
			},
			wantErr: true,
		},
		{
			name: "invalid reason ref key",
			modify: func(s *Score) {
				s.ReasonRef.Key = ""
			},
			wantErr: true,
		},
		{
			name: "empty judge ID",
			modify: func(s *Score) {
				s.JudgeID = ""
			},
			wantErr: true,
		},
		{
			name: "negative latency",
			modify: func(s *Score) {
				s.LatencyMs = -1
			},
			wantErr: true,
		},
		{
			name: "negative tokens",
			modify: func(s *Score) {
				s.TokensUsed = -1
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := *validScore // Create a copy
			tt.modify(&score)

			err := score.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestScore_IsValid(t *testing.T) {
	tests := []struct {
		name  string
		score Score
		want  bool
	}{
		{
			name: "valid score",
			score: Score{
				ScoreValidity: ScoreValidity{
					Valid: true,
					Error: "",
				},
			},
			want: true,
		},
		{
			name: "invalid score",
			score: Score{
				ScoreValidity: ScoreValidity{
					Valid: false,
					Error: "",
				},
			},
			want: false,
		},
		{
			name: "score with error",
			score: Score{
				ScoreValidity: ScoreValidity{
					Valid: true,
					Error: "Scoring failed",
				},
			},
			want: false,
		},
		{
			name: "invalid with error",
			score: Score{
				ScoreValidity: ScoreValidity{
					Valid: false,
					Error: "Scoring failed",
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.score.IsValid())
		})
	}
}

func TestScoreAnswersInput_Validate(t *testing.T) {
	validAnswer := Answer{
		ID: "823e4567-e89b-12d3-a456-426614174000",
		ContentRef: ArtifactRef{
			Key:  "answers/valid-content.txt",
			Size: 13,
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
		input   ScoreAnswersInput
		wantErr bool
	}{
		{
			name: "valid input",
			input: ScoreAnswersInput{
				Question: "What is the capital of France?",
				Answers:  []Answer{validAnswer},
				Config:   DefaultEvalConfig(),
			},
			wantErr: false,
		},
		{
			name: "question too short",
			input: ScoreAnswersInput{
				Question: "Hi",
				Answers:  []Answer{validAnswer},
				Config:   DefaultEvalConfig(),
			},
			wantErr: true,
		},
		{
			name: "empty answers",
			input: ScoreAnswersInput{
				Question: "What is the capital of France?",
				Answers:  []Answer{},
				Config:   DefaultEvalConfig(),
			},
			wantErr: true,
		},
		{
			name: "invalid config",
			input: ScoreAnswersInput{
				Question: "What is the capital of France?",
				Answers:  []Answer{validAnswer},
				Config: EvalConfig{
					MaxAnswers:      0, // Invalid
					MaxAnswerTokens: 1000,
					Temperature:     0.7,
					ScoreThreshold:  0.7,
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

func TestScoreAnswersOutput_Validate(t *testing.T) {
	validScore := Score{
		ID:         "123e4567-e89b-12d3-a456-426614174001",
		AnswerID:   "123e4567-e89b-12d3-a456-426614174000",
		Value:      0.8,
		Confidence: 0.9,
		ScoreEvidence: ScoreEvidence{
			ReasonRef: ArtifactRef{
				Key:  "scores/reasoning-2.txt",
				Size: 11,
				Kind: ArtifactJudgeRationale,
			},
		},
		ScoreProvenance: ScoreProvenance{
			JudgeID:  "judge-1",
			Provider: "openai",
			Model:    "gpt-4",
			ScoredAt: time.Now(),
		},
		ScoreValidity: ScoreValidity{
			Valid: true,
		},
	}

	tests := []struct {
		name    string
		output  ScoreAnswersOutput
		wantErr bool
	}{
		{
			name: "valid output",
			output: ScoreAnswersOutput{
				Scores:     []Score{validScore},
				TokensUsed: 100,
				CallsMade:  1,
			},
			wantErr: false,
		},
		{
			name: "empty scores",
			output: ScoreAnswersOutput{
				Scores:     []Score{},
				TokensUsed: 100,
				CallsMade:  1,
			},
			wantErr: true,
		},
		{
			name: "negative tokens",
			output: ScoreAnswersOutput{
				Scores:     []Score{validScore},
				TokensUsed: -1,
				CallsMade:  1,
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

func TestTotalTokens(t *testing.T) {
	tests := []struct {
		name   string
		scores []Score
		want   int64
	}{
		{
			name: "multiple scores",
			scores: []Score{
				{ScoreUsage: ScoreUsage{TokensUsed: 50}},
				{ScoreUsage: ScoreUsage{TokensUsed: 75}},
				{ScoreUsage: ScoreUsage{TokensUsed: 100}},
			},
			want: 225,
		},
		{
			name:   "empty scores",
			scores: []Score{},
			want:   0,
		},
		{
			name: "single score",
			scores: []Score{
				{
					ScoreUsage: ScoreUsage{
						TokensUsed: 123,
					},
				},
			},
			want: 123,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TotalTokens(tt.scores)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAggregateScoresInput_Validate(t *testing.T) {
	validScore := Score{
		ID:         "123e4567-e89b-12d3-a456-426614174001",
		AnswerID:   "123e4567-e89b-12d3-a456-426614174000",
		Value:      0.8,
		Confidence: 0.9,
		ScoreEvidence: ScoreEvidence{
			ReasonRef: ArtifactRef{
				Key:  "scores/reasoning-3.txt",
				Size: 4,
				Kind: ArtifactJudgeRationale,
			},
		},
		ScoreProvenance: ScoreProvenance{
			JudgeID:  "judge-1",
			Provider: "openai",
			Model:    "gpt-4",
			ScoredAt: time.Now(),
		},
		ScoreValidity: ScoreValidity{
			Valid: true,
		},
	}

	validAnswer := Answer{
		ID: "823e4567-e89b-12d3-a456-426614174000",
		ContentRef: ArtifactRef{
			Key:  "answers/valid-content-agg.txt",
			Size: 13,
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
		input   AggregateScoresInput
		wantErr bool
	}{
		{
			name: "valid input",
			input: AggregateScoresInput{
				Scores:  []Score{validScore},
				Answers: []Answer{validAnswer},
			},
			wantErr: false,
		},
		{
			name: "empty scores",
			input: AggregateScoresInput{
				Scores:  []Score{},
				Answers: []Answer{validAnswer},
			},
			wantErr: true,
		},
		{
			name: "empty answers",
			input: AggregateScoresInput{
				Scores:  []Score{validScore},
				Answers: []Answer{},
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

func TestAggregateScoresOutput_Validate(t *testing.T) {
	validAnswer := Answer{
		ID: "823e4567-e89b-12d3-a456-426614174000",
		ContentRef: ArtifactRef{
			Key:  "answers/valid-content-output.txt",
			Size: 13,
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
		output  AggregateScoresOutput
		wantErr bool
	}{
		{
			name: "valid output with winner",
			output: AggregateScoresOutput{
				WinnerAnswer:   &validAnswer,
				AggregateScore: 0.8,
			},
			wantErr: false,
		},
		{
			name: "valid output without winner",
			output: AggregateScoresOutput{
				WinnerAnswer:   nil,
				AggregateScore: 0.5,
			},
			wantErr: false,
		},
		{
			name: "score below minimum",
			output: AggregateScoresOutput{
				WinnerAnswer:   &validAnswer,
				AggregateScore: -0.1,
			},
			wantErr: true,
		},
		{
			name: "score above maximum",
			output: AggregateScoresOutput{
				WinnerAnswer:   &validAnswer,
				AggregateScore: 1.1,
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

func TestAggregateScores(t *testing.T) {
	tests := []struct {
		name     string
		scores   []Score
		expected float64
	}{
		{
			name: "simple average",
			scores: []Score{
				{Value: 0.8, ScoreValidity: ScoreValidity{Valid: true}},
				{Value: 0.6, ScoreValidity: ScoreValidity{Valid: true}},
			},
			expected: 0.7,
		},
		{
			name: "with invalid scores",
			scores: []Score{
				{Value: 0.8, ScoreValidity: ScoreValidity{Valid: true}},
				{Value: 0.6, ScoreValidity: ScoreValidity{Valid: true}},
				{Value: 0.2, ScoreValidity: ScoreValidity{Valid: false}}, // Should be ignored
			},
			expected: 0.7,
		},
		{
			name: "with error scores",
			scores: []Score{
				{Value: 0.8, ScoreValidity: ScoreValidity{Valid: true, Error: ""}},
				{Value: 0.6, ScoreValidity: ScoreValidity{Valid: true, Error: ""}},
				{Value: 0.9, ScoreValidity: ScoreValidity{Valid: true, Error: "Failed"}}, // Should be ignored
			},
			expected: 0.7,
		},
		{
			name:     "empty scores",
			scores:   []Score{},
			expected: 0,
		},
		{
			name: "all invalid scores",
			scores: []Score{
				{Value: 0.8, ScoreValidity: ScoreValidity{Valid: false}},
				{Value: 0.6, ScoreValidity: ScoreValidity{Valid: false}},
			},
			expected: 0,
		},
		{
			name: "single valid score",
			scores: []Score{
				{Value: 0.85, ScoreValidity: ScoreValidity{Valid: true}},
			},
			expected: 0.85,
		},
		{
			name: "multiple scores same value",
			scores: []Score{
				{Value: 0.75, ScoreValidity: ScoreValidity{Valid: true}},
				{Value: 0.75, ScoreValidity: ScoreValidity{Valid: true}},
				{Value: 0.75, ScoreValidity: ScoreValidity{Valid: true}},
			},
			expected: 0.75,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := AggregateScores(tt.scores)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestScore_EdgeValues(t *testing.T) {
	// Test minimum valid values
	scoreMin := Score{
		ID:         "123e4567-e89b-12d3-a456-426614174001",
		AnswerID:   "123e4567-e89b-12d3-a456-426614174000",
		Value:      0.0,
		Confidence: 0.0,
		ScoreEvidence: ScoreEvidence{
			ReasonRef: ArtifactRef{
				Key:  "scores/reasoning-min.txt",
				Size: 1,
				Kind: ArtifactJudgeRationale,
			},
		},
		ScoreProvenance: ScoreProvenance{
			JudgeID:  "j",
			Provider: "p",
			Model:    "m",
			ScoredAt: time.Now(),
		},
		ScoreUsage: ScoreUsage{
			LatencyMs:  0,
			TokensUsed: 0,
		},
		ScoreValidity: ScoreValidity{
			Valid: true,
		},
	}
	err := scoreMin.Validate()
	assert.NoError(t, err)

	// Test maximum valid values
	scoreMax := Score{
		ID:         "123e4567-e89b-12d3-a456-426614174002",
		AnswerID:   "123e4567-e89b-12d3-a456-426614174000",
		Value:      1.0,
		Confidence: 1.0,
		ScoreEvidence: ScoreEvidence{
			ReasonRef: ArtifactRef{
				Key:  "scores/reasoning-max.txt",
				Size: 500,
				Kind: ArtifactJudgeRationale,
			},
		},
		ScoreProvenance: ScoreProvenance{
			JudgeID:  "judge",
			Provider: "provider",
			Model:    "model",
			ScoredAt: time.Now(),
		},
		ScoreUsage: ScoreUsage{
			LatencyMs:  999999,
			TokensUsed: 999999,
		},
		ScoreValidity: ScoreValidity{
			Valid: true,
		},
	}
	err = scoreMax.Validate()
	assert.NoError(t, err)
}

func TestScore_ReasoningLength(t *testing.T) {
	// Test artifact reference for reasoning
	score := Score{
		ID:         "123e4567-e89b-12d3-a456-426614174001",
		AnswerID:   "123e4567-e89b-12d3-a456-426614174000",
		Value:      0.5,
		Confidence: 0.5,
		ScoreEvidence: ScoreEvidence{
			ReasonRef: ArtifactRef{
				Key:  "scores/reasoning-500.txt",
				Size: 500,
				Kind: ArtifactJudgeRationale,
			},
		},
		ScoreProvenance: ScoreProvenance{
			JudgeID:  "judge",
			Provider: "provider",
			Model:    "model",
			ScoredAt: time.Now(),
		},
		ScoreValidity: ScoreValidity{
			Valid: true,
		},
	}

	err := score.Validate()
	assert.NoError(t, err)

	// Test invalid artifact reference (should fail)
	score.ReasonRef.Key = ""
	err = score.Validate()
	assert.Error(t, err)
}

func TestNewScore(t *testing.T) {
	tests := []struct {
		name      string
		reasonRef ArtifactRef
		wantErr   bool
		errMsg    string
	}{
		{
			name: "valid score with correct reason ref kind",
			reasonRef: ArtifactRef{
				Key:  "scores/reasoning.txt",
				Size: 100,
				Kind: ArtifactJudgeRationale,
			},
			wantErr: false,
		},
		{
			name: "invalid score with wrong reason ref kind",
			reasonRef: ArtifactRef{
				Key:  "scores/reasoning.txt",
				Size: 100,
				Kind: ArtifactAnswer, // Wrong kind
			},
			wantErr: true,
			errMsg:  "reason_ref.kind must be 'judge_rationale'",
		},
		{
			name: "invalid score with empty reason ref kind",
			reasonRef: ArtifactRef{
				Key:  "scores/reasoning.txt",
				Size: 100,
				Kind: "", // Empty kind
			},
			wantErr: true,
			errMsg:  "reason_ref.kind must be 'judge_rationale'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score, err := NewScore(
				"123e4567-e89b-12d3-a456-426614174001",
				"123e4567-e89b-12d3-a456-426614174000",
				"judge-1",
				"openai",
				"gpt-4",
				tt.reasonRef,
			)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Nil(t, score)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, score)
				assert.Equal(t, "123e4567-e89b-12d3-a456-426614174001", score.ID)
				assert.Equal(t, ArtifactJudgeRationale, score.ReasonRef.Kind)
				assert.True(t, score.Valid)
			}
		})
	}
}

func TestScore_SetScoreValues(t *testing.T) {
	score := &Score{}

	tests := []struct {
		name          string
		value         float64
		confidence    float64
		expectedValue float64
		expectedConf  float64
	}{
		{
			name:          "normal values",
			value:         0.75,
			confidence:    0.85,
			expectedValue: 0.75,
			expectedConf:  0.85,
		},
		{
			name:          "clamp negative value",
			value:         -0.5,
			confidence:    0.5,
			expectedValue: 0,
			expectedConf:  0.5,
		},
		{
			name:          "clamp value above 1",
			value:         1.5,
			confidence:    0.9,
			expectedValue: 1,
			expectedConf:  0.9,
		},
		{
			name:          "clamp negative confidence",
			value:         0.5,
			confidence:    -0.2,
			expectedValue: 0.5,
			expectedConf:  0,
		},
		{
			name:          "clamp confidence above 1",
			value:         0.7,
			confidence:    1.2,
			expectedValue: 0.7,
			expectedConf:  1,
		},
		{
			name:          "clamp both negative",
			value:         -0.5,
			confidence:    -0.3,
			expectedValue: 0,
			expectedConf:  0,
		},
		{
			name:          "clamp both above 1",
			value:         1.5,
			confidence:    2.0,
			expectedValue: 1,
			expectedConf:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score.SetScoreValues(tt.value, tt.confidence)
			assert.Equal(t, tt.expectedValue, score.Value)
			assert.Equal(t, tt.expectedConf, score.Confidence)
		})
	}
}

func TestDetermineWinner(t *testing.T) {
	answer1 := Answer{
		ID: "123e4567-e89b-12d3-a456-426614174000",
		ContentRef: ArtifactRef{
			Key:  "answers/answer1.txt",
			Size: 100,
			Kind: ArtifactAnswer,
		},
		AnswerProvenance: AnswerProvenance{
			Provider:    "openai",
			Model:       "gpt-4",
			GeneratedAt: time.Now(),
		},
	}

	answer2 := Answer{
		ID: "223e4567-e89b-12d3-a456-426614174000",
		ContentRef: ArtifactRef{
			Key:  "answers/answer2.txt",
			Size: 100,
			Kind: ArtifactAnswer,
		},
		AnswerProvenance: AnswerProvenance{
			Provider:    "openai",
			Model:       "gpt-4",
			GeneratedAt: time.Now(),
		},
	}

	answer3 := Answer{
		ID: "323e4567-e89b-12d3-a456-426614174000",
		ContentRef: ArtifactRef{
			Key:  "answers/answer3.txt",
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
		name       string
		scores     []Score
		answers    []Answer
		expectedID string
		expectNil  bool
	}{
		{
			name: "single winner",
			scores: []Score{
				{AnswerID: answer1.ID, Value: 0.8, ScoreValidity: ScoreValidity{Valid: true}},
				{AnswerID: answer2.ID, Value: 0.6, ScoreValidity: ScoreValidity{Valid: true}},
			},
			answers:    []Answer{answer1, answer2},
			expectedID: answer1.ID,
		},
		{
			name: "tie-breaking by lowest ID",
			scores: []Score{
				{AnswerID: answer1.ID, Value: 0.8, ScoreValidity: ScoreValidity{Valid: true}},
				{AnswerID: answer2.ID, Value: 0.8, ScoreValidity: ScoreValidity{Valid: true}}, // Same score
			},
			answers:    []Answer{answer1, answer2},
			expectedID: answer1.ID, // Lower ID wins
		},
		{
			name: "all invalid scores",
			scores: []Score{
				{AnswerID: answer1.ID, Value: 0.8, ScoreValidity: ScoreValidity{Valid: false}},
				{AnswerID: answer2.ID, Value: 0.6, ScoreValidity: ScoreValidity{Valid: false}},
			},
			answers:   []Answer{answer1, answer2},
			expectNil: true,
		},
		{
			name: "mixed valid and invalid",
			scores: []Score{
				{AnswerID: answer1.ID, Value: 0.8, ScoreValidity: ScoreValidity{Valid: false}},
				{AnswerID: answer2.ID, Value: 0.6, ScoreValidity: ScoreValidity{Valid: true}},
				{AnswerID: answer3.ID, Value: 0.5, ScoreValidity: ScoreValidity{Valid: true}},
			},
			answers:    []Answer{answer1, answer2, answer3},
			expectedID: answer2.ID, // Highest valid score
		},
		{
			name:      "empty scores",
			scores:    []Score{},
			answers:   []Answer{answer1, answer2},
			expectNil: true,
		},
		{
			name: "empty answers",
			scores: []Score{
				{AnswerID: answer1.ID, Value: 0.8, ScoreValidity: ScoreValidity{Valid: true}},
			},
			answers:   []Answer{},
			expectNil: true,
		},
		{
			name: "score with no matching answer",
			scores: []Score{
				{AnswerID: "999e4567-e89b-12d3-a456-426614174000", Value: 0.9, ScoreValidity: ScoreValidity{Valid: true}},
			},
			answers:   []Answer{answer1, answer2},
			expectNil: true,
		},
		{
			name: "three-way tie with deterministic result",
			scores: []Score{
				{AnswerID: answer3.ID, Value: 0.8, ScoreValidity: ScoreValidity{Valid: true}},
				{AnswerID: answer1.ID, Value: 0.8, ScoreValidity: ScoreValidity{Valid: true}},
				{AnswerID: answer2.ID, Value: 0.8, ScoreValidity: ScoreValidity{Valid: true}},
			},
			answers:    []Answer{answer1, answer2, answer3},
			expectedID: answer1.ID, // Lowest ID wins
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			winner := DetermineWinner(tt.scores, tt.answers)
			if tt.expectNil {
				assert.Nil(t, winner)
			} else {
				require.NotNil(t, winner)
				assert.Equal(t, tt.expectedID, winner.ID)
			}
		})
	}
}
