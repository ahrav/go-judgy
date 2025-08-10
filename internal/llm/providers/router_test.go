package providers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ahrav/go-judgy/internal/llm/configuration"
	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

func TestNewRouter(t *testing.T) {
	tests := []struct {
		name     string
		configs  map[string]configuration.ProviderConfig
		wantErr  bool
		errMsg   string
		validate func(t *testing.T, router Router)
	}{
		{
			name: "success_single_provider",
			configs: map[string]configuration.ProviderConfig{
				ProviderOpenAI: {
					APIKey:   "test-key",
					Endpoint: "https://api.openai.com/v1",
				},
			},
			validate: func(t *testing.T, router Router) {
				adapter, err := router.Pick(ProviderOpenAI, "gpt-3.5-turbo")
				require.NoError(t, err)
				assert.Equal(t, ProviderOpenAI, adapter.Name())
			},
		},
		{
			name: "success_multiple_providers",
			configs: map[string]configuration.ProviderConfig{
				ProviderOpenAI: {
					APIKey:   "openai-key",
					Endpoint: "https://api.openai.com/v1",
				},
				ProviderAnthropic: {
					APIKey:   "anthropic-key",
					Endpoint: "https://api.anthropic.com",
				},
				ProviderGoogle: {
					APIKey:   "google-key",
					Endpoint: "https://generativelanguage.googleapis.com/v1",
				},
			},
			validate: func(t *testing.T, router Router) {
				// Test OpenAI
				openaiAdapter, err := router.Pick(ProviderOpenAI, "gpt-4")
				require.NoError(t, err)
				assert.Equal(t, ProviderOpenAI, openaiAdapter.Name())

				// Test Anthropic
				anthropicAdapter, err := router.Pick(ProviderAnthropic, "claude-3-sonnet")
				require.NoError(t, err)
				assert.Equal(t, ProviderAnthropic, anthropicAdapter.Name())

				// Test Google
				googleAdapter, err := router.Pick(ProviderGoogle, "gemini-pro")
				require.NoError(t, err)
				assert.Equal(t, ProviderGoogle, googleAdapter.Name())
			},
		},
		{
			name: "failure_unknown_provider",
			configs: map[string]configuration.ProviderConfig{
				"unknown-provider": {
					APIKey: "test-key",
				},
			},
			wantErr: true,
			errMsg:  "unknown provider: unknown-provider",
		},
		{
			name:    "success_empty_config",
			configs: map[string]configuration.ProviderConfig{},
			validate: func(t *testing.T, router Router) {
				// Should create empty router successfully
				assert.NotNil(t, router)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router, err := NewRouter(tt.configs)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Nil(t, router)

				// Check that error is the expected type
				assert.ErrorIs(t, err, llmerrors.ErrUnknownProvider)
			} else {
				require.NoError(t, err)
				require.NotNil(t, router)
				if tt.validate != nil {
					tt.validate(t, router)
				}
			}
		})
	}
}

func TestRouter_Pick(t *testing.T) {
	// Create router with multiple providers
	router, err := NewRouter(map[string]configuration.ProviderConfig{
		ProviderOpenAI: {
			APIKey:   "openai-key",
			Endpoint: "https://api.openai.com/v1",
		},
		ProviderAnthropic: {
			APIKey:   "anthropic-key",
			Endpoint: "https://api.anthropic.com",
		},
	})
	require.NoError(t, err)

	tests := []struct {
		name     string
		provider string
		model    string
		wantErr  bool
		errMsg   string
		validate func(t *testing.T, adapter ProviderAdapter)
	}{
		{
			name:     "success_openai_provider",
			provider: ProviderOpenAI,
			model:    "gpt-3.5-turbo",
			validate: func(t *testing.T, adapter ProviderAdapter) {
				assert.Equal(t, ProviderOpenAI, adapter.Name())
			},
		},
		{
			name:     "success_anthropic_provider",
			provider: ProviderAnthropic,
			model:    "claude-3-sonnet",
			validate: func(t *testing.T, adapter ProviderAdapter) {
				assert.Equal(t, ProviderAnthropic, adapter.Name())
			},
		},
		{
			name:     "success_model_ignored", // Current implementation ignores model parameter
			provider: ProviderOpenAI,
			model:    "any-model",
			validate: func(t *testing.T, adapter ProviderAdapter) {
				assert.Equal(t, ProviderOpenAI, adapter.Name())
			},
		},
		{
			name:     "failure_unknown_provider",
			provider: "unknown",
			model:    "some-model",
			wantErr:  true,
			errMsg:   "unknown provider: unknown",
		},
		{
			name:     "failure_empty_provider",
			provider: "",
			model:    "some-model",
			wantErr:  true,
			errMsg:   "unknown provider:",
		},
		{
			name:     "failure_unconfigured_provider",
			provider: ProviderGoogle, // Not configured in this router
			model:    "gemini-pro",
			wantErr:  true,
			errMsg:   "unknown provider: google",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter, err := router.Pick(tt.provider, tt.model)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Nil(t, adapter)

				// Check that error is the expected type
				assert.ErrorIs(t, err, llmerrors.ErrUnknownProvider)
			} else {
				require.NoError(t, err)
				require.NotNil(t, adapter)
				if tt.validate != nil {
					tt.validate(t, adapter)
				}
			}
		})
	}
}

func TestRouter_ProviderIntegration(t *testing.T) {
	// Integration test that ensures router works with actual provider adapters
	router, err := NewRouter(map[string]configuration.ProviderConfig{
		ProviderOpenAI: {
			APIKey:   "test-openai-key",
			Endpoint: "https://api.openai.com/v1",
		},
		ProviderAnthropic: {
			APIKey:   "test-anthropic-key",
			Endpoint: "https://api.anthropic.com",
		},
		ProviderGoogle: {
			APIKey:   "test-google-key",
			Endpoint: "https://generativelanguage.googleapis.com/v1",
		},
	})
	require.NoError(t, err)

	ctx := context.Background()
	req := &transport.Request{
		Operation:     transport.OpGeneration,
		Provider:      ProviderOpenAI,
		Model:         "gpt-3.5-turbo",
		Question:      "Test question",
		MaxTokens:     100,
		Temperature:   0.7,
		ArtifactStore: &mockArtifactStore{},
	}

	// Test that we can pick a provider and build a request
	adapter, err := router.Pick(ProviderOpenAI, "gpt-3.5-turbo")
	require.NoError(t, err)

	httpReq, err := adapter.Build(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, httpReq)

	// Validate the request is properly formed
	assert.Equal(t, "POST", httpReq.Method)
	assert.Contains(t, httpReq.URL.String(), "chat/completions")
	assert.Equal(t, "application/json", httpReq.Header.Get("Content-Type"))
	assert.Equal(t, "Bearer test-openai-key", httpReq.Header.Get("Authorization"))
}

func TestProviderConstants(t *testing.T) {
	// Test that provider constants match expected values
	assert.Equal(t, "openai", ProviderOpenAI)
	assert.Equal(t, "anthropic", ProviderAnthropic)
	assert.Equal(t, "google", ProviderGoogle)
}

func TestRouterInterface(t *testing.T) {
	// Test that router implements the Router interface correctly
	var r Router = &router{
		adapters: make(map[string]ProviderAdapter),
	}

	// Should be able to call Pick method
	_, err := r.Pick("nonexistent", "model")
	require.Error(t, err)
	assert.ErrorIs(t, err, llmerrors.ErrUnknownProvider)
}

// Test error handling when provider adapter creation might fail (edge case)
func TestNewRouter_EdgeCases(t *testing.T) {
	t.Run("nil_configs", func(t *testing.T) {
		router, err := NewRouter(nil)
		require.NoError(t, err)
		assert.NotNil(t, router)

		// Should fail to find any provider
		_, err = router.Pick(ProviderOpenAI, "gpt-3.5-turbo")
		require.Error(t, err)
		assert.ErrorIs(t, err, llmerrors.ErrUnknownProvider)
	})

	t.Run("empty_provider_name", func(t *testing.T) {
		configs := map[string]configuration.ProviderConfig{
			"": {
				APIKey: "test-key",
			},
		}

		// Empty provider name should be treated as unknown
		_, err := NewRouter(configs)
		require.Error(t, err)
		assert.ErrorIs(t, err, llmerrors.ErrUnknownProvider)
	})
}

func TestRouter_Concurrency(t *testing.T) {
	// Test that router is safe for concurrent use
	router, err := NewRouter(map[string]configuration.ProviderConfig{
		ProviderOpenAI: {
			APIKey:   "test-key",
			Endpoint: "https://api.openai.com/v1",
		},
	})
	require.NoError(t, err)

	// Run multiple goroutines that use the router concurrently
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			adapter, err := router.Pick(ProviderOpenAI, "gpt-3.5-turbo")
			assert.NoError(t, err)
			assert.NotNil(t, adapter)
			assert.Equal(t, ProviderOpenAI, adapter.Name())
			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}
}
