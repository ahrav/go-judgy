package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/configuration"
	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// GoogleAdapter implements ProviderAdapter for Google Gemini models.
// It handles Google's generateContent API format with API key authentication,
// system instructions, and Google-specific response structures.
type GoogleAdapter struct {
	config configuration.ProviderConfig
}

// NewGoogleAdapter creates a Google provider adapter with default endpoint.
// If no endpoint is configured, it defaults to Google's generative language API.
func NewGoogleAdapter(cfg configuration.ProviderConfig) *GoogleAdapter {
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
func (a *GoogleAdapter) Build(ctx context.Context, req *transport.Request) (*http.Request, error) {
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
	case transport.OpGeneration:
		userContent = req.Question
	case transport.OpScoring:
		userContent = fmt.Sprintf("Question: %s\n\nAnswer to evaluate: %s",
			req.Question, transport.ExtractAnswerContent(ctx, req.Answers, req.ArtifactStore))
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
func (a *GoogleAdapter) Parse(httpResp *http.Response) (*transport.Response, error) {
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

	return &transport.Response{
		Content:            content,
		FinishReason:       finishReason,
		ProviderRequestIDs: requestIDs,
		Usage: transport.NormalizedUsage{
			PromptTokens:     int64(resp.UsageMetadata.PromptTokenCount),
			CompletionTokens: int64(resp.UsageMetadata.CandidatesTokenCount),
			TotalTokens:      int64(resp.UsageMetadata.TotalTokenCount),
		},
		Headers: httpResp.Header,
		RawBody: body,
	}, nil
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
		return &llmerrors.ProviderError{
			Provider:   ProviderGoogle,
			StatusCode: statusCode,
			Message:    errResp.Error.Message,
			Code:       errResp.Error.Status,
			Type:       classifyErrorType(statusCode, errResp.Error.Status),
		}
	}

	return &llmerrors.ProviderError{
		Provider:   ProviderGoogle,
		StatusCode: statusCode,
		Message:    string(body),
		Type:       classifyErrorType(statusCode, ""),
	}
}
