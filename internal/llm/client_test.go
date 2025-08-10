package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/configuration"
)

func TestClient_NewClient(t *testing.T) {
	tests := []struct {
		name    string
		config  *configuration.Config
		wantErr bool
		errMsg  string
	}{
		{
			name:   "success_nil_config_uses_defaults",
			config: nil,
		},
		{
			name: "success_minimal_config",
			config: &configuration.Config{
				Providers: map[string]configuration.ProviderConfig{
					"openai": {
						APIKey:   "test-key",
						Endpoint: "https://api.openai.com",
					},
				},
				Retry: configuration.RetryConfig{
					MaxAttempts:     3,
					InitialInterval: 250 * time.Millisecond,
					MaxElapsedTime:  45 * time.Second,
					MaxInterval:     5 * time.Second,
					Multiplier:      2.0,
				},
			},
		},
		{
			name: "success_with_custom_http_client",
			config: &configuration.Config{
				HTTPClient: &http.Client{Timeout: 30 * time.Second},
				Providers: map[string]configuration.ProviderConfig{
					"openai": {
						APIKey:   "test-key",
						Endpoint: "https://api.openai.com",
					},
				},
				Retry: configuration.RetryConfig{
					MaxAttempts:     3,
					InitialInterval: 250 * time.Millisecond,
					MaxElapsedTime:  45 * time.Second,
					MaxInterval:     5 * time.Second,
					Multiplier:      2.0,
				},
			},
		},
		{
			name: "success_with_custom_artifact_store",
			config: &configuration.Config{
				ArtifactStore: &mockConfigArtifactStore{},
				Providers: map[string]configuration.ProviderConfig{
					"openai": {
						APIKey:   "test-key",
						Endpoint: "https://api.openai.com",
					},
				},
				Retry: configuration.RetryConfig{
					MaxAttempts:     3,
					InitialInterval: 250 * time.Millisecond,
					MaxElapsedTime:  45 * time.Second,
					MaxInterval:     5 * time.Second,
					Multiplier:      2.0,
				},
			},
		},
		{
			name: "failure_invalid_provider_config",
			config: &configuration.Config{
				Providers: map[string]configuration.ProviderConfig{
					"invalid": {}, // Missing required fields
				},
			},
			wantErr: true,
			errMsg:  "failed to initialize router",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(tt.config)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Nil(t, client)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, client)

				// Validate that client implements the interface correctly
				// by calling a basic operation
				ctx := context.Background()
				input := domain.GenerateAnswersInput{
					Question:   "test",
					NumAnswers: 1,
					Config: domain.EvalConfig{
						Provider:        "test",
						Model:           "test",
						MaxAnswerTokens: 100,
						Temperature:     0.5,
						Timeout:         30,
						MaxAnswers:      1,
						ScoreThreshold:  0.7,
					},
				}
				// This should not panic, even if it returns errors
				output, _ := client.Generate(ctx, input)
				assert.NotNil(t, output) // Should at least return an output structure
			}
		})
	}
}

func TestClient_Generate(t *testing.T) {
	// Mock HTTP server for provider responses
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate OpenAI response format
		response := `{
			"id": "chatcmpl-test",
			"object": "chat.completion",
			"created": 1234567890,
			"model": "gpt-3.5-turbo",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": "This is a test answer."
				},
				"finish_reason": "stop"
			}],
			"usage": {
				"prompt_tokens": 10,
				"completion_tokens": 5,
				"total_tokens": 15
			}
		}`
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(response))
	}))
	defer server.Close()

	config := &configuration.Config{
		Providers: map[string]configuration.ProviderConfig{
			"openai": {
				APIKey:   "test-key",
				Endpoint: server.URL,
			},
		},
		// Disable middleware that might interfere with testing
		Cache:     configuration.CacheConfig{Enabled: false},
		RateLimit: configuration.RateLimitConfig{Local: configuration.LocalRateLimitConfig{Enabled: false}},
		Pricing:   configuration.PricingConfig{Enabled: false},
		Retry: configuration.RetryConfig{
			MaxAttempts:     3,
			InitialInterval: 250 * time.Millisecond,
			MaxElapsedTime:  45 * time.Second,
			MaxInterval:     5 * time.Second,
			Multiplier:      2.0,
		},
		CircuitBreaker: configuration.CircuitBreakerConfig{
			FailureThreshold: 10,
			SuccessThreshold: 3,
			OpenTimeout:      time.Second,
		},
		Observability: configuration.ObservabilityConfig{MetricsEnabled: false},
	}

	client, err := NewClient(config)
	require.NoError(t, err)

	tests := []struct {
		name      string
		input     domain.GenerateAnswersInput
		wantError bool
		validate  func(t *testing.T, output *domain.GenerateAnswersOutput)
	}{
		{
			name: "success_single_answer",
			input: domain.GenerateAnswersInput{
				Question:   "What is the capital of France?",
				NumAnswers: 1,
				Config: domain.EvalConfig{
					Provider:        "openai",
					Model:           "gpt-3.5-turbo",
					MaxAnswerTokens: 100,
					Temperature:     0.7,
					Timeout:         30,
					MaxAnswers:      1,
					ScoreThreshold:  0.7,
				},
			},
			validate: func(t *testing.T, output *domain.GenerateAnswersOutput) {
				assert.Len(t, output.Answers, 1)
				assert.Greater(t, output.TokensUsed, int64(0))
				assert.Equal(t, int64(1), output.CallsMade)
				assert.Empty(t, output.Error)
				// Answer has ContentRef instead of direct Content field
				assert.NotEmpty(t, output.Answers[0].ContentRef.Key)
			},
		},
		{
			name: "success_multiple_answers",
			input: domain.GenerateAnswersInput{
				Question:   "What is AI?",
				NumAnswers: 3,
				Config: domain.EvalConfig{
					Provider:        "openai",
					Model:           "gpt-3.5-turbo",
					MaxAnswerTokens: 100,
					Temperature:     0.7,
					Timeout:         30,
					MaxAnswers:      3,
					ScoreThreshold:  0.7,
				},
			},
			validate: func(t *testing.T, output *domain.GenerateAnswersOutput) {
				assert.Len(t, output.Answers, 3)
				assert.Greater(t, output.TokensUsed, int64(0))
				assert.Equal(t, int64(3), output.CallsMade)
				assert.Empty(t, output.Error)
			},
		},
		{
			name: "failure_invalid_provider",
			input: domain.GenerateAnswersInput{
				Question:   "Test question",
				NumAnswers: 1,
				Config: domain.EvalConfig{
					Provider:        "nonexistent",
					Model:           "test-model",
					Timeout:         30,
					MaxAnswers:      1,
					MaxAnswerTokens: 100,
					Temperature:     0.7,
					ScoreThreshold:  0.7,
				},
			},
			validate: func(t *testing.T, output *domain.GenerateAnswersOutput) {
				assert.Len(t, output.Answers, 0)
				assert.NotEmpty(t, output.Error)
				assert.Contains(t, output.Error, "failed to generate answer")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			output, err := client.Generate(ctx, tt.input)

			require.NoError(t, err) // Generate should not return errors, errors go in output.Error
			require.NotNil(t, output)

			tt.validate(t, output)
		})
	}
}

func TestClient_Score(t *testing.T) {
	// Mock HTTP server for scoring responses
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate OpenAI response with scoring content
		response := `{
			"id": "chatcmpl-score",
			"object": "chat.completion", 
			"created": 1234567890,
			"model": "gpt-3.5-turbo",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": "{\"value\": 0.85, \"confidence\": 0.9, \"reasoning\": \"Good answer\", \"dimensions\": [{\"name\": \"accuracy\", \"value\": 0.9}, {\"name\": \"clarity\", \"value\": 0.8}]}"
				},
				"finish_reason": "stop"
			}],
			"usage": {
				"prompt_tokens": 20,
				"completion_tokens": 10,
				"total_tokens": 30
			}
		}`
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(response))
	}))
	defer server.Close()

	config := &configuration.Config{
		Providers: map[string]configuration.ProviderConfig{
			"openai": {
				APIKey:   "test-key",
				Endpoint: server.URL,
			},
		},
		// Disable middleware
		Cache:     configuration.CacheConfig{Enabled: false},
		RateLimit: configuration.RateLimitConfig{Local: configuration.LocalRateLimitConfig{Enabled: false}},
		Pricing:   configuration.PricingConfig{Enabled: false},
		Retry: configuration.RetryConfig{
			MaxAttempts:     3,
			InitialInterval: 250 * time.Millisecond,
			MaxElapsedTime:  45 * time.Second,
			MaxInterval:     5 * time.Second,
			Multiplier:      2.0,
		},
		CircuitBreaker: configuration.CircuitBreakerConfig{
			FailureThreshold: 10,
			SuccessThreshold: 3,
			OpenTimeout:      time.Second,
		},
		Observability: configuration.ObservabilityConfig{MetricsEnabled: false},
		Features: configuration.FeatureFlags{
			DisableJSONRepair: false,
		},
	}

	client, err := NewClient(config)
	require.NoError(t, err)

	tests := []struct {
		name     string
		input    domain.ScoreAnswersInput
		validate func(t *testing.T, output *domain.ScoreAnswersOutput)
	}{
		{
			name: "success_score_answers",
			input: domain.ScoreAnswersInput{
				Question: "What is the capital of France?",
				Answers: []domain.Answer{
					{
						ID: "answer-1",
						ContentRef: domain.ArtifactRef{
							Key:  "test-content-1",
							Kind: domain.ArtifactAnswer,
						},
						AnswerProvenance: domain.AnswerProvenance{
							Provider: "test-provider",
							Model:    "test-model",
						},
					},
					{
						ID: "answer-2",
						ContentRef: domain.ArtifactRef{
							Key:  "test-content-2",
							Kind: domain.ArtifactAnswer,
						},
						AnswerProvenance: domain.AnswerProvenance{
							Provider: "test-provider",
							Model:    "test-model",
						},
					},
				},
				Config: domain.EvalConfig{
					Provider:        "openai",
					Model:           "gpt-3.5-turbo",
					Timeout:         30,
					MaxAnswers:      2,
					MaxAnswerTokens: 100,
					Temperature:     0.1,
					ScoreThreshold:  0.7,
				},
			},
			validate: func(t *testing.T, output *domain.ScoreAnswersOutput) {
				assert.Len(t, output.Scores, 2)
				assert.Greater(t, output.TokensUsed, int64(0))
				assert.Equal(t, int64(2), output.CallsMade)

				for _, score := range output.Scores {
					assert.Greater(t, score.Value, float64(0))
					assert.Greater(t, score.Confidence, float64(0))
					assert.NotEmpty(t, score.ReasonRef.Key) // ReasonRef instead of Reasoning
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			output, err := client.Score(ctx, tt.input)

			require.NoError(t, err) // Score should not return errors, errors go in individual scores
			require.NotNil(t, output)

			tt.validate(t, output)
		})
	}
}

// Test adapter functions
func TestArtifactStoreAdapter(t *testing.T) {
	mockStore := &mockBusinessArtifactStore{}
	adapter := newArtifactStoreAdapter(mockStore)

	ctx := context.Background()

	t.Run("Get", func(t *testing.T) {
		ref := domain.ArtifactRef{Key: "test-key"}
		mockStore.getContent = "test content"

		content, err := adapter.Get(ctx, ref)
		require.NoError(t, err)
		assert.Equal(t, "test content", content)
		assert.Equal(t, ref, mockStore.getRef)
	})

	t.Run("Put", func(t *testing.T) {
		content := "test content"
		expectedRef := domain.ArtifactRef{Key: "generated-key"}
		mockStore.putRef = expectedRef

		ref, err := adapter.Put(ctx, content)
		require.NoError(t, err)
		assert.Equal(t, expectedRef, ref)
		assert.Equal(t, content, mockStore.putContent)
		assert.Equal(t, domain.ArtifactAnswer, mockStore.putKind)
		assert.Contains(t, mockStore.putKey, "artifacts/")
	})
}

func TestConfigArtifactStoreAdapter(t *testing.T) {
	mockStore := &mockConfigArtifactStore{}
	adapter := newConfigArtifactStoreAdapter(mockStore)

	ctx := context.Background()

	t.Run("Get", func(t *testing.T) {
		ref := domain.ArtifactRef{Key: "test-key"}
		mockStore.getContent = "test content"

		content, err := adapter.Get(ctx, ref)
		require.NoError(t, err)
		assert.Equal(t, "test content", content)
		assert.Equal(t, ref, mockStore.getRef)
	})

	t.Run("Put", func(t *testing.T) {
		content := "test content"
		kind := domain.ArtifactRawPrompt
		key := "test-key"
		baseRef := domain.ArtifactRef{Key: "base-key"}
		mockStore.putRef = baseRef

		ref, err := adapter.Put(ctx, content, kind, key)
		require.NoError(t, err)
		assert.Equal(t, "test-key", ref.Key)
		assert.Equal(t, kind, ref.Kind)
		assert.Equal(t, content, mockStore.putContent)
	})

	t.Run("Exists_with_interface", func(t *testing.T) {
		mockStoreWithExists := &mockConfigArtifactStoreWithExists{}
		adapter := newConfigArtifactStoreAdapter(mockStoreWithExists)

		ref := domain.ArtifactRef{Key: "test-key"}
		mockStoreWithExists.existsResult = true

		exists, err := adapter.Exists(ctx, ref)
		require.NoError(t, err)
		assert.True(t, exists)
		assert.Equal(t, ref, mockStoreWithExists.existsRef)
	})

	t.Run("Exists_without_interface", func(t *testing.T) {
		ref := domain.ArtifactRef{Key: "test-key"}
		mockStore.getContent = "exists"

		exists, err := adapter.Exists(ctx, ref)
		require.NoError(t, err)
		assert.True(t, exists)
		assert.Equal(t, ref, mockStore.getRef)
	})
}

func TestBusinessScoreValidator(t *testing.T) {
	validator := newBusinessScoreValidator()

	tests := []struct {
		name         string
		content      string
		enableRepair bool
		wantErr      bool
	}{
		{
			name:    "valid_json",
			content: `{"value": 0.85, "confidence": 0.9, "reasoning": "Good answer"}`,
		},
		{
			name:         "invalid_json_with_repair",
			content:      `{"value": 0.85, "confidence": 0.9, "reasoning": "Good answer"`, // Missing closing brace
			enableRepair: true,
			wantErr:      false, // Repair should succeed
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scoreData, err := validator.ValidateAndRepairScore(tt.content, tt.enableRepair)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, scoreData)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, scoreData)
				assert.Greater(t, scoreData.Value, 0.0)
				assert.Greater(t, scoreData.Confidence, 0.0)
				assert.NotEmpty(t, scoreData.Reasoning)
			}
		})
	}
}

// Mock implementations
type mockConfigArtifactStore struct {
	getRef     domain.ArtifactRef
	getContent string
	getErr     error

	putContent string
	putRef     domain.ArtifactRef
	putErr     error
}

func (m *mockConfigArtifactStore) Get(ctx context.Context, ref domain.ArtifactRef) (string, error) {
	m.getRef = ref
	return m.getContent, m.getErr
}

func (m *mockConfigArtifactStore) Put(ctx context.Context, content string) (domain.ArtifactRef, error) {
	m.putContent = content
	return m.putRef, m.putErr
}

type mockConfigArtifactStoreWithExists struct {
	mockConfigArtifactStore
	existsRef    domain.ArtifactRef
	existsResult bool
	existsErr    error
}

func (m *mockConfigArtifactStoreWithExists) Exists(ctx context.Context, ref domain.ArtifactRef) (bool, error) {
	m.existsRef = ref
	return m.existsResult, m.existsErr
}

type mockBusinessArtifactStore struct {
	getRef     domain.ArtifactRef
	getContent string
	getErr     error

	putContent string
	putKind    domain.ArtifactKind
	putKey     string
	putRef     domain.ArtifactRef
	putErr     error

	existsRef    domain.ArtifactRef
	existsResult bool
	existsErr    error

	deleteRef domain.ArtifactRef
	deleteErr error
}

func (m *mockBusinessArtifactStore) Get(ctx context.Context, ref domain.ArtifactRef) (string, error) {
	m.getRef = ref
	return m.getContent, m.getErr
}

func (m *mockBusinessArtifactStore) Put(ctx context.Context, content string, kind domain.ArtifactKind, key string) (domain.ArtifactRef, error) {
	m.putContent = content
	m.putKind = kind
	m.putKey = key
	return m.putRef, m.putErr
}

func (m *mockBusinessArtifactStore) Exists(ctx context.Context, ref domain.ArtifactRef) (bool, error) {
	m.existsRef = ref
	return m.existsResult, m.existsErr
}

func (m *mockBusinessArtifactStore) Delete(ctx context.Context, ref domain.ArtifactRef) error {
	m.deleteRef = ref
	return m.deleteErr
}
