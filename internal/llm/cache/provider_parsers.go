package cache

import (
	"encoding/json"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/providers"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// extractContentFromRawResponse parses the main content from a raw response
// body based on the provider. It delegates to provider-specific parsers and
// includes a fallback for unknown providers.
func extractContentFromRawResponse(rawBody []byte, provider string) string {
	if len(rawBody) == 0 {
		return ""
	}

	switch provider {
	case providers.ProviderOpenAI:
		return extractOpenAIContent(rawBody)
	case providers.ProviderAnthropic:
		return extractAnthropicContent(rawBody)
	case providers.ProviderGoogle:
		return extractGoogleContent(rawBody)
	default:
		// Fallback: try to parse as generic JSON and extract content field.
		var generic struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(rawBody, &generic); err == nil && generic.Content != "" {
			return generic.Content
		}
		// Return raw content for debugging when all parsing fails.
		return string(rawBody)
	}
}

// extractOpenAIContent extracts the message content from a standard OpenAI API
// response. It specifically looks for "choices[0].message.content".
func extractOpenAIContent(rawBody []byte) string {
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(rawBody, &resp); err != nil {
		return string(rawBody) // Use raw content when parsing fails.
	}

	if len(resp.Choices) > 0 {
		return resp.Choices[0].Message.Content
	}

	return string(rawBody)
}

// extractAnthropicContent extracts the text content from a standard Anthropic
// API response. It specifically looks for "content[0].text".
func extractAnthropicContent(rawBody []byte) string {
	var resp struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}

	if err := json.Unmarshal(rawBody, &resp); err != nil {
		return string(rawBody) // Use raw content when parsing fails.
	}

	if len(resp.Content) > 0 {
		return resp.Content[0].Text
	}

	return string(rawBody)
}

// extractGoogleContent extracts the text content from a standard Google Gemini
// API response. It specifically looks for "candidates[0].content.parts[0].text".
func extractGoogleContent(rawBody []byte) string {
	var resp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	if err := json.Unmarshal(rawBody, &resp); err != nil {
		return string(rawBody) // Use raw content when parsing fails.
	}

	if len(resp.Candidates) > 0 && len(resp.Candidates[0].Content.Parts) > 0 {
		return resp.Candidates[0].Content.Parts[0].Text
	}

	return string(rawBody)
}

// extractFinishReasonFromUsage derives a domain.FinishReason from usage
// statistics. This function provides a placeholder implementation that can be
// extended with provider-specific logic.
func extractFinishReasonFromUsage(_ *transport.NormalizedUsage) domain.FinishReason {
	// Default implementation, extend with provider-specific logic as needed.
	return domain.FinishReason("stop")
}

// extractRequestIDsFromHeaders extracts the request ID from response headers.
// It looks for the "X-Request-ID" header, which is useful for tracing and
// debugging.
func extractRequestIDsFromHeaders(headers map[string]string) []string {
	if reqID, exists := headers["X-Request-ID"]; exists {
		return []string{reqID}
	}
	return []string{}
}
