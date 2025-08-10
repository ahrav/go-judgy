// Package business provides LLM usage tracking and provider normalization.
// Converts provider-specific token usage formats (OpenAI, Anthropic, Google)
// to a unified structure for cost calculation and resource monitoring.
package business

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// UsageMapper normalizes provider-specific usage data to a unified format.
// Each provider returns usage data in different formats which must be
// converted to NormalizedUsage for consistent cost calculation and monitoring.
// MapUsage returns an error for unsupported input types or invalid data.
type UsageMapper interface {
	MapUsage(rawUsage any) (*transport.NormalizedUsage, error)
}

// OpenAIUsage represents token usage data from OpenAI API responses.
// Maps to OpenAI's standard usage object format.
type OpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// AnthropicUsage represents token usage data from Anthropic API responses.
// Anthropic uses input/output terminology instead of prompt/completion.
type AnthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// GoogleUsage represents token usage data from Google Vertex AI responses.
// Google uses camelCase field names and candidates terminology.
type GoogleUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

// openAIUsageMapper implements UsageMapper for OpenAI responses.
type openAIUsageMapper struct {
	logger *slog.Logger
}

// NewOpenAIUsageMapper creates a usage mapper for OpenAI responses.
func NewOpenAIUsageMapper() UsageMapper {
	return &openAIUsageMapper{
		logger: slog.Default().With("mapper", "openai"),
	}
}

// MapUsage converts OpenAI usage data to normalized format.
// Handles OpenAIUsage struct, pointer, map[string]any, and json.RawMessage inputs.
// Returns ErrUnsupportedUsageType for unsupported input types.
func (m *openAIUsageMapper) MapUsage(rawUsage any) (*transport.NormalizedUsage, error) {
	if rawUsage == nil {
		return &transport.NormalizedUsage{}, nil
	}

	// Convert various input formats to OpenAIUsage struct.
	var usage OpenAIUsage
	switch v := rawUsage.(type) {
	case OpenAIUsage:
		usage = v
	case *OpenAIUsage:
		usage = *v
	case map[string]any:
		data, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal usage map: %w", err)
		}
		if err := json.Unmarshal(data, &usage); err != nil {
			return nil, fmt.Errorf("failed to unmarshal OpenAI usage: %w", err)
		}
	case json.RawMessage:
		if err := json.Unmarshal(v, &usage); err != nil {
			return nil, fmt.Errorf("failed to unmarshal OpenAI usage from raw JSON: %w", err)
		}
	default:
		return nil, fmt.Errorf("OpenAI usage type %T: %w", rawUsage, errors.ErrUnsupportedUsageType)
	}

	// Convert to standard field names and types.
	normalized := &transport.NormalizedUsage{
		PromptTokens:     int64(usage.PromptTokens),
		CompletionTokens: int64(usage.CompletionTokens),
		TotalTokens:      int64(usage.TotalTokens),
	}

	// Calculate missing total when individual counts provided.
	if normalized.TotalTokens == 0 && (normalized.PromptTokens > 0 || normalized.CompletionTokens > 0) {
		normalized.TotalTokens = normalized.PromptTokens + normalized.CompletionTokens
		m.logger.Debug("calculated missing total tokens",
			"prompt", normalized.PromptTokens,
			"completion", normalized.CompletionTokens,
			"total", normalized.TotalTokens)
	}

	return normalized, nil
}

// anthropicUsageMapper implements UsageMapper for Anthropic responses.
type anthropicUsageMapper struct {
	logger *slog.Logger
}

// NewAnthropicUsageMapper creates a usage mapper for Anthropic responses.
func NewAnthropicUsageMapper() UsageMapper {
	return &anthropicUsageMapper{
		logger: slog.Default().With("mapper", "anthropic"),
	}
}

// MapUsage converts Anthropic usage data to normalized format.
// Handles AnthropicUsage struct, pointer, map[string]any, and json.RawMessage inputs.
// Maps input_tokens to prompt_tokens and output_tokens to completion_tokens.
func (m *anthropicUsageMapper) MapUsage(rawUsage any) (*transport.NormalizedUsage, error) {
	if rawUsage == nil {
		return &transport.NormalizedUsage{}, nil
	}

	// Convert various input formats to AnthropicUsage struct.
	var usage AnthropicUsage
	switch v := rawUsage.(type) {
	case AnthropicUsage:
		usage = v
	case *AnthropicUsage:
		usage = *v
	case map[string]any:
		// Extract Anthropic field names from map interface.
		if input, ok := v["input_tokens"].(float64); ok {
			usage.InputTokens = int(input)
		}
		if output, ok := v["output_tokens"].(float64); ok {
			usage.OutputTokens = int(output)
		}
	case json.RawMessage:
		if err := json.Unmarshal(v, &usage); err != nil {
			return nil, fmt.Errorf("failed to unmarshal Anthropic usage from raw JSON: %w", err)
		}
	default:
		return nil, fmt.Errorf("anthropic usage type %T: %w", rawUsage, errors.ErrUnsupportedUsageType)
	}

	// Convert Anthropic input/output terminology to prompt/completion.
	normalized := &transport.NormalizedUsage{
		PromptTokens:     int64(usage.InputTokens),
		CompletionTokens: int64(usage.OutputTokens),
		TotalTokens:      int64(usage.InputTokens + usage.OutputTokens),
	}

	return normalized, nil
}

// googleUsageMapper implements UsageMapper for Google Vertex AI responses.
type googleUsageMapper struct {
	logger *slog.Logger
}

// NewGoogleUsageMapper creates a usage mapper for Google Vertex AI responses.
func NewGoogleUsageMapper() UsageMapper {
	return &googleUsageMapper{
		logger: slog.Default().With("mapper", "google"),
	}
}

// MapUsage converts Google usage data to normalized format.
// Handles GoogleUsage struct, pointer, map[string]any, and json.RawMessage inputs.
// Maps candidatesTokenCount to completion_tokens for consistency.
func (m *googleUsageMapper) MapUsage(rawUsage any) (*transport.NormalizedUsage, error) {
	if rawUsage == nil {
		return &transport.NormalizedUsage{}, nil
	}

	// Convert various input formats to GoogleUsage struct.
	var usage GoogleUsage
	switch v := rawUsage.(type) {
	case GoogleUsage:
		usage = v
	case *GoogleUsage:
		usage = *v
	case map[string]any:
		// Extract Google field names from map interface.
		if prompt, ok := v["promptTokenCount"].(float64); ok {
			usage.PromptTokenCount = int(prompt)
		}
		if candidates, ok := v["candidatesTokenCount"].(float64); ok {
			usage.CandidatesTokenCount = int(candidates)
		}
		if total, ok := v["totalTokenCount"].(float64); ok {
			usage.TotalTokenCount = int(total)
		}
	case json.RawMessage:
		if err := json.Unmarshal(v, &usage); err != nil {
			return nil, fmt.Errorf("failed to unmarshal Google usage from raw JSON: %w", err)
		}
	default:
		return nil, fmt.Errorf("google usage type %T: %w", rawUsage, errors.ErrUnsupportedUsageType)
	}

	// Convert Google candidates terminology to completion tokens.
	normalized := &transport.NormalizedUsage{
		PromptTokens:     int64(usage.PromptTokenCount),
		CompletionTokens: int64(usage.CandidatesTokenCount),
		TotalTokens:      int64(usage.TotalTokenCount),
	}

	// Calculate missing total when individual counts provided.
	if normalized.TotalTokens == 0 && (normalized.PromptTokens > 0 || normalized.CompletionTokens > 0) {
		normalized.TotalTokens = normalized.PromptTokens + normalized.CompletionTokens
		m.logger.Debug("calculated missing total tokens",
			"prompt", normalized.PromptTokens,
			"completion", normalized.CompletionTokens,
			"total", normalized.TotalTokens)
	}

	return normalized, nil
}

// UsageMapperFactory creates provider-specific usage mappers.
// Maintains a registry of mappers for OpenAI, Anthropic, and Google providers.
type UsageMapperFactory struct {
	mappers map[string]UsageMapper
}

// NewUsageMapperFactory creates a factory with all supported provider mappers.
func NewUsageMapperFactory() *UsageMapperFactory {
	return &UsageMapperFactory{
		mappers: map[string]UsageMapper{
			"openai":    NewOpenAIUsageMapper(),
			"anthropic": NewAnthropicUsageMapper(),
			"google":    NewGoogleUsageMapper(),
		},
	}
}

// GetMapper returns the usage mapper for the specified provider.
// Returns ErrUnsupportedProvider for unknown provider names.
func (f *UsageMapperFactory) GetMapper(provider string) (UsageMapper, error) {
	mapper, ok := f.mappers[provider]
	if !ok {
		return nil, fmt.Errorf("provider %s: %w", provider, errors.ErrUnsupportedProvider)
	}
	return mapper, nil
}

// NormalizeUsage is a convenience function to normalize usage data from any provider.
// Creates a factory, selects the appropriate mapper, and normalizes the usage data.
// Returns errors for unsupported providers or invalid usage data.
func NormalizeUsage(provider string, rawUsage any) (*transport.NormalizedUsage, error) {
	factory := NewUsageMapperFactory()
	mapper, err := factory.GetMapper(provider)
	if err != nil {
		return nil, err
	}
	return mapper.MapUsage(rawUsage)
}

// ValidateUsage checks if normalized usage data is valid and consistent.
// Validates non-negative values, token count consistency, and reasonable limits.
// Allows ±1 token discrepancy for provider rounding differences.
func ValidateUsage(usage *transport.NormalizedUsage) error {
	if usage == nil {
		return errors.ErrUsageNil
	}

	// Reject negative token counts.
	if usage.PromptTokens < 0 {
		return fmt.Errorf("prompt tokens %d: %w", usage.PromptTokens, errors.ErrNegativePromptTokens)
	}
	if usage.CompletionTokens < 0 {
		return fmt.Errorf("completion tokens %d: %w", usage.CompletionTokens, errors.ErrNegativeCompletionTokens)
	}
	if usage.TotalTokens < 0 {
		return fmt.Errorf("total tokens %d: %w", usage.TotalTokens, errors.ErrNegativeTotalTokens)
	}

	// Verify total matches sum of prompt and completion tokens.
	if usage.TotalTokens > 0 {
		calculated := usage.PromptTokens + usage.CompletionTokens
		if calculated > 0 && usage.TotalTokens != calculated {
			// Allow ±1 discrepancy for provider rounding differences.
			diff := usage.TotalTokens - calculated
			if diff < -1 || diff > 1 {
				return fmt.Errorf("total=%d, prompt+completion=%d: %w",
					usage.TotalTokens, calculated, errors.ErrInconsistentTokenCounts)
			}
		}
	}

	// Reject unreasonably high token counts that likely indicate errors.
	const maxReasonableTokens = 1000000 // 1M tokens
	if usage.TotalTokens > maxReasonableTokens {
		return fmt.Errorf("token count %d: %w", usage.TotalTokens, errors.ErrSuspiciouslyHighTokenCount)
	}

	return nil
}

// EstimateCharactersFromTokens estimates character count from token count.
// Uses 4:1 character-to-token ratio; accuracy varies by language and content type.
func EstimateCharactersFromTokens(tokens int64) int64 {
	const avgCharsPerToken = 4
	return tokens * avgCharsPerToken
}

// EstimateTokensFromCharacters estimates token count from character count.
// Uses 4:1 character-to-token ratio with ceiling division for conservative estimates.
func EstimateTokensFromCharacters(characters int64) int64 {
	const avgCharsPerToken = 4
	if characters == 0 {
		return 0
	}
	return (characters + avgCharsPerToken - 1) / avgCharsPerToken // Ceiling division.
}
