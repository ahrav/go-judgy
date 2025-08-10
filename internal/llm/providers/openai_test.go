package providers

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/configuration"
	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

func TestNewOpenAIAdapter(t *testing.T) {
	tests := []struct {
		name             string
		config           configuration.ProviderConfig
		expectedEndpoint string
	}{
		{
			name: "default_endpoint_when_empty",
			config: configuration.ProviderConfig{
				APIKey: "test-key",
			},
			expectedEndpoint: "https://api.openai.com/v1",
		},
		{
			name: "custom_endpoint_preserved",
			config: configuration.ProviderConfig{
				APIKey:   "test-key",
				Endpoint: "https://custom.openai.com/v1",
			},
			expectedEndpoint: "https://custom.openai.com/v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := NewOpenAIAdapter(tt.config)
			assert.Equal(t, ProviderOpenAI, adapter.Name())
			assert.Equal(t, tt.expectedEndpoint, adapter.config.Endpoint)
		})
	}
}

func TestOpenAIAdapter_Build(t *testing.T) {
	adapter := NewOpenAIAdapter(configuration.ProviderConfig{
		APIKey:   "test-key",
		Endpoint: "https://api.openai.com/v1",
		Headers: map[string]string{
			"X-Custom-Header": "custom-value",
		},
	})

	tests := []struct {
		name        string
		request     *transport.Request
		wantErr     bool
		errMsg      string
		validateReq func(t *testing.T, httpReq *http.Request)
	}{
		{
			name: "generation_request_success",
			request: &transport.Request{
				Operation:      transport.OpGeneration,
				Provider:       "openai",
				Model:          "gpt-3.5-turbo",
				Question:       "What is AI?",
				SystemPrompt:   "You are a helpful assistant.",
				MaxTokens:      100,
				Temperature:    0.7,
				IdempotencyKey: "test-key-123",
				ArtifactStore:  &mockArtifactStore{},
			},
			validateReq: func(t *testing.T, httpReq *http.Request) {
				assert.Equal(t, "POST", httpReq.Method)
				assert.Equal(t, "https://api.openai.com/v1/chat/completions", httpReq.URL.String())
				assert.Equal(t, "application/json", httpReq.Header.Get("Content-Type"))
				assert.Equal(t, "Bearer test-key", httpReq.Header.Get("Authorization"))
				assert.Equal(t, "test-key-123", httpReq.Header.Get("Idempotency-Key"))
				assert.Equal(t, "custom-value", httpReq.Header.Get("X-Custom-Header"))
			},
		},
		{
			name: "scoring_request_success",
			request: &transport.Request{
				Operation:     transport.OpScoring,
				Provider:      "openai",
				Model:         "gpt-4",
				Question:      "What is AI?",
				SystemPrompt:  "You are a scoring judge.",
				MaxTokens:     200,
				Temperature:   0.1,
				ArtifactStore: &mockArtifactStore{content: "AI is artificial intelligence."},
				Answers: []domain.Answer{
					{
						ID: "answer-1",
						ContentRef: domain.ArtifactRef{
							Key:  "test-content",
							Kind: domain.ArtifactAnswer,
						},
					},
				},
			},
			validateReq: func(t *testing.T, httpReq *http.Request) {
				assert.Equal(t, "POST", httpReq.Method)
				assert.Equal(t, "https://api.openai.com/v1/chat/completions", httpReq.URL.String())
			},
		},
		{
			name: "request_with_seed",
			request: &transport.Request{
				Operation:     transport.OpGeneration,
				Provider:      "openai",
				Model:         "gpt-3.5-turbo",
				Question:      "Test question",
				MaxTokens:     100,
				Temperature:   0.0,
				Seed:          int64Ptr(12345),
				ArtifactStore: &mockArtifactStore{},
			},
			validateReq: func(t *testing.T, httpReq *http.Request) {
				// We can't easily validate the JSON body content in the request,
				// but we can ensure the request was created successfully
				assert.NotNil(t, httpReq.Body)
			},
		},
		{
			name: "unsupported_operation",
			request: &transport.Request{
				Operation: "unsupported",
				Provider:  "openai",
			},
			wantErr: true,
			errMsg:  "unsupported operation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			httpReq, err := adapter.Build(ctx, tt.request)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Nil(t, httpReq)
			} else {
				require.NoError(t, err)
				require.NotNil(t, httpReq)
				if tt.validateReq != nil {
					tt.validateReq(t, httpReq)
				}
			}
		})
	}
}

func TestOpenAIAdapter_Parse(t *testing.T) {
	adapter := NewOpenAIAdapter(configuration.ProviderConfig{})

	tests := []struct {
		name         string
		statusCode   int
		responseBody string
		headers      map[string]string
		wantErr      bool
		validate     func(t *testing.T, resp *transport.Response)
	}{
		{
			name:       "successful_response",
			statusCode: http.StatusOK,
			responseBody: `{
				"id": "chatcmpl-test123",
				"object": "chat.completion",
				"created": 1677652288,
				"model": "gpt-3.5-turbo",
				"choices": [{
					"index": 0,
					"message": {
						"role": "assistant",
						"content": "AI stands for Artificial Intelligence."
					},
					"finish_reason": "stop"
				}],
				"usage": {
					"prompt_tokens": 20,
					"completion_tokens": 10,
					"total_tokens": 30
				}
			}`,
			headers: map[string]string{
				"x-request-id": "req-123456789",
			},
			validate: func(t *testing.T, resp *transport.Response) {
				assert.Equal(t, "AI stands for Artificial Intelligence.", resp.Content)
				assert.Equal(t, domain.FinishStop, resp.FinishReason)
				assert.Equal(t, int64(20), resp.Usage.PromptTokens)
				assert.Equal(t, int64(10), resp.Usage.CompletionTokens)
				assert.Equal(t, int64(30), resp.Usage.TotalTokens)
				assert.Contains(t, resp.ProviderRequestIDs, "req-123456789")
			},
		},
		{
			name:       "length_limit_reached",
			statusCode: http.StatusOK,
			responseBody: `{
				"id": "chatcmpl-test456",
				"choices": [{
					"message": {"role": "assistant", "content": "Truncated response"},
					"finish_reason": "length"
				}],
				"usage": {"prompt_tokens": 50, "completion_tokens": 100, "total_tokens": 150}
			}`,
			validate: func(t *testing.T, resp *transport.Response) {
				assert.Equal(t, "Truncated response", resp.Content)
				assert.Equal(t, domain.FinishLength, resp.FinishReason)
			},
		},
		{
			name:       "content_filter_triggered",
			statusCode: http.StatusOK,
			responseBody: `{
				"choices": [{
					"message": {"role": "assistant", "content": ""},
					"finish_reason": "content_filter"
				}],
				"usage": {"prompt_tokens": 10, "completion_tokens": 0, "total_tokens": 10}
			}`,
			validate: func(t *testing.T, resp *transport.Response) {
				assert.Equal(t, domain.FinishContentFilter, resp.FinishReason)
			},
		},
		{
			name:       "function_call_triggered",
			statusCode: http.StatusOK,
			responseBody: `{
				"choices": [{
					"message": {"role": "assistant", "content": ""},
					"finish_reason": "function_call"
				}],
				"usage": {"prompt_tokens": 15, "completion_tokens": 5, "total_tokens": 20}
			}`,
			validate: func(t *testing.T, resp *transport.Response) {
				assert.Equal(t, domain.FinishToolUse, resp.FinishReason)
			},
		},
		{
			name:       "tool_calls_triggered",
			statusCode: http.StatusOK,
			responseBody: `{
				"choices": [{
					"message": {"role": "assistant", "content": ""},
					"finish_reason": "tool_calls"
				}],
				"usage": {"prompt_tokens": 15, "completion_tokens": 5, "total_tokens": 20}
			}`,
			validate: func(t *testing.T, resp *transport.Response) {
				assert.Equal(t, domain.FinishToolUse, resp.FinishReason)
			},
		},
		{
			name:       "empty_choices",
			statusCode: http.StatusOK,
			responseBody: `{
				"choices": [],
				"usage": {"prompt_tokens": 10, "completion_tokens": 0, "total_tokens": 10}
			}`,
			validate: func(t *testing.T, resp *transport.Response) {
				assert.Empty(t, resp.Content)
				assert.Equal(t, domain.FinishReason(""), resp.FinishReason) // Empty when no choices
			},
		},
		{
			name:         "rate_limit_error",
			statusCode:   http.StatusTooManyRequests,
			responseBody: `{"error": {"message": "Rate limit exceeded", "type": "requests", "code": "rate_limit_exceeded"}}`,
			wantErr:      true,
		},
		{
			name:         "authentication_error",
			statusCode:   http.StatusUnauthorized,
			responseBody: `{"error": {"message": "Invalid API key", "type": "invalid_request_error", "code": "invalid_api_key"}}`,
			wantErr:      true,
		},
		{
			name:         "server_error",
			statusCode:   http.StatusInternalServerError,
			responseBody: `{"error": {"message": "Internal server error", "type": "server_error"}}`,
			wantErr:      true,
		},
		{
			name:         "invalid_json_response",
			statusCode:   http.StatusOK,
			responseBody: `invalid json`,
			wantErr:      true,
		},
		{
			name:         "malformed_error_response",
			statusCode:   http.StatusBadRequest,
			responseBody: `not json error`,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock HTTP response
			httpResp := &http.Response{
				StatusCode: tt.statusCode,
				Header:     make(http.Header),
				Body:       http.NoBody,
			}

			// Set headers
			for k, v := range tt.headers {
				httpResp.Header.Set(k, v)
			}

			// Set body
			httpResp.Body = http.NoBody
			if tt.responseBody != "" {
				httpResp.Body = newStringReadCloser(tt.responseBody)
			}

			resp, err := adapter.Parse(httpResp)

			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, resp)

				// Check if error is properly typed
				if tt.statusCode != http.StatusOK {
					var providerErr *llmerrors.ProviderError
					assert.ErrorAs(t, err, &providerErr)
					assert.Equal(t, ProviderOpenAI, providerErr.Provider)
					assert.Equal(t, tt.statusCode, providerErr.StatusCode)
				}
			} else {
				require.NoError(t, err)
				require.NotNil(t, resp)
				if tt.validate != nil {
					tt.validate(t, resp)
				}
			}
		})
	}
}

func TestMapOpenAIFinishReason(t *testing.T) {
	tests := []struct {
		openaiReason string
		expected     domain.FinishReason
	}{
		{"stop", domain.FinishStop},
		{"length", domain.FinishLength},
		{"content_filter", domain.FinishContentFilter},
		{"function_call", domain.FinishToolUse},
		{"tool_calls", domain.FinishToolUse},
		{"unknown_reason", domain.FinishStop}, // Default fallback
		{"", domain.FinishStop},               // Empty string fallback
	}

	for _, tt := range tests {
		t.Run(tt.openaiReason, func(t *testing.T) {
			result := mapOpenAIFinishReason(tt.openaiReason)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseOpenAIError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		validate   func(t *testing.T, err error)
	}{
		{
			name:       "structured_error_response",
			statusCode: http.StatusBadRequest,
			body:       `{"error": {"message": "Invalid request", "type": "invalid_request_error", "code": "invalid_request"}}`,
			validate: func(t *testing.T, err error) {
				var providerErr *llmerrors.ProviderError
				require.ErrorAs(t, err, &providerErr)
				assert.Equal(t, ProviderOpenAI, providerErr.Provider)
				assert.Equal(t, http.StatusBadRequest, providerErr.StatusCode)
				assert.Equal(t, "Invalid request", providerErr.Message)
				assert.Equal(t, "invalid_request", providerErr.Code)
				assert.Equal(t, llmerrors.ErrorTypeValidation, providerErr.Type)
			},
		},
		{
			name:       "rate_limit_error_with_code",
			statusCode: http.StatusTooManyRequests,
			body:       `{"error": {"message": "Rate limit exceeded", "type": "requests", "code": "rate_limit_exceeded"}}`,
			validate: func(t *testing.T, err error) {
				var providerErr *llmerrors.ProviderError
				require.ErrorAs(t, err, &providerErr)
				assert.Equal(t, llmerrors.ErrorTypeRateLimit, providerErr.Type)
			},
		},
		{
			name:       "unstructured_error_response",
			statusCode: http.StatusInternalServerError,
			body:       "Internal server error occurred",
			validate: func(t *testing.T, err error) {
				var providerErr *llmerrors.ProviderError
				require.ErrorAs(t, err, &providerErr)
				assert.Equal(t, ProviderOpenAI, providerErr.Provider)
				assert.Equal(t, http.StatusInternalServerError, providerErr.StatusCode)
				assert.Equal(t, "Internal server error occurred", providerErr.Message)
				assert.Equal(t, llmerrors.ErrorTypeProvider, providerErr.Type)
			},
		},
		{
			name:       "malformed_json_error",
			statusCode: http.StatusBadRequest,
			body:       `{invalid json}`,
			validate: func(t *testing.T, err error) {
				var providerErr *llmerrors.ProviderError
				require.ErrorAs(t, err, &providerErr)
				assert.Equal(t, "{invalid json}", providerErr.Message)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := parseOpenAIError(tt.statusCode, []byte(tt.body))
			require.Error(t, err)
			tt.validate(t, err)
		})
	}
}

// Mock implementations for testing
type mockArtifactStore struct {
	content string
}

func (m *mockArtifactStore) Get(ctx context.Context, ref domain.ArtifactRef) (string, error) {
	if m.content != "" {
		return m.content, nil
	}
	return "mock content", nil
}

func (m *mockArtifactStore) Put(ctx context.Context, content string) (domain.ArtifactRef, error) {
	return domain.ArtifactRef{Key: "test-key", Kind: domain.ArtifactAnswer}, nil
}

// Helper functions
func int64Ptr(i int64) *int64 {
	return &i
}

func newStringReadCloser(s string) *stringReadCloser {
	return &stringReadCloser{strings.NewReader(s)}
}

type stringReadCloser struct {
	*strings.Reader
}

func (s *stringReadCloser) Close() error {
	return nil
}
