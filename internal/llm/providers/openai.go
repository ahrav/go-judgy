package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/configuration"
	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// OpenAIAdapter implements ProviderAdapter for OpenAI GPT models.
// It handles OpenAI's chat/completions API format including system prompts,
// request/response transformation, and OpenAI-specific error handling.
type OpenAIAdapter struct {
	config configuration.ProviderConfig
}

// NewOpenAIAdapter creates an OpenAI provider adapter with default endpoint.
// If no endpoint is configured, it defaults to OpenAI's production API.
func NewOpenAIAdapter(cfg configuration.ProviderConfig) *OpenAIAdapter {
	if cfg.Endpoint == "" {
		cfg.Endpoint = "https://api.openai.com/v1"
	}
	return &OpenAIAdapter{config: cfg}
}

// Name returns the provider name.
func (a *OpenAIAdapter) Name() string {
	return ProviderOpenAI
}

// Build constructs an OpenAI API request from normalized LLM request.
// It builds the chat/completions request with proper message formatting,
// parameter handling, and authentication headers.
func (a *OpenAIAdapter) Build(ctx context.Context, req *transport.Request) (*http.Request, error) {
	// Choose endpoint based on operation type.
	var endpoint string
	switch req.Operation {
	case transport.OpGeneration, transport.OpScoring:
		endpoint = fmt.Sprintf("%s/chat/completions", a.config.Endpoint)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedOperation, req.Operation)
	}

	messages := []map[string]any{}

	if req.SystemPrompt != "" {
		messages = append(messages, map[string]any{
			"role":    "system",
			"content": req.SystemPrompt,
		})
	}

	var userContent string
	switch req.Operation {
	case transport.OpGeneration:
		userContent = req.Question
	case transport.OpScoring:
		// Format question and answers for scoring.
		userContent = fmt.Sprintf("Question: %s\n\nAnswer to evaluate: %s",
			req.Question, transport.ExtractAnswerContent(ctx, req.Answers, req.ArtifactStore))
	}

	messages = append(messages, map[string]any{
		"role":    "user",
		"content": userContent,
	})

	body := map[string]any{
		"model":       req.Model,
		"messages":    messages,
		"max_tokens":  req.MaxTokens,
		"temperature": req.Temperature,
	}

	if req.Seed != nil {
		body["seed"] = *req.Seed
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", a.config.APIKey))

	if req.IdempotencyKey != "" {
		httpReq.Header.Set("Idempotency-Key", req.IdempotencyKey)
	}

	for k, v := range a.config.Headers {
		httpReq.Header.Set(k, v)
	}

	return httpReq, nil
}

// Parse extracts normalized data from an OpenAI API response.
// It handles OpenAI's JSON format, extracts usage metrics, and maps
// finish reasons to domain types.
func (a *OpenAIAdapter) Parse(httpResp *http.Response) (*transport.Response, error) {
	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, parseOpenAIError(httpResp.StatusCode, body)
	}

	var resp struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		Model   string `json:"model"`
		Choices []struct {
			Index   int `json:"index"`
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	var content string
	var finishReason domain.FinishReason

	if len(resp.Choices) > 0 {
		content = resp.Choices[0].Message.Content
		finishReason = mapOpenAIFinishReason(resp.Choices[0].FinishReason)
	}

	requestIDs := []string{}
	if reqID := httpResp.Header.Get("x-request-id"); reqID != "" {
		requestIDs = append(requestIDs, reqID)
	}

	return &transport.Response{
		Content:            content,
		FinishReason:       finishReason,
		ProviderRequestIDs: requestIDs,
		Usage: transport.NormalizedUsage{
			PromptTokens:     int64(resp.Usage.PromptTokens),
			CompletionTokens: int64(resp.Usage.CompletionTokens),
			TotalTokens:      int64(resp.Usage.TotalTokens),
		},
		Headers: httpResp.Header,
		RawBody: body,
	}, nil
}

// mapOpenAIFinishReason converts OpenAI finish_reason to domain FinishReason.
// Maps OpenAI-specific completion reasons to normalized domain types.
func mapOpenAIFinishReason(reason string) domain.FinishReason {
	switch reason {
	case "stop":
		return domain.FinishStop
	case "length":
		return domain.FinishLength
	case "content_filter":
		return domain.FinishContentFilter
	case "tool_calls", "function_call":
		return domain.FinishToolUse
	default:
		return domain.FinishStop
	}
}

// parseOpenAIError converts OpenAI error responses to ProviderError.
// Extracts error details from OpenAI's JSON error format.
func parseOpenAIError(statusCode int, body []byte) error {
	var errResp struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}

	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Error.Message != "" {
		return &llmerrors.ProviderError{
			Provider:   ProviderOpenAI,
			StatusCode: statusCode,
			Message:    errResp.Error.Message,
			Code:       errResp.Error.Code,
			Type:       classifyErrorType(statusCode, errResp.Error.Type),
		}
	}

	return &llmerrors.ProviderError{
		Provider:   ProviderOpenAI,
		StatusCode: statusCode,
		Message:    string(body),
		Type:       classifyErrorType(statusCode, ""),
	}
}
