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

// AnthropicAdapter implements ProviderAdapter for Anthropic Claude models.
// It handles Anthropic's messages API format with separate system prompts,
// request/response transformation, and Anthropic-specific headers.
type AnthropicAdapter struct {
	config configuration.ProviderConfig
}

// NewAnthropicAdapter creates an Anthropic provider adapter with default endpoint.
// If no endpoint is configured, it defaults to Anthropic's production API.
func NewAnthropicAdapter(cfg configuration.ProviderConfig) *AnthropicAdapter {
	if cfg.Endpoint == "" {
		cfg.Endpoint = "https://api.anthropic.com/v1"
	}
	return &AnthropicAdapter{config: cfg}
}

// Name returns the provider name.
func (a *AnthropicAdapter) Name() string {
	return ProviderAnthropic
}

// Build constructs an Anthropic API request from normalized LLM request.
// It builds the messages request with Anthropic's format including separate
// system prompts, proper authentication, and API versioning.
func (a *AnthropicAdapter) Build(ctx context.Context, req *transport.Request) (*http.Request, error) {
	endpoint := fmt.Sprintf("%s/messages", a.config.Endpoint)

	// Build messages array (Anthropic format).
	messages := []map[string]any{}

	var userContent string
	switch req.Operation {
	case transport.OpGeneration:
		userContent = req.Question
	case transport.OpScoring:
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

	// Add system prompt separately (Anthropic format).
	if req.SystemPrompt != "" {
		body["system"] = req.SystemPrompt
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers (Anthropic specific).
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.config.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	if req.IdempotencyKey != "" {
		httpReq.Header.Set("Idempotency-Key", req.IdempotencyKey)
	}

	for k, v := range a.config.Headers {
		httpReq.Header.Set(k, v)
	}

	return httpReq, nil
}

// Parse extracts normalized data from an Anthropic API response.
// It handles Anthropic's content format, extracts usage metrics, and maps
// stop reasons to domain types.
func (a *AnthropicAdapter) Parse(httpResp *http.Response) (*transport.Response, error) {
	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, parseAnthropicError(httpResp.StatusCode, body)
	}

	var resp struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Model        string `json:"model"`
		StopReason   string `json:"stop_reason"`
		StopSequence string `json:"stop_sequence"`
		Usage        struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	var content string
	if len(resp.Content) > 0 {
		content = resp.Content[0].Text
	}

	finishReason := mapAnthropicStopReason(resp.StopReason)

	requestIDs := []string{}
	if reqID := httpResp.Header.Get("anthropic-request-id"); reqID != "" {
		requestIDs = append(requestIDs, reqID)
	}

	return &transport.Response{
		Content:            content,
		FinishReason:       finishReason,
		ProviderRequestIDs: requestIDs,
		Usage: transport.NormalizedUsage{
			PromptTokens:     int64(resp.Usage.InputTokens),
			CompletionTokens: int64(resp.Usage.OutputTokens),
			TotalTokens:      int64(resp.Usage.InputTokens + resp.Usage.OutputTokens),
		},
		Headers: httpResp.Header,
		RawBody: body,
	}, nil
}

// mapAnthropicStopReason converts Anthropic stop_reason to domain FinishReason.
// Maps Anthropic-specific completion reasons to normalized domain types.
func mapAnthropicStopReason(reason string) domain.FinishReason {
	switch reason {
	case "end_turn", "stop_sequence":
		return domain.FinishStop
	case "max_tokens":
		return domain.FinishLength
	case "content_filter":
		return domain.FinishContentFilter
	case "tool_use":
		return domain.FinishToolUse
	default:
		return domain.FinishStop
	}
}

// parseAnthropicError converts Anthropic error responses to ProviderError.
// Extracts error details from Anthropic's JSON error format.
func parseAnthropicError(statusCode int, body []byte) error {
	var errResp struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Message != "" {
		return &llmerrors.ProviderError{
			Provider:   ProviderAnthropic,
			StatusCode: statusCode,
			Message:    errResp.Message,
			Code:       errResp.Type,
			Type:       classifyErrorType(statusCode, errResp.Type),
		}
	}

	return &llmerrors.ProviderError{
		Provider:   ProviderAnthropic,
		StatusCode: statusCode,
		Message:    string(body),
		Type:       classifyErrorType(statusCode, ""),
	}
}
