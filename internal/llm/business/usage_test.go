package business

import (
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

func TestOpenAIUsageMapper_MapUsage(t *testing.T) {
	mapper := NewOpenAIUsageMapper()

	t.Run("nil_input", func(t *testing.T) {
		result, err := mapper.MapUsage(nil)
		require.NoError(t, err)
		assert.Equal(t, &transport.NormalizedUsage{}, result)
	})

	t.Run("openai_usage_struct", func(t *testing.T) {
		usage := OpenAIUsage{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
		}

		result, err := mapper.MapUsage(usage)
		require.NoError(t, err)
		assert.Equal(t, int64(100), result.PromptTokens)
		assert.Equal(t, int64(50), result.CompletionTokens)
		assert.Equal(t, int64(150), result.TotalTokens)
	})

	t.Run("openai_usage_pointer", func(t *testing.T) {
		usage := &OpenAIUsage{
			PromptTokens:     200,
			CompletionTokens: 100,
			TotalTokens:      300,
		}

		result, err := mapper.MapUsage(usage)
		require.NoError(t, err)
		assert.Equal(t, int64(200), result.PromptTokens)
		assert.Equal(t, int64(100), result.CompletionTokens)
		assert.Equal(t, int64(300), result.TotalTokens)
	})

	t.Run("map_string_any", func(t *testing.T) {
		usage := map[string]any{
			"prompt_tokens":     float64(150),
			"completion_tokens": float64(75),
			"total_tokens":      float64(225),
		}

		result, err := mapper.MapUsage(usage)
		require.NoError(t, err)
		assert.Equal(t, int64(150), result.PromptTokens)
		assert.Equal(t, int64(75), result.CompletionTokens)
		assert.Equal(t, int64(225), result.TotalTokens)
	})

	t.Run("json_raw_message", func(t *testing.T) {
		jsonData := `{"prompt_tokens": 80, "completion_tokens": 40, "total_tokens": 120}`
		rawMessage := json.RawMessage(jsonData)

		result, err := mapper.MapUsage(rawMessage)
		require.NoError(t, err)
		assert.Equal(t, int64(80), result.PromptTokens)
		assert.Equal(t, int64(40), result.CompletionTokens)
		assert.Equal(t, int64(120), result.TotalTokens)
	})

	t.Run("calculate_missing_total", func(t *testing.T) {
		usage := OpenAIUsage{
			PromptTokens:     60,
			CompletionTokens: 30,
			TotalTokens:      0, // Missing total
		}

		result, err := mapper.MapUsage(usage)
		require.NoError(t, err)
		assert.Equal(t, int64(60), result.PromptTokens)
		assert.Equal(t, int64(30), result.CompletionTokens)
		assert.Equal(t, int64(90), result.TotalTokens) // Calculated
	})

	t.Run("invalid_map_marshal_error", func(t *testing.T) {
		// Create a map with a non-marshallable value
		usage := map[string]any{
			"prompt_tokens": make(chan int), // Cannot marshal channels
		}

		result, err := mapper.MapUsage(usage)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "failed to marshal usage map")
	})

	t.Run("invalid_json_raw_message", func(t *testing.T) {
		rawMessage := json.RawMessage(`invalid json`)

		result, err := mapper.MapUsage(rawMessage)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "failed to unmarshal OpenAI usage from raw JSON")
	})

	t.Run("unsupported_type", func(t *testing.T) {
		result, err := mapper.MapUsage("invalid type")
		require.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, errors.ErrUnsupportedUsageType)
	})
}

func TestAnthropicUsageMapper_MapUsage(t *testing.T) {
	mapper := NewAnthropicUsageMapper()

	t.Run("nil_input", func(t *testing.T) {
		result, err := mapper.MapUsage(nil)
		require.NoError(t, err)
		assert.Equal(t, &transport.NormalizedUsage{}, result)
	})

	t.Run("anthropic_usage_struct", func(t *testing.T) {
		usage := AnthropicUsage{
			InputTokens:  120,
			OutputTokens: 80,
		}

		result, err := mapper.MapUsage(usage)
		require.NoError(t, err)
		assert.Equal(t, int64(120), result.PromptTokens)    // Input -> Prompt
		assert.Equal(t, int64(80), result.CompletionTokens) // Output -> Completion
		assert.Equal(t, int64(200), result.TotalTokens)     // Calculated sum
	})

	t.Run("anthropic_usage_pointer", func(t *testing.T) {
		usage := &AnthropicUsage{
			InputTokens:  90,
			OutputTokens: 60,
		}

		result, err := mapper.MapUsage(usage)
		require.NoError(t, err)
		assert.Equal(t, int64(90), result.PromptTokens)
		assert.Equal(t, int64(60), result.CompletionTokens)
		assert.Equal(t, int64(150), result.TotalTokens)
	})

	t.Run("map_string_any", func(t *testing.T) {
		usage := map[string]any{
			"input_tokens":  float64(110),
			"output_tokens": float64(70),
		}

		result, err := mapper.MapUsage(usage)
		require.NoError(t, err)
		assert.Equal(t, int64(110), result.PromptTokens)
		assert.Equal(t, int64(70), result.CompletionTokens)
		assert.Equal(t, int64(180), result.TotalTokens)
	})

	t.Run("json_raw_message", func(t *testing.T) {
		jsonData := `{"input_tokens": 95, "output_tokens": 55}`
		rawMessage := json.RawMessage(jsonData)

		result, err := mapper.MapUsage(rawMessage)
		require.NoError(t, err)
		assert.Equal(t, int64(95), result.PromptTokens)
		assert.Equal(t, int64(55), result.CompletionTokens)
		assert.Equal(t, int64(150), result.TotalTokens)
	})

	t.Run("invalid_json_raw_message", func(t *testing.T) {
		rawMessage := json.RawMessage(`invalid json`)

		result, err := mapper.MapUsage(rawMessage)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "failed to unmarshal Anthropic usage from raw JSON")
	})

	t.Run("unsupported_type", func(t *testing.T) {
		result, err := mapper.MapUsage(123)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, errors.ErrUnsupportedUsageType)
	})

	t.Run("empty_map", func(t *testing.T) {
		usage := map[string]any{}

		result, err := mapper.MapUsage(usage)
		require.NoError(t, err)
		assert.Equal(t, int64(0), result.PromptTokens)
		assert.Equal(t, int64(0), result.CompletionTokens)
		assert.Equal(t, int64(0), result.TotalTokens)
	})
}

func TestGoogleUsageMapper_MapUsage(t *testing.T) {
	mapper := NewGoogleUsageMapper()

	t.Run("nil_input", func(t *testing.T) {
		result, err := mapper.MapUsage(nil)
		require.NoError(t, err)
		assert.Equal(t, &transport.NormalizedUsage{}, result)
	})

	t.Run("google_usage_struct", func(t *testing.T) {
		usage := GoogleUsage{
			PromptTokenCount:     140,
			CandidatesTokenCount: 90,
			TotalTokenCount:      230,
		}

		result, err := mapper.MapUsage(usage)
		require.NoError(t, err)
		assert.Equal(t, int64(140), result.PromptTokens)    // Prompt -> Prompt
		assert.Equal(t, int64(90), result.CompletionTokens) // Candidates -> Completion
		assert.Equal(t, int64(230), result.TotalTokens)     // Total
	})

	t.Run("google_usage_pointer", func(t *testing.T) {
		usage := &GoogleUsage{
			PromptTokenCount:     160,
			CandidatesTokenCount: 40,
			TotalTokenCount:      200,
		}

		result, err := mapper.MapUsage(usage)
		require.NoError(t, err)
		assert.Equal(t, int64(160), result.PromptTokens)
		assert.Equal(t, int64(40), result.CompletionTokens)
		assert.Equal(t, int64(200), result.TotalTokens)
	})

	t.Run("map_string_any", func(t *testing.T) {
		usage := map[string]any{
			"promptTokenCount":     float64(130),
			"candidatesTokenCount": float64(70),
			"totalTokenCount":      float64(200),
		}

		result, err := mapper.MapUsage(usage)
		require.NoError(t, err)
		assert.Equal(t, int64(130), result.PromptTokens)
		assert.Equal(t, int64(70), result.CompletionTokens)
		assert.Equal(t, int64(200), result.TotalTokens)
	})

	t.Run("json_raw_message", func(t *testing.T) {
		jsonData := `{"promptTokenCount": 85, "candidatesTokenCount": 45, "totalTokenCount": 130}`
		rawMessage := json.RawMessage(jsonData)

		result, err := mapper.MapUsage(rawMessage)
		require.NoError(t, err)
		assert.Equal(t, int64(85), result.PromptTokens)
		assert.Equal(t, int64(45), result.CompletionTokens)
		assert.Equal(t, int64(130), result.TotalTokens)
	})

	t.Run("calculate_missing_total", func(t *testing.T) {
		usage := GoogleUsage{
			PromptTokenCount:     100,
			CandidatesTokenCount: 50,
			TotalTokenCount:      0, // Missing total
		}

		result, err := mapper.MapUsage(usage)
		require.NoError(t, err)
		assert.Equal(t, int64(100), result.PromptTokens)
		assert.Equal(t, int64(50), result.CompletionTokens)
		assert.Equal(t, int64(150), result.TotalTokens) // Calculated
	})

	t.Run("invalid_json_raw_message", func(t *testing.T) {
		rawMessage := json.RawMessage(`invalid json`)

		result, err := mapper.MapUsage(rawMessage)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "failed to unmarshal Google usage from raw JSON")
	})

	t.Run("unsupported_type", func(t *testing.T) {
		result, err := mapper.MapUsage([]int{1, 2, 3})
		require.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, errors.ErrUnsupportedUsageType)
	})
}

func TestUsageMapperFactory(t *testing.T) {
	factory := NewUsageMapperFactory()

	t.Run("get_openai_mapper", func(t *testing.T) {
		mapper, err := factory.GetMapper("openai")
		require.NoError(t, err)
		assert.NotNil(t, mapper)
		assert.IsType(t, &openAIUsageMapper{}, mapper)
	})

	t.Run("get_anthropic_mapper", func(t *testing.T) {
		mapper, err := factory.GetMapper("anthropic")
		require.NoError(t, err)
		assert.NotNil(t, mapper)
		assert.IsType(t, &anthropicUsageMapper{}, mapper)
	})

	t.Run("get_google_mapper", func(t *testing.T) {
		mapper, err := factory.GetMapper("google")
		require.NoError(t, err)
		assert.NotNil(t, mapper)
		assert.IsType(t, &googleUsageMapper{}, mapper)
	})

	t.Run("unsupported_provider", func(t *testing.T) {
		mapper, err := factory.GetMapper("unsupported")
		require.Error(t, err)
		assert.Nil(t, mapper)
		assert.ErrorIs(t, err, errors.ErrUnsupportedProvider)
		assert.Contains(t, err.Error(), "unsupported")
	})
}

func TestNormalizeUsage(t *testing.T) {
	t.Run("openai_provider", func(t *testing.T) {
		usage := OpenAIUsage{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
		}

		result, err := NormalizeUsage("openai", usage)
		require.NoError(t, err)
		assert.Equal(t, int64(100), result.PromptTokens)
		assert.Equal(t, int64(50), result.CompletionTokens)
		assert.Equal(t, int64(150), result.TotalTokens)
	})

	t.Run("anthropic_provider", func(t *testing.T) {
		usage := AnthropicUsage{
			InputTokens:  80,
			OutputTokens: 40,
		}

		result, err := NormalizeUsage("anthropic", usage)
		require.NoError(t, err)
		assert.Equal(t, int64(80), result.PromptTokens)
		assert.Equal(t, int64(40), result.CompletionTokens)
		assert.Equal(t, int64(120), result.TotalTokens)
	})

	t.Run("unsupported_provider", func(t *testing.T) {
		result, err := NormalizeUsage("unsupported", "data")
		require.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, errors.ErrUnsupportedProvider)
	})

	t.Run("mapper_error", func(t *testing.T) {
		result, err := NormalizeUsage("openai", "invalid data")
		require.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, errors.ErrUnsupportedUsageType)
	})
}

func TestValidateUsage(t *testing.T) {
	t.Run("nil_usage", func(t *testing.T) {
		err := ValidateUsage(nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrUsageNil)
	})

	t.Run("valid_usage", func(t *testing.T) {
		usage := &transport.NormalizedUsage{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
		}

		err := ValidateUsage(usage)
		assert.NoError(t, err)
	})

	t.Run("negative_prompt_tokens", func(t *testing.T) {
		usage := &transport.NormalizedUsage{
			PromptTokens:     -10,
			CompletionTokens: 50,
			TotalTokens:      40,
		}

		err := ValidateUsage(usage)
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrNegativePromptTokens)
	})

	t.Run("negative_completion_tokens", func(t *testing.T) {
		usage := &transport.NormalizedUsage{
			PromptTokens:     100,
			CompletionTokens: -20,
			TotalTokens:      80,
		}

		err := ValidateUsage(usage)
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrNegativeCompletionTokens)
	})

	t.Run("negative_total_tokens", func(t *testing.T) {
		usage := &transport.NormalizedUsage{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      -150,
		}

		err := ValidateUsage(usage)
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrNegativeTotalTokens)
	})

	t.Run("inconsistent_token_counts", func(t *testing.T) {
		usage := &transport.NormalizedUsage{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      200, // Should be 150
		}

		err := ValidateUsage(usage)
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrInconsistentTokenCounts)
	})

	t.Run("allowed_token_count_discrepancy", func(t *testing.T) {
		usage := &transport.NormalizedUsage{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      151, // Off by 1, should be allowed
		}

		err := ValidateUsage(usage)
		assert.NoError(t, err)
	})

	t.Run("zero_total_with_positive_components", func(t *testing.T) {
		usage := &transport.NormalizedUsage{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      0, // Zero total is allowed when components exist
		}

		err := ValidateUsage(usage)
		assert.NoError(t, err)
	})

	t.Run("suspiciously_high_token_count", func(t *testing.T) {
		usage := &transport.NormalizedUsage{
			PromptTokens:     2000000, // 2M tokens
			CompletionTokens: 0,
			TotalTokens:      2000000,
		}

		err := ValidateUsage(usage)
		require.Error(t, err)
		assert.ErrorIs(t, err, errors.ErrSuspiciouslyHighTokenCount)
	})

	t.Run("zero_usage", func(t *testing.T) {
		usage := &transport.NormalizedUsage{
			PromptTokens:     0,
			CompletionTokens: 0,
			TotalTokens:      0,
		}

		err := ValidateUsage(usage)
		assert.NoError(t, err)
	})
}

func TestEstimateCharactersFromTokens(t *testing.T) {
	tests := []struct {
		name     string
		tokens   int64
		expected int64
	}{
		{"zero_tokens", 0, 0},
		{"one_token", 1, 4},
		{"hundred_tokens", 100, 400},
		{"thousand_tokens", 1000, 4000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EstimateCharactersFromTokens(tt.tokens)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEstimateTokensFromCharacters(t *testing.T) {
	tests := []struct {
		name       string
		characters int64
		expected   int64
	}{
		{"zero_characters", 0, 0},
		{"few_characters", 3, 1},            // Ceiling division: (3 + 4 - 1) / 4 = 1
		{"four_characters", 4, 1},           // Exactly one token
		{"five_characters", 5, 2},           // Ceiling division: (5 + 4 - 1) / 4 = 2
		{"hundred_characters", 100, 25},     // 100 / 4 = 25
		{"hundred_one_characters", 101, 26}, // Ceiling division: (101 + 4 - 1) / 4 = 26
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EstimateTokensFromCharacters(tt.characters)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestUsageMapperWithLogger(t *testing.T) {
	// Test that mappers can be created and work with slog
	t.Run("openai_mapper_with_logger", func(t *testing.T) {
		mapper := &openAIUsageMapper{
			logger: slog.Default().With("test", "openai"),
		}

		usage := OpenAIUsage{
			PromptTokens:     100,
			CompletionTokens: 50,
			// TotalTokens is 0, should trigger debug log
		}

		result, err := mapper.MapUsage(usage)
		require.NoError(t, err)
		assert.Equal(t, int64(150), result.TotalTokens) // Should be calculated
	})

	t.Run("google_mapper_with_logger", func(t *testing.T) {
		mapper := &googleUsageMapper{
			logger: slog.Default().With("test", "google"),
		}

		usage := GoogleUsage{
			PromptTokenCount:     80,
			CandidatesTokenCount: 40,
			// TotalTokenCount is 0, should trigger debug log
		}

		result, err := mapper.MapUsage(usage)
		require.NoError(t, err)
		assert.Equal(t, int64(120), result.TotalTokens) // Should be calculated
	})
}

func TestUsageStructsJSONMarshaling(t *testing.T) {
	t.Run("openai_usage_json", func(t *testing.T) {
		usage := OpenAIUsage{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
		}

		data, err := json.Marshal(usage)
		require.NoError(t, err)

		var unmarshaled OpenAIUsage
		err = json.Unmarshal(data, &unmarshaled)
		require.NoError(t, err)
		assert.Equal(t, usage, unmarshaled)
	})

	t.Run("anthropic_usage_json", func(t *testing.T) {
		usage := AnthropicUsage{
			InputTokens:  80,
			OutputTokens: 40,
		}

		data, err := json.Marshal(usage)
		require.NoError(t, err)

		var unmarshaled AnthropicUsage
		err = json.Unmarshal(data, &unmarshaled)
		require.NoError(t, err)
		assert.Equal(t, usage, unmarshaled)
	})

	t.Run("google_usage_json", func(t *testing.T) {
		usage := GoogleUsage{
			PromptTokenCount:     100,
			CandidatesTokenCount: 50,
			TotalTokenCount:      150,
		}

		data, err := json.Marshal(usage)
		require.NoError(t, err)

		var unmarshaled GoogleUsage
		err = json.Unmarshal(data, &unmarshaled)
		require.NoError(t, err)
		assert.Equal(t, usage, unmarshaled)
	})
}
