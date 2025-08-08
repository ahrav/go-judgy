package domain //nolint:testpackage // Need access to unexported validate

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIsValidVerdictStatus verifies that IsValidVerdictStatus correctly identifies
// valid verdict status constants and rejects invalid status values.
func TestIsValidVerdictStatus(t *testing.T) {
	tests := []struct {
		name   string
		status VerdictStatus
		want   bool
	}{
		{"success", VerdictStatusSuccess, true},
		{"partial", VerdictStatusPartial, true},
		{"failed", VerdictStatusFailed, true},
		{"timeout", VerdictStatusTimeout, true},
		{"invalid", VerdictStatus("invalid"), false},
		{"empty", VerdictStatus(""), false},
		{"pending", VerdictStatus("pending"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsValidVerdictStatus(tt.status))
		})
	}
}

// TestVerdict_Validate verifies that Verdict.Validate() enforces struct validation
// tags, required fields, and business rules including the empty scores constraint.
func TestVerdict_Validate(t *testing.T) {
	validScore := Score{
		ID:         "123e4567-e89b-12d3-a456-426614174001",
		AnswerID:   "123e4567-e89b-12d3-a456-426614174000",
		Value:      0.8,
		Confidence: 0.9,
		ScoreEvidence: ScoreEvidence{
			ReasonRef: ArtifactRef{
				Key:  "scores/reasoning-good.txt",
				Size: 4,
				Kind: "judge_rationale",
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

	validVerdict := &Verdict{
		ID:                   "123e4567-e89b-12d3-a456-426614174000",
		EvaluationID:         "223e4567-e89b-12d3-a456-426614174000",
		EvaluationInstanceID: "323e4567-e89b-12d3-a456-426614174000",
		VerdictOutcome: VerdictOutcome{
			WinnerAnswerID: "",
			AggregateScore: 0.75,
			Status:         VerdictStatusSuccess,
			ErrorCode:      ErrCodeNone,
			Error:          "",
		},
		VerdictUsageTotals: VerdictUsageTotals{
			StartedAt:      time.Now().Add(-5 * time.Minute),
			CompletedAt:    time.Now(),
			DurationMillis: 300000,
			TotalTokens:    1000,
			TotalCalls:     5,
			TotalCostCents: Cents(50),
		},
		VerdictReview: VerdictReview{
			RequiresHumanReview: false,
			ReviewReasonCode:    "",
			ReviewReason:        "",
		},
		WinnerAnswer: nil,
		AllScores:    []Score{validScore},
	}

	tests := []struct {
		name    string
		modify  func(*Verdict)
		wantErr bool
	}{
		{
			name:    "valid verdict",
			modify:  func(_ *Verdict) {},
			wantErr: false,
		},
		{
			name: "empty ID",
			modify: func(v *Verdict) {
				v.ID = ""
			},
			wantErr: true,
		},
		{
			name: "empty evaluation ID",
			modify: func(v *Verdict) {
				v.EvaluationID = ""
			},
			wantErr: true,
		},
		{
			name: "empty evaluation instance ID",
			modify: func(v *Verdict) {
				v.EvaluationInstanceID = ""
			},
			wantErr: true,
		},
		{
			name: "negative aggregate score",
			modify: func(v *Verdict) {
				v.AggregateScore = -0.1
			},
			wantErr: true,
		},
		{
			name: "aggregate score too high",
			modify: func(v *Verdict) {
				v.AggregateScore = 1.1
			},
			wantErr: true,
		},
		{
			name: "empty scores",
			modify: func(v *Verdict) {
				v.AllScores = []Score{}
			},
			wantErr: true,
		},
		{
			name: "zero started time",
			modify: func(v *Verdict) {
				v.StartedAt = time.Time{}
			},
			wantErr: true,
		},
		{
			name: "zero completed time",
			modify: func(v *Verdict) {
				v.CompletedAt = time.Time{}
			},
			wantErr: true,
		},
		{
			name: "negative duration",
			modify: func(v *Verdict) {
				v.DurationMillis = -1
			},
			wantErr: true,
		},
		{
			name: "empty status",
			modify: func(v *Verdict) {
				v.Status = ""
			},
			wantErr: true,
		},
		{
			name: "negative total tokens",
			modify: func(v *Verdict) {
				v.TotalTokens = -1
			},
			wantErr: true,
		},
		{
			name: "negative total calls",
			modify: func(v *Verdict) {
				v.TotalCalls = -1
			},
			wantErr: true,
		},
		{
			name: "negative total cost",
			modify: func(v *Verdict) {
				v.TotalCostCents = Cents(-1)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verdict := *validVerdict
			tt.modify(&verdict)

			err := verdict.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestVerdict_CalculateDuration verifies that duration calculation correctly
// computes milliseconds between StartedAt and CompletedAt timestamps.
func TestVerdict_CalculateDuration(t *testing.T) {
	verdict := Verdict{}
	verdict.StartedAt = time.Now().Add(-5 * time.Minute)
	verdict.CompletedAt = time.Now()
	verdict.DurationMillis = 0

	verdict.CalculateDuration()

	// Should be approximately 300000ms (5 minutes).
	assert.Greater(t, verdict.DurationMillis, int64(299000))
	assert.Less(t, verdict.DurationMillis, int64(301000))

	verdict2 := Verdict{}
	verdict2.StartedAt = time.Date(2023, 1, 1, 10, 0, 0, 0, time.UTC)
	verdict2.CompletedAt = time.Date(2023, 1, 1, 10, 0, 10, 0, time.UTC)
	verdict2.DurationMillis = 0

	verdict2.CalculateDuration()
	assert.Equal(t, int64(10000), verdict2.DurationMillis) // 10 seconds
}

// TestVerdict_DetermineStatus verifies that status determination follows
// the correct priority order based on errors, scores, and winner presence.
func TestVerdict_DetermineStatus(t *testing.T) {
	validScore := Score{
		ID:    "123e4567-e89b-12d3-a456-426614174001",
		Value: 0.8,
		ScoreValidity: ScoreValidity{
			Valid: true,
		},
	}

	validAnswer := Answer{
		ID: "answer-1",
		ContentRef: ArtifactRef{
			Key:  "answers/valid.txt",
			Size: 12,
			Kind: "answer",
		},
	}

	tests := []struct {
		name           string
		verdict        Verdict
		expectedStatus VerdictStatus
	}{
		{
			name: "success with winner",
			verdict: Verdict{
				VerdictOutcome: VerdictOutcome{
					ErrorCode: ErrCodeNone,
					Error:     "",
				},
				WinnerAnswer: &validAnswer,
				AllScores:    []Score{validScore},
			},
			expectedStatus: VerdictStatusSuccess,
		},
		{
			name: "success with winner answer ID",
			verdict: Verdict{
				VerdictOutcome: VerdictOutcome{
					WinnerAnswerID: "123e4567-e89b-12d3-a456-426614174000",
					ErrorCode:      ErrCodeNone,
					Error:          "",
				},
				WinnerAnswer: nil,
				AllScores:    []Score{validScore},
			},
			expectedStatus: VerdictStatusSuccess,
		},
		{
			name: "partial without winner",
			verdict: Verdict{
				VerdictOutcome: VerdictOutcome{
					WinnerAnswerID: "",
					ErrorCode:      ErrCodeNone,
					Error:          "",
				},
				WinnerAnswer: nil,
				AllScores:    []Score{validScore},
			},
			expectedStatus: VerdictStatusPartial,
		},
		{
			name: "failed with error",
			verdict: Verdict{
				VerdictOutcome: VerdictOutcome{
					ErrorCode: ErrCodeProvider,
					Error:     "Processing failed",
				},
				WinnerAnswer: &validAnswer,
				AllScores:    []Score{validScore},
			},
			expectedStatus: VerdictStatusFailed,
		},
		{
			name: "timeout error with error code",
			verdict: Verdict{
				VerdictOutcome: VerdictOutcome{
					ErrorCode: ErrCodeTimeout,
					Error:     "operation timed out",
				},
				WinnerAnswer: &validAnswer,
				AllScores:    []Score{validScore},
			},
			expectedStatus: VerdictStatusTimeout,
		},
		{
			name: "failed with no scores",
			verdict: Verdict{
				VerdictOutcome: VerdictOutcome{
					ErrorCode: ErrCodeNone,
					Error:     "",
				},
				WinnerAnswer: &validAnswer,
				AllScores:    []Score{},
			},
			expectedStatus: VerdictStatusFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.verdict.DetermineStatus()
			assert.Equal(t, tt.expectedStatus, tt.verdict.Status)
		})
	}
}

// TestVerdict_NeedsReview verifies that review determination correctly evaluates
// score thresholds and status conditions while setting appropriate flags.
func TestVerdict_NeedsReview(t *testing.T) {
	validAnswer := Answer{
		ID: "answer-1",
		ContentRef: ArtifactRef{
			Key:  "answers/valid-answer.txt",
			Size: 12,
			Kind: "answer",
		},
	}

	tests := []struct {
		name             string
		verdict          Verdict
		threshold        float64
		expectReview     bool
		expectReasonCode ReviewReasonCode
		expectReason     string
	}{
		{
			name: "score above threshold success",
			verdict: Verdict{
				VerdictOutcome: VerdictOutcome{
					AggregateScore: 0.8,
					Status:         VerdictStatusSuccess,
				},
				WinnerAnswer: &validAnswer,
			},
			threshold:        0.7,
			expectReview:     false,
			expectReasonCode: "",
			expectReason:     "",
		},
		{
			name: "score below threshold",
			verdict: Verdict{
				VerdictOutcome: VerdictOutcome{
					AggregateScore: 0.6,
					Status:         VerdictStatusSuccess,
				},
				WinnerAnswer: &validAnswer,
			},
			threshold:        0.7,
			expectReview:     true,
			expectReasonCode: ReviewLowAggregate,
			expectReason:     "Score below threshold",
		},
		{
			name: "failed status",
			verdict: Verdict{
				VerdictOutcome: VerdictOutcome{
					AggregateScore: 0.9,
					Status:         VerdictStatusFailed,
				},
				WinnerAnswer: &validAnswer,
			},
			threshold:        0.7,
			expectReview:     true,
			expectReasonCode: ReviewUnsuccessful,
			expectReason:     "Evaluation did not complete successfully",
		},
		{
			name: "partial status",
			verdict: Verdict{
				VerdictOutcome: VerdictOutcome{
					AggregateScore: 0.9,
					Status:         VerdictStatusPartial,
				},
				WinnerAnswer: nil,
			},
			threshold:        0.7,
			expectReview:     true,
			expectReasonCode: ReviewUnsuccessful,
			expectReason:     "Evaluation did not complete successfully",
		},
		{
			name: "timeout status",
			verdict: Verdict{
				VerdictOutcome: VerdictOutcome{
					AggregateScore: 0.9,
					Status:         VerdictStatusTimeout,
				},
				WinnerAnswer: &validAnswer,
			},
			threshold:        0.7,
			expectReview:     true,
			expectReasonCode: ReviewUnsuccessful,
			expectReason:     "Evaluation did not complete successfully",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.verdict.NeedsReview(tt.threshold)
			assert.Equal(t, tt.expectReview, result)
			assert.Equal(t, tt.expectReview, tt.verdict.RequiresHumanReview)
			if tt.expectReview {
				assert.Equal(t, tt.expectReasonCode, tt.verdict.ReviewReasonCode)
				assert.Equal(t, tt.expectReason, tt.verdict.ReviewReason)
			} else {
				assert.Equal(t, ReviewReasonCode(""), tt.verdict.ReviewReasonCode)
				assert.Equal(t, "", tt.verdict.ReviewReason)
			}
		})
	}
}

// TestVerdict_WithWinnerAnswer verifies that verdicts correctly handle
// winner answers and maintain referential integrity during validation.
func TestVerdict_WithWinnerAnswer(t *testing.T) {
	winnerAnswer := Answer{
		ID: "923e4567-e89b-12d3-a456-426614174000",
		ContentRef: ArtifactRef{
			Key:  "answers/paris-answer.txt",
			Size: 32,
			Kind: "answer",
		},
		AnswerProvenance: AnswerProvenance{
			Provider:    "openai",
			Model:       "gpt-4",
			GeneratedAt: time.Now(),
		},
		AnswerUsage: AnswerUsage{
			TotalTokens: 150,
		},
		AnswerCost: AnswerCost{
			EstimatedCost: Cents(15),
		},
	}

	verdict := Verdict{
		ID:                   "923e4567-e89b-12d3-a456-426614174000",
		EvaluationID:         "123e4567-e89b-12d3-a456-426614174000",
		EvaluationInstanceID: "223e4567-e89b-12d3-a456-426614174000",
		VerdictOutcome: VerdictOutcome{
			AggregateScore: 0.85,
			Status:         VerdictStatusSuccess,
		},
		VerdictUsageTotals: VerdictUsageTotals{
			StartedAt:      time.Now().Add(-1 * time.Minute),
			CompletedAt:    time.Now(),
			DurationMillis: 60000,
			TotalTokens:    500,
			TotalCalls:     5,
			TotalCostCents: Cents(50),
		},
		WinnerAnswer: &winnerAnswer,
		AllScores: []Score{
			{
				ID:       "123e4567-e89b-12d3-a456-426614174011",
				AnswerID: "123e4567-e89b-12d3-a456-426614174001",
				Value:    0.9,
				ScoreEvidence: ScoreEvidence{
					ReasonRef: ArtifactRef{
						Key:  "scores/reasoning-1.txt",
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
				ScoreValidity: ScoreValidity{Valid: true},
			},
			{
				ID:       "123e4567-e89b-12d3-a456-426614174012",
				AnswerID: "123e4567-e89b-12d3-a456-426614174002",
				Value:    0.8,
				ScoreEvidence: ScoreEvidence{
					ReasonRef: ArtifactRef{
						Key:  "scores/reasoning-2.txt",
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
				ScoreValidity: ScoreValidity{Valid: true},
			},
		},
	}

	require.NotNil(t, verdict.WinnerAnswer)
	assert.Equal(t, "923e4567-e89b-12d3-a456-426614174000", verdict.WinnerAnswer.ID)
	assert.Equal(t, "answers/paris-answer.txt", verdict.WinnerAnswer.ContentRef.Key)

	// Validate the verdict.
	err := verdict.Validate()
	assert.NoError(t, err)
}

// TestVerdict_ErrorHandling verifies that error scenarios are properly
// tracked and reflected in status determination logic.
func TestVerdict_ErrorHandling(t *testing.T) {
	verdict := Verdict{
		ID:                   "923e4567-e89b-12d3-a456-426614174000",
		EvaluationID:         "123e4567-e89b-12d3-a456-426614174000",
		EvaluationInstanceID: "223e4567-e89b-12d3-a456-426614174000",
		VerdictOutcome: VerdictOutcome{
			AggregateScore: 0.0,
			Status:         VerdictStatusFailed,
			ErrorCode:      ErrCodeProvider,
			Error:          "Failed to generate answers: rate limit exceeded",
		},
		VerdictUsageTotals: VerdictUsageTotals{
			StartedAt:      time.Now().Add(-1 * time.Minute),
			CompletedAt:    time.Now(),
			DurationMillis: 60000,
			TotalTokens:    0,
			TotalCalls:     0,
			TotalCostCents: Cents(0),
		},
		AllScores: []Score{},
	}

	assert.NotEmpty(t, verdict.Error)
	assert.Equal(t, VerdictStatusFailed, verdict.Status)

	// Determine status should keep it as failed.
	verdict.DetermineStatus()
	assert.Equal(t, VerdictStatusFailed, verdict.Status)
}

// TestVerdict_ReviewFlags verifies that RequiresHumanReview and ReviewReason
// fields correctly capture manual review requirements.
func TestVerdict_ReviewFlags(t *testing.T) {
	verdict := Verdict{
		ID:                   "923e4567-e89b-12d3-a456-426614174000",
		EvaluationID:         "123e4567-e89b-12d3-a456-426614174000",
		EvaluationInstanceID: "223e4567-e89b-12d3-a456-426614174000",
		VerdictOutcome: VerdictOutcome{
			AggregateScore: 0.4,
			Status:         VerdictStatusPartial,
		},
		VerdictReview: VerdictReview{
			RequiresHumanReview: true,
			ReviewReasonCode:    ReviewLowAggregate,
			ReviewReason:        "Low confidence scores across all answers",
		},
	}

	assert.True(t, verdict.RequiresHumanReview)
	assert.Equal(t, "Low confidence scores across all answers", verdict.ReviewReason)
}

// TestVerdictStatus_Constants verifies that VerdictStatus enum constants
// have the expected string values for serialization consistency.
func TestVerdictStatus_Constants(t *testing.T) {
	// Ensure the constants have the expected values.
	assert.Equal(t, VerdictStatus("success"), VerdictStatusSuccess)
	assert.Equal(t, VerdictStatus("partial"), VerdictStatusPartial)
	assert.Equal(t, VerdictStatus("failed"), VerdictStatusFailed)
	assert.Equal(t, VerdictStatus("timeout"), VerdictStatusTimeout)
}

// TestVerdict_ApplyTotals verifies that the ApplyTotals method correctly
// applies measured resource usage to the verdict.
func TestVerdict_ApplyTotals(t *testing.T) {
	verdict := Verdict{}

	totals := UsageTotals{
		Tokens: 1500,
		Calls:  10,
		Cost:   Cents(75),
	}

	verdict.ApplyTotals(totals)

	assert.Equal(t, int64(1500), verdict.TotalTokens)
	assert.Equal(t, int64(10), verdict.TotalCalls)
	assert.Equal(t, Cents(75), verdict.TotalCostCents)
}

// TestVerdict_TimeValidation verifies that validation fails when
// CompletedAt is before StartedAt.
func TestVerdict_TimeValidation(t *testing.T) {
	validScore := Score{
		ID:         "623e4567-e89b-12d3-a456-426614174003",
		AnswerID:   "123e4567-e89b-12d3-a456-426614174000",
		Value:      0.8,
		Confidence: 0.9,
		ScoreEvidence: ScoreEvidence{
			ReasonRef: ArtifactRef{
				Key:  "scores/reasoning-good.txt",
				Size: 4,
				Kind: "judge_rationale",
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

	verdict := Verdict{
		ID:                   "923e4567-e89b-12d3-a456-426614174000",
		EvaluationID:         "123e4567-e89b-12d3-a456-426614174000",
		EvaluationInstanceID: "223e4567-e89b-12d3-a456-426614174000",
		VerdictOutcome: VerdictOutcome{
			AggregateScore: 0.75,
			Status:         VerdictStatusSuccess,
		},
		VerdictUsageTotals: VerdictUsageTotals{
			StartedAt:      time.Now(),
			CompletedAt:    time.Now().Add(-1 * time.Hour), // Before StartedAt
			DurationMillis: 0,
			TotalTokens:    1000,
			TotalCalls:     5,
			TotalCostCents: Cents(50),
		},
		AllScores: []Score{validScore},
	}

	err := verdict.Validate()
	assert.Error(t, err)
	// The time validation check ensures CompletedAt >= StartedAt
	// The exact error type may be either ErrInvalidVerdict or a validation error
}

// TestVerdictErrorCode_Constants verifies that VerdictErrorCode constants
// have the expected values.
func TestVerdictErrorCode_Constants(t *testing.T) {
	assert.Equal(t, VerdictErrorCode(""), ErrCodeNone)
	assert.Equal(t, VerdictErrorCode("timeout"), ErrCodeTimeout)
	assert.Equal(t, VerdictErrorCode("budget_exceeded"), ErrCodeBudget)
	assert.Equal(t, VerdictErrorCode("provider_error"), ErrCodeProvider)
	assert.Equal(t, VerdictErrorCode("validation_error"), ErrCodeValidation)
}

// TestReviewReasonCode_Constants verifies that ReviewReasonCode constants
// have the expected values.
func TestReviewReasonCode_Constants(t *testing.T) {
	assert.Equal(t, ReviewReasonCode("low_aggregate_score"), ReviewLowAggregate)
	assert.Equal(t, ReviewReasonCode("unsuccessful_status"), ReviewUnsuccessful)
}

// TestVerdict_UUIDValidation verifies that verdict IDs must be valid UUIDs.
func TestVerdict_UUIDValidation(t *testing.T) {
	validScore := Score{
		ID:       "723e4567-e89b-12d3-a456-426614174004",
		AnswerID: "123e4567-e89b-12d3-a456-426614174000",
		Value:    0.8,
		ScoreEvidence: ScoreEvidence{
			ReasonRef: ArtifactRef{
				Key:  "scores/reasoning.txt",
				Size: 4,
				Kind: "judge_rationale",
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
		id      string
		evalID  string
		instID  string
		wantErr bool
	}{
		{
			name:    "valid UUIDs",
			id:      "123e4567-e89b-12d3-a456-426614174000",
			evalID:  "223e4567-e89b-12d3-a456-426614174000",
			instID:  "323e4567-e89b-12d3-a456-426614174000",
			wantErr: false,
		},
		{
			name:    "invalid verdict ID",
			id:      "not-a-uuid",
			evalID:  "223e4567-e89b-12d3-a456-426614174000",
			instID:  "323e4567-e89b-12d3-a456-426614174000",
			wantErr: true,
		},
		{
			name:    "invalid evaluation ID",
			id:      "123e4567-e89b-12d3-a456-426614174000",
			evalID:  "not-a-uuid",
			instID:  "323e4567-e89b-12d3-a456-426614174000",
			wantErr: true,
		},
		{
			name:    "invalid instance ID",
			id:      "123e4567-e89b-12d3-a456-426614174000",
			evalID:  "223e4567-e89b-12d3-a456-426614174000",
			instID:  "not-a-uuid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verdict := Verdict{
				ID:                   tt.id,
				EvaluationID:         tt.evalID,
				EvaluationInstanceID: tt.instID,
				VerdictOutcome: VerdictOutcome{
					AggregateScore: 0.75,
					Status:         VerdictStatusSuccess,
				},
				VerdictUsageTotals: VerdictUsageTotals{
					StartedAt:      time.Now().Add(-1 * time.Minute),
					CompletedAt:    time.Now(),
					DurationMillis: 60000,
					TotalTokens:    1000,
					TotalCalls:     5,
					TotalCostCents: Cents(50),
				},
				AllScores: []Score{validScore},
			}

			err := verdict.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
