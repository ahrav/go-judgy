package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ahrav/go-judgy/internal/domain"
)

// Router selects the appropriate provider adapter for request routing.
// Provides centralized adapter selection based on provider and model configuration,
// enabling dynamic provider switching and adapter lifecycle management.
type Router interface {
	// Pick selects the adapter for the specified provider and model combination.
	// Returns error if provider is unknown, unsupported, or model is unavailable.
	// for the requested provider.
	Pick(provider, model string) (ProviderAdapter, error)
}

// ProviderAdapter abstracts provider-specific HTTP communication patterns.
// Each provider (OpenAI, Anthropic, Google) implements this interface to handle
// their unique API formats, authentication schemes, and response structures.
type ProviderAdapter interface {
	// Build constructs provider-specific HTTP request from normalized LLM request.
	// Sets authentication headers, API endpoints, request bodies, and timeouts.
	// according to provider requirements and model specifications.
	Build(ctx context.Context, req *LLMRequest) (*http.Request, error)

	// Parse extracts normalized data from provider's HTTP response.
	// Handles provider-specific response formats, header extraction, usage metrics,.
	// and error conditions to produce consistent LLMResponse structure.
	Parse(httpResp *http.Response) (*Request, error)

	// Name returns canonical provider identifier for routing and metrics.
	// Valid values: "openai", "anthropic", "google" matching configuration keys.
	Name() string
}

// Supported LLM provider identifiers.
// These constants must match the provider names used in configuration.
const (
	ProviderOpenAI    = "openai"    // OpenAI GPT models
	ProviderAnthropic = "anthropic" // Anthropic Claude models
	ProviderGoogle    = "google"    // Google Gemini models
)

// Provider adapter errors.
var (
	ErrUnsupportedOperation = errors.New("unsupported operation")
)

// NewRouter creates a router with configured provider adapters.
func NewRouter(configs map[string]ProviderConfig) (Router, error) {
	adapters := make(map[string]ProviderAdapter)

	for name, cfg := range configs {
		var adapter ProviderAdapter
		switch name {
		case ProviderOpenAI:
			adapter = NewOpenAIAdapter(cfg)
		case ProviderAnthropic:
			adapter = NewAnthropicAdapter(cfg)
		case ProviderGoogle:
			adapter = NewGoogleAdapter(cfg)
		default:
			return nil, fmt.Errorf("%w: %s", ErrUnknownProvider, name)
		}
		adapters[name] = adapter
	}

	return &router{adapters: adapters}, nil
}

// router implements Router interface with provider adapter registry.
// It maintains a map of configured provider adapters for request routing.
type router struct {
	adapters map[string]ProviderAdapter
}

// Pick selects the appropriate provider adapter for the given provider name.
// Returns an error if the provider is not configured or unknown.
func (r *router) Pick(provider, _ string) (ProviderAdapter, error) {
	adapter, ok := r.adapters[provider]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownProvider, provider)
	}
	return adapter, nil
}

// OpenAIAdapter implements ProviderAdapter for OpenAI GPT models.
// It handles OpenAI's chat/completions API format including system prompts,
// request/response transformation, and OpenAI-specific error handling.
type OpenAIAdapter struct {
	config ProviderConfig
}

// NewOpenAIAdapter creates an OpenAI provider adapter with default endpoint.
// If no endpoint is configured, it defaults to OpenAI's production API.
func NewOpenAIAdapter(cfg ProviderConfig) *OpenAIAdapter {
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
func (a *OpenAIAdapter) Build(ctx context.Context, req *LLMRequest) (*http.Request, error) {
	// Choose endpoint based on operation type.
	var endpoint string
	switch req.Operation {
	case OpGeneration, OpScoring:
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
	case OpGeneration:
		userContent = req.Question
	case OpScoring:
		// Format question and answers for scoring.
		userContent = fmt.Sprintf("Question: %s\n\nAnswer to evaluate: %s",
			req.Question, extractAnswerContent(ctx, req.Answers, req.ArtifactStore))
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
func (a *OpenAIAdapter) Parse(httpResp *http.Response) (*Request, error) {
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

	return &Request{
		Content:            content,
		FinishReason:       finishReason,
		ProviderRequestIDs: requestIDs,
		Usage: NormalizedUsage{
			PromptTokens:     int64(resp.Usage.PromptTokens),
			CompletionTokens: int64(resp.Usage.CompletionTokens),
			TotalTokens:      int64(resp.Usage.TotalTokens),
		},
		Headers: httpResp.Header,
		RawBody: body,
	}, nil
}

// AnthropicAdapter implements ProviderAdapter for Anthropic Claude models.
// It handles Anthropic's messages API format with separate system prompts,
// request/response transformation, and Anthropic-specific headers.
type AnthropicAdapter struct {
	config ProviderConfig
}

// NewAnthropicAdapter creates an Anthropic provider adapter with default endpoint.
// If no endpoint is configured, it defaults to Anthropic's production API.
func NewAnthropicAdapter(cfg ProviderConfig) *AnthropicAdapter {
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
func (a *AnthropicAdapter) Build(ctx context.Context, req *LLMRequest) (*http.Request, error) {
	endpoint := fmt.Sprintf("%s/messages", a.config.Endpoint)

	// Build messages array (Anthropic format).
	messages := []map[string]any{}

	var userContent string
	switch req.Operation {
	case OpGeneration:
		userContent = req.Question
	case OpScoring:
		userContent = fmt.Sprintf("Question: %s\n\nAnswer to evaluate: %s",
			req.Question, extractAnswerContent(ctx, req.Answers, req.ArtifactStore))
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
func (a *AnthropicAdapter) Parse(httpResp *http.Response) (*Request, error) {
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

	return &Request{
		Content:            content,
		FinishReason:       finishReason,
		ProviderRequestIDs: requestIDs,
		Usage: NormalizedUsage{
			PromptTokens:     int64(resp.Usage.InputTokens),
			CompletionTokens: int64(resp.Usage.OutputTokens),
			TotalTokens:      int64(resp.Usage.InputTokens + resp.Usage.OutputTokens),
		},
		Headers: httpResp.Header,
		RawBody: body,
	}, nil
}

// GoogleAdapter implements ProviderAdapter for Google Gemini models.
// It handles Google's generateContent API format with API key authentication,
// system instructions, and Google-specific response structures.
type GoogleAdapter struct {
	config ProviderConfig
}

// NewGoogleAdapter creates a Google provider adapter with default endpoint.
// If no endpoint is configured, it defaults to Google's generative language API.
func NewGoogleAdapter(cfg ProviderConfig) *GoogleAdapter {
	if cfg.Endpoint == "" {
		cfg.Endpoint = "https://generativelanguage.googleapis.com/v1beta"
	}
	return &GoogleAdapter{config: cfg}
}

// Name returns the provider name.
func (a *GoogleAdapter) Name() string {
	return ProviderGoogle
}

// Build constructs a Google Gemini API request from normalized LLM request.
// It builds the generateContent request with API key authentication,
// system instructions, and Google-specific parameter formatting.
func (a *GoogleAdapter) Build(ctx context.Context, req *LLMRequest) (*http.Request, error) {
	endpoint := fmt.Sprintf("%s/models/%s:generateContent", a.config.Endpoint, req.Model)

	// Add API key to URL.
	endpoint = fmt.Sprintf("%s?key=%s", endpoint, a.config.APIKey)

	parts := []map[string]any{}

	var systemInstruction string
	if req.SystemPrompt != "" {
		systemInstruction = req.SystemPrompt
	}

	var userContent string
	switch req.Operation {
	case OpGeneration:
		userContent = req.Question
	case OpScoring:
		userContent = fmt.Sprintf("Question: %s\n\nAnswer to evaluate: %s",
			req.Question, extractAnswerContent(ctx, req.Answers, req.ArtifactStore))
	}

	parts = append(parts, map[string]any{
		"text": userContent,
	})

	body := map[string]any{
		"contents": []map[string]any{
			{
				"parts": parts,
			},
		},
		"generationConfig": map[string]any{
			"temperature":     req.Temperature,
			"maxOutputTokens": req.MaxTokens,
		},
	}

	if systemInstruction != "" {
		body["systemInstruction"] = map[string]any{
			"parts": []map[string]any{
				{"text": systemInstruction},
			},
		}
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

	for k, v := range a.config.Headers {
		httpReq.Header.Set(k, v)
	}

	return httpReq, nil
}

// Parse extracts normalized data from a Google Gemini API response.
// It handles Google's candidates format, extracts usage metadata, and maps
// finish reasons to domain types.
func (a *GoogleAdapter) Parse(httpResp *http.Response) (*Request, error) {
	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, parseGoogleError(httpResp.StatusCode, body)
	}

	var resp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
			TotalTokenCount      int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	var content string
	var finishReason domain.FinishReason

	if len(resp.Candidates) > 0 && len(resp.Candidates[0].Content.Parts) > 0 {
		content = resp.Candidates[0].Content.Parts[0].Text
		finishReason = mapGoogleFinishReason(resp.Candidates[0].FinishReason)
	}

	requestIDs := []string{}
	if reqID := httpResp.Header.Get("x-goog-request-id"); reqID != "" {
		requestIDs = append(requestIDs, reqID)
	} else if reqID := httpResp.Header.Get("x-request-id"); reqID != "" {
		requestIDs = append(requestIDs, reqID)
	}

	return &Request{
		Content:            content,
		FinishReason:       finishReason,
		ProviderRequestIDs: requestIDs,
		Usage: NormalizedUsage{
			PromptTokens:     int64(resp.UsageMetadata.PromptTokenCount),
			CompletionTokens: int64(resp.UsageMetadata.CandidatesTokenCount),
			TotalTokens:      int64(resp.UsageMetadata.TotalTokenCount),
		},
		Headers: httpResp.Header,
		RawBody: body,
	}, nil
}

// extractAnswerContent retrieves answer content from artifact storage for scoring.
// Returns formatted answer content or error descriptions for failed retrievals.
func extractAnswerContent(ctx context.Context, answers []domain.Answer, artifactStore ArtifactStore) string {
	if len(answers) == 0 {
		return ""
	}

	// Fetch actual content from artifact storage.
	answer := answers[0]
	if answer.ContentRef.Key == "" {
		return fmt.Sprintf("[Answer ID: %s - No content reference]", answer.ID)
	}

	if artifactStore == nil {
		return fmt.Sprintf("[Answer ID: %s - No artifact store configured]", answer.ID)
	}

	content, err := artifactStore.Get(ctx, answer.ContentRef)
	if err != nil {
		return fmt.Sprintf("[Answer ID: %s - Failed to fetch content: %v]", answer.ID, err)
	}

	return content
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

// mapGoogleFinishReason converts Google finishReason to domain FinishReason.
// Maps Google-specific completion reasons to normalized domain types.
func mapGoogleFinishReason(reason string) domain.FinishReason {
	switch strings.ToUpper(reason) {
	case "STOP":
		return domain.FinishStop
	case "MAX_TOKENS":
		return domain.FinishLength
	case "SAFETY", "BLOCKLIST":
		return domain.FinishContentFilter
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
		return &ProviderError{
			Provider:   ProviderOpenAI,
			StatusCode: statusCode,
			Message:    errResp.Error.Message,
			Code:       errResp.Error.Code,
			Type:       classifyErrorType(statusCode, errResp.Error.Type),
		}
	}

	return &ProviderError{
		Provider:   ProviderOpenAI,
		StatusCode: statusCode,
		Message:    string(body),
		Type:       classifyErrorType(statusCode, ""),
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
		return &ProviderError{
			Provider:   ProviderAnthropic,
			StatusCode: statusCode,
			Message:    errResp.Message,
			Code:       errResp.Type,
			Type:       classifyErrorType(statusCode, errResp.Type),
		}
	}

	return &ProviderError{
		Provider:   ProviderAnthropic,
		StatusCode: statusCode,
		Message:    string(body),
		Type:       classifyErrorType(statusCode, ""),
	}
}

// parseGoogleError converts Google error responses to ProviderError.
// Extracts error details from Google's JSON error format.
func parseGoogleError(statusCode int, body []byte) error {
	var errResp struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}

	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Error.Message != "" {
		return &ProviderError{
			Provider:   ProviderGoogle,
			StatusCode: statusCode,
			Message:    errResp.Error.Message,
			Code:       errResp.Error.Status,
			Type:       classifyErrorType(statusCode, errResp.Error.Status),
		}
	}

	return &ProviderError{
		Provider:   ProviderGoogle,
		StatusCode: statusCode,
		Message:    string(body),
		Type:       classifyErrorType(statusCode, ""),
	}
}

// classifyErrorType determines ErrorType from HTTP status and provider error codes.
// It examines both provider-specific error codes and HTTP status codes to
// classify errors into retryable and non-retryable categories.
func classifyErrorType(statusCode int, errorCode string) ErrorType {
	// Check error code first for specific classifications.
	lowerCode := strings.ToLower(errorCode)
	if strings.Contains(lowerCode, "rate") || strings.Contains(lowerCode, "limit") {
		return ErrorTypeRateLimit
	}
	if strings.Contains(lowerCode, "timeout") {
		return ErrorTypeTimeout
	}
	if strings.Contains(lowerCode, "auth") || strings.Contains(lowerCode, "unauthorized") {
		return ErrorTypeAuth
	}
	if strings.Contains(lowerCode, "permission") || strings.Contains(lowerCode, "forbidden") {
		return ErrorTypePermission
	}
	if strings.Contains(lowerCode, "quota") {
		return ErrorTypeQuota
	}

	// Fall back to status code classification.
	switch statusCode {
	case http.StatusTooManyRequests:
		return ErrorTypeRateLimit
	case http.StatusUnauthorized:
		return ErrorTypeAuth
	case http.StatusForbidden:
		return ErrorTypePermission
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return ErrorTypeTimeout
	case http.StatusBadRequest:
		return ErrorTypeValidation
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable:
		return ErrorTypeProvider
	default:
		if statusCode >= 500 {
			return ErrorTypeProvider
		}
		return ErrorTypeUnknown
	}
}
