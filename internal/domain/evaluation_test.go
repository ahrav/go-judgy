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

func TestDefaultBudgetLimits(t *testing.T) {
	limits := DefaultBudgetLimits()

	assert.Equal(t, int64(10000), limits.MaxTotalTokens)
	assert.Equal(t, int64(10), limits.MaxCalls)
	assert.Equal(t, Cents(100), limits.MaxCostCents)
	assert.Equal(t, int64(300), limits.TimeoutSecs)

	// Validate the default limits
	err := limits.Validate()
	assert.NoError(t, err)
}

func TestBudgetLimits_Validate(t *testing.T) {
	tests := []struct {
		name    string
		limits  BudgetLimits
		wantErr bool
	}{
		{
			name:    "valid limits",
			limits:  DefaultBudgetLimits(),
			wantErr: false,
		},
		{
			name: "max total tokens too low",
			limits: BudgetLimits{
				MaxTotalTokens: 50,
				MaxCalls:       10,
				MaxCostCents:   Cents(100),
				TimeoutSecs:    300,
			},
			wantErr: true,
		},
		{
			name: "max calls zero",
			limits: BudgetLimits{
				MaxTotalTokens: 10000,
				MaxCalls:       0,
				MaxCostCents:   Cents(100),
				TimeoutSecs:    300,
			},
			wantErr: true,
		},
		{
			name: "max cost zero",
			limits: BudgetLimits{
				MaxTotalTokens: 10000,
				MaxCalls:       10,
				MaxCostCents:   Cents(0),
				TimeoutSecs:    300,
			},
			wantErr: true,
		},
		{
			name: "timeout too low",
			limits: BudgetLimits{
				MaxTotalTokens: 10000,
				MaxCalls:       10,
				MaxCostCents:   Cents(100),
				TimeoutSecs:    20,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.limits.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestBudgetExceededError(t *testing.T) {
	err := NewBudgetExceededError(BudgetTokens, 1000, 900, 200)

	assert.Equal(t, BudgetTokens, err.Type)
	assert.Equal(t, int64(1000), err.Limit)
	assert.Equal(t, int64(900), err.Current)
	assert.Equal(t, int64(200), err.Required)

	// Test OverBy method
	assert.Equal(t, int64(100), err.OverBy())

	errMsg := err.Error()
	assert.Contains(t, errMsg, "budget exceeded")
	assert.Contains(t, errMsg, "tokens")
	assert.Contains(t, errMsg, "1000")
	assert.Contains(t, errMsg, "900")
	assert.Contains(t, errMsg, "200")
}

// TestArtifactKind_Validation verifies that ArtifactKind enum values
// work correctly with validation and provide type safety.
func TestArtifactKind_Validation(t *testing.T) {
	tests := []struct {
		name    string
		ref     ArtifactRef
		wantErr bool
	}{
		{
			name: "valid answer artifact",
			ref: ArtifactRef{
				Key:  "test-key",
				Size: 100,
				Kind: ArtifactAnswer,
			},
			wantErr: false,
		},
		{
			name: "valid judge rationale artifact",
			ref: ArtifactRef{
				Key:  "test-key",
				Size: 100,
				Kind: ArtifactJudgeRationale,
			},
			wantErr: false,
		},
		{
			name: "valid raw prompt artifact",
			ref: ArtifactRef{
				Key:  "test-key",
				Size: 100,
				Kind: ArtifactRawPrompt,
			},
			wantErr: false,
		},
		{
			name: "invalid artifact kind",
			ref: ArtifactRef{
				Key:  "test-key",
				Size: 100,
				Kind: "invalid_kind",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.ref.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestBudgetType_String verifies that BudgetType.String() returns
// correct string representations for all enum values.
func TestBudgetType_String(t *testing.T) {
	tests := []struct {
		budgetType BudgetType
		expected   string
	}{
		{BudgetTokens, "tokens"},
		{BudgetCalls, "calls"},
		{BudgetCost, "cost"},
		{BudgetTime, "time"},
		{BudgetType(99), "unknown"}, // Invalid value
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.budgetType.String())
		})
	}
}

// TestPrincipal_Validation verifies that Principal validation
// enforces type and ID constraints correctly.
func TestPrincipal_Validation(t *testing.T) {
	tests := []struct {
		name      string
		principal Principal
		wantErr   bool
	}{
		{
			name: "valid user principal",
			principal: Principal{
				Type: PrincipalUser,
				ID:   "user@example.com",
			},
			wantErr: false,
		},
		{
			name: "valid service principal",
			principal: Principal{
				Type: PrincipalService,
				ID:   "service-account-123",
			},
			wantErr: false,
		},
		{
			name: "invalid principal type",
			principal: Principal{
				Type: "invalid",
				ID:   "user@example.com",
			},
			wantErr: true,
		},
		{
			name: "empty principal ID",
			principal: Principal{
				Type: PrincipalUser,
				ID:   "",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate.Struct(&tt.principal)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestPrincipal_String verifies that Principal.String() provides
// a readable representation in the format "type:id".
func TestPrincipal_String(t *testing.T) {
	tests := []struct {
		name      string
		principal Principal
		expected  string
	}{
		{
			name: "user principal",
			principal: Principal{
				Type: PrincipalUser,
				ID:   "user@example.com",
			},
			expected: "user:user@example.com",
		},
		{
			name: "service principal",
			principal: Principal{
				Type: PrincipalService,
				ID:   "my-service",
			},
			expected: "service:my-service",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.principal.String())
		})
	}
}

// TestNewPromptSpec_HashIntegrity verifies that NewPromptSpec computes
// deterministic hashes and validates artifact kind constraints.
func TestNewPromptSpec_HashIntegrity(t *testing.T) {
	template := "Hello {{.name}}, welcome to {{.place}}!"
	variables := map[string]string{
		"name":  "Alice",
		"place": "Wonderland",
	}
	renderedContent := "Hello Alice, welcome to Wonderland!"

	t.Run("successful creation with correct hash", func(t *testing.T) {
		ref := ArtifactRef{
			Key:  "prompts/test.txt",
			Size: int64(len(renderedContent)),
			Kind: ArtifactRawPrompt,
		}

		spec, err := NewPromptSpec(template, variables, renderedContent, ref)
		require.NoError(t, err)

		// Verify basic fields
		assert.Equal(t, template, spec.Template)
		assert.Equal(t, variables, spec.Variables)
		assert.Equal(t, ref, spec.RenderedRef)

		// Verify hash is deterministic
		spec2, err := NewPromptSpec(template, variables, renderedContent, ref)
		require.NoError(t, err)
		assert.Equal(t, spec.Hash, spec2.Hash)

		// Verify different content produces different hash
		ref3 := ArtifactRef{Key: "prompts/test2.txt", Size: 10, Kind: ArtifactRawPrompt}
		spec3, err := NewPromptSpec(template, variables, "different content", ref3)
		require.NoError(t, err)
		assert.NotEqual(t, spec.Hash, spec3.Hash)
	})

	t.Run("rejects wrong artifact kind", func(t *testing.T) {
		ref := ArtifactRef{
			Key:  "answers/wrong.txt",
			Size: int64(len(renderedContent)),
			Kind: ArtifactAnswer, // Wrong kind
		}

		_, err := NewPromptSpec(template, variables, renderedContent, ref)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "rendered_ref.kind must be")
	})

	t.Run("variables are cloned to prevent aliasing", func(t *testing.T) {
		originalVars := map[string]string{
			"name":  "Alice",
			"place": "Wonderland",
		}
		ref := ArtifactRef{
			Key:  "prompts/test.txt",
			Size: int64(len(renderedContent)),
			Kind: ArtifactRawPrompt,
		}

		spec, err := NewPromptSpec(template, originalVars, renderedContent, ref)
		require.NoError(t, err)

		// Modify original map
		originalVars["name"] = "Bob"

		// Verify spec variables are unchanged
		assert.Equal(t, "Alice", spec.Variables["name"])
	})
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
