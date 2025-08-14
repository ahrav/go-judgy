package domain //nolint:testpackage // Need access to unexported validate

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMakeEvaluationRequest(t *testing.T) {
	testID := "123e4567-e89b-12d3-a456-426614174000"
	testTime := time.Now()
	testPrincipal := Principal{Type: PrincipalUser, ID: "user@example.com"}

	tests := []struct {
		name        string
		id          string
		requestedAt time.Time
		question    string
		requestedBy Principal
		config      EvalConfig
		limits      BudgetLimits
		wantErr     bool
		errContains string
	}{
		{
			name:        "valid request",
			id:          testID,
			requestedAt: testTime,
			question:    "What is the capital of France?",
			requestedBy: testPrincipal,
			config:      DefaultEvalConfig(),
			limits:      DefaultBudgetLimits(),
			wantErr:     false,
		},
		{
			name:        "invalid UUID",
			id:          "not-a-uuid",
			requestedAt: testTime,
			question:    "What is the capital of France?",
			requestedBy: testPrincipal,
			config:      DefaultEvalConfig(),
			limits:      DefaultBudgetLimits(),
			wantErr:     true,
			errContains: "uuid",
		},
		{
			name:        "zero time",
			id:          testID,
			requestedAt: time.Time{},
			question:    "What is the capital of France?",
			requestedBy: testPrincipal,
			config:      DefaultEvalConfig(),
			limits:      DefaultBudgetLimits(),
			wantErr:     true,
		},
		{
			name:        "question too short",
			id:          testID,
			requestedAt: testTime,
			question:    "Hi",
			requestedBy: testPrincipal,
			config:      DefaultEvalConfig(),
			limits:      DefaultBudgetLimits(),
			wantErr:     true,
			errContains: "min",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := MakeEvaluationRequest(tt.id, tt.requestedAt, tt.question, tt.requestedBy, tt.config, tt.limits)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				assert.Nil(t, req)
			} else {
				assert.NoError(t, err)
				require.NotNil(t, req)
				assert.Equal(t, tt.id, req.ID)
				assert.Equal(t, tt.requestedAt, req.RequestedAt)
				assert.Equal(t, tt.question, req.Question)
				assert.Equal(t, tt.requestedBy, req.RequestedBy)
				assert.Equal(t, tt.config, req.Config)
				assert.Equal(t, tt.limits, req.BudgetLimits)
				assert.NotNil(t, req.Metadata)
			}
		})
	}
}

func TestNewEvaluationRequest(t *testing.T) {
	testPrincipal := Principal{Type: PrincipalUser, ID: "user@example.com"}

	tests := []struct {
		name        string
		question    string
		requestedBy Principal
		config      EvalConfig
		limits      BudgetLimits
		wantErr     bool
		errContains string
	}{
		{
			name:        "valid request",
			question:    "What is the capital of France?",
			requestedBy: testPrincipal,
			config:      DefaultEvalConfig(),
			limits:      DefaultBudgetLimits(),
			wantErr:     false,
		},
		{
			name:        "question too short",
			question:    "Hi",
			requestedBy: testPrincipal,
			config:      DefaultEvalConfig(),
			limits:      DefaultBudgetLimits(),
			wantErr:     true,
			errContains: "min",
		},
		{
			name:        "invalid principal type",
			question:    "What is the capital of France?",
			requestedBy: Principal{Type: "invalid", ID: "user@example.com"},
			config:      DefaultEvalConfig(),
			limits:      DefaultBudgetLimits(),
			wantErr:     true,
			errContains: "oneof",
		},
		{
			name:        "invalid config",
			question:    "What is the capital of France?",
			requestedBy: testPrincipal,
			config: EvalConfig{
				MaxAnswers:      0, // Invalid: less than minimum
				MaxAnswerTokens: 1000,
				Temperature:     0.7,
				ScoreThreshold:  0.7,
				Provider:        "openai",
				Timeout:         60,
			},
			limits:  DefaultBudgetLimits(),
			wantErr: true,
		},
		{
			name:        "invalid budget limits",
			question:    "What is the capital of France?",
			requestedBy: testPrincipal,
			config:      DefaultEvalConfig(),
			limits: BudgetLimits{
				MaxTotalTokens: 50, // Invalid: less than minimum
				MaxCalls:       10,
				MaxCostCents:   Cents(100),
				TimeoutSecs:    300,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := NewEvaluationRequest(tt.question, tt.requestedBy, tt.config, tt.limits)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				assert.Nil(t, req)
			} else {
				assert.NoError(t, err)
				require.NotNil(t, req)
				assert.NotEmpty(t, req.ID)
				assert.Equal(t, tt.question, req.Question)
				assert.Equal(t, tt.requestedBy, req.RequestedBy)
				assert.Equal(t, tt.config, req.Config)
				assert.Equal(t, tt.limits, req.BudgetLimits)
				assert.NotNil(t, req.Metadata)
				assert.False(t, req.RequestedAt.IsZero())
			}
		})
	}
}

func TestEvaluationRequest_Validate(t *testing.T) {
	validReq := &EvaluationRequest{
		ID:           "123e4567-e89b-12d3-a456-426614174000",
		Question:     "What is the capital of France?",
		Config:       DefaultEvalConfig(),
		BudgetLimits: DefaultBudgetLimits(),
		RequestedBy:  Principal{Type: PrincipalUser, ID: "user@example.com"},
		RequestedAt:  time.Now(),
		Metadata:     make(map[string]string),
	}

	tests := []struct {
		name    string
		modify  func(*EvaluationRequest)
		wantErr bool
	}{
		{
			name:    "valid request",
			modify:  func(_ *EvaluationRequest) {},
			wantErr: false,
		},
		{
			name: "invalid UUID",
			modify: func(r *EvaluationRequest) {
				r.ID = "not-a-uuid"
			},
			wantErr: true,
		},
		{
			name: "empty question",
			modify: func(r *EvaluationRequest) {
				r.Question = ""
			},
			wantErr: true,
		},
		{
			name: "question too long",
			modify: func(r *EvaluationRequest) {
				r.Question = string(make([]byte, 1001))
			},
			wantErr: true,
		},
		{
			name: "invalid principal type",
			modify: func(r *EvaluationRequest) {
				r.RequestedBy = Principal{Type: "invalid", ID: "user@example.com"}
			},
			wantErr: true,
		},
		{
			name: "zero time",
			modify: func(r *EvaluationRequest) {
				r.RequestedAt = time.Time{}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := *validReq // Create a copy
			tt.modify(&req)

			err := req.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestDefaultEvalConfig(t *testing.T) {
	config := DefaultEvalConfig()

	assert.Equal(t, int64(3), config.MaxAnswers)
	assert.Equal(t, int64(1000), config.MaxAnswerTokens)
	assert.Equal(t, 0.7, config.Temperature)
	assert.Equal(t, 0.7, config.ScoreThreshold)
	assert.Equal(t, "openai", config.Provider)
	assert.Equal(t, int64(60), config.Timeout)

	// Validate the default config
	err := config.Validate()
	assert.NoError(t, err)
}

func TestEvalConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  EvalConfig
		wantErr bool
	}{
		{
			name:    "valid config",
			config:  DefaultEvalConfig(),
			wantErr: false,
		},
		{
			name: "max answers too low",
			config: EvalConfig{
				MaxAnswers:      0,
				MaxAnswerTokens: 1000,
				Temperature:     0.7,
				ScoreThreshold:  0.7,
				Provider:        "openai",
				Timeout:         60,
			},
			wantErr: true,
		},
		{
			name: "max answers too high",
			config: EvalConfig{
				MaxAnswers:      11,
				MaxAnswerTokens: 1000,
				Temperature:     0.7,
				ScoreThreshold:  0.7,
				Provider:        "openai",
				Timeout:         60,
			},
			wantErr: true,
		},
		{
			name: "max answer tokens too low",
			config: EvalConfig{
				MaxAnswers:      3,
				MaxAnswerTokens: 40,
				Temperature:     0.7,
				ScoreThreshold:  0.7,
				Provider:        "openai",
				Timeout:         60,
			},
			wantErr: true,
		},
		{
			name: "max answer tokens too high",
			config: EvalConfig{
				MaxAnswers:      3,
				MaxAnswerTokens: 4001,
				Temperature:     0.7,
				ScoreThreshold:  0.7,
				Provider:        "openai",
				Timeout:         60,
			},
			wantErr: true,
		},
		{
			name: "temperature too low",
			config: EvalConfig{
				MaxAnswers:      3,
				MaxAnswerTokens: 1000,
				Temperature:     -0.1,
				ScoreThreshold:  0.7,
				Provider:        "openai",
				Timeout:         60,
			},
			wantErr: true,
		},
		{
			name: "temperature too high",
			config: EvalConfig{
				MaxAnswers:      3,
				MaxAnswerTokens: 1000,
				Temperature:     2.1,
				ScoreThreshold:  0.7,
				Provider:        "openai",
				Timeout:         60,
			},
			wantErr: true,
		},
		{
			name: "timeout too low",
			config: EvalConfig{
				MaxAnswers:      3,
				MaxAnswerTokens: 1000,
				Temperature:     0.7,
				ScoreThreshold:  0.7,
				Provider:        "openai",
				Timeout:         5,
			},
			wantErr: true,
		},
		{
			name: "timeout too high",
			config: EvalConfig{
				MaxAnswers:      3,
				MaxAnswerTokens: 1000,
				Temperature:     0.7,
				ScoreThreshold:  0.7,
				Provider:        "openai",
				Timeout:         301,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestEvalConfig_IsValidProvider(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		want     bool
	}{
		{"openai", "openai", true},
		{"anthropic", "anthropic", true},
		{"google", "google", true},
		{"custom", "custom-provider", true},
		{"empty", "", false},
		{"aws", "aws", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := EvalConfig{Provider: tt.provider}
			assert.Equal(t, tt.want, config.IsValidProvider())
		})
	}
}

// TestEvaluationRequest_MetadataImmutability verifies that WithMeta and WithMetadata
// methods provide immutable behavior and don't affect the original request.
func TestEvaluationRequest_MetadataImmutability(t *testing.T) {
	original := &EvaluationRequest{
		ID:           "123e4567-e89b-12d3-a456-426614174000",
		Question:     "What is the capital of France?",
		Config:       DefaultEvalConfig(),
		BudgetLimits: DefaultBudgetLimits(),
		RequestedBy:  Principal{Type: PrincipalUser, ID: "user@example.com"},
		RequestedAt:  time.Now(),
		Metadata:     map[string]string{"existing": "value"},
	}

	t.Run("WithMeta creates new instance", func(t *testing.T) {
		updated := original.WithMeta("new_key", "new_value")

		// Verify original is unchanged
		assert.Equal(t, 1, len(original.Metadata))
		assert.Equal(t, "value", original.Metadata["existing"])
		assert.NotContains(t, original.Metadata, "new_key")

		// Verify updated copy has both old and new
		assert.Equal(t, 2, len(updated.Metadata))
		assert.Equal(t, "value", updated.Metadata["existing"])
		assert.Equal(t, "new_value", updated.Metadata["new_key"])

		// Verify different instances
		assert.NotSame(t, original, updated)
	})

	t.Run("WithMetadata merges correctly", func(t *testing.T) {
		newMeta := map[string]string{
			"key1": "value1",
			"key2": "value2",
		}
		updated := original.WithMetadata(newMeta)

		// Verify original is unchanged
		assert.Equal(t, 1, len(original.Metadata))
		assert.NotContains(t, original.Metadata, "key1")

		// Verify updated copy has merged metadata
		assert.Equal(t, 3, len(updated.Metadata))
		assert.Equal(t, "value", updated.Metadata["existing"])
		assert.Equal(t, "value1", updated.Metadata["key1"])
		assert.Equal(t, "value2", updated.Metadata["key2"])
	})

	t.Run("WithMeta overwrites existing keys", func(t *testing.T) {
		updated := original.WithMeta("existing", "new_value")

		// Verify original is unchanged
		assert.Equal(t, "value", original.Metadata["existing"])

		// Verify updated copy has overwritten value
		assert.Equal(t, "new_value", updated.Metadata["existing"])
	})

	t.Run("handles nil metadata", func(t *testing.T) {
		reqWithNilMeta := &EvaluationRequest{
			ID:           "223e4567-e89b-12d3-a456-426614174000",
			Question:     "Test question?",
			Config:       DefaultEvalConfig(),
			BudgetLimits: DefaultBudgetLimits(),
			RequestedBy:  Principal{Type: PrincipalUser, ID: "user@example.com"},
			RequestedAt:  time.Now(),
			Metadata:     nil,
		}

		updated := reqWithNilMeta.WithMeta("key", "value")
		assert.NotNil(t, updated.Metadata)
		assert.Equal(t, "value", updated.Metadata["key"])
	})
}
