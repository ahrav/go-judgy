package business

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ahrav/go-judgy/internal/domain"
	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

func TestValidateAndRepairScore(t *testing.T) {
	t.Run("valid_json_no_repair_needed", func(t *testing.T) {
		content := `{"value": 0.8, "confidence": 0.9, "reasoning": "Good answer"}`

		result, err := ValidateAndRepairScore(content, false)
		require.NoError(t, err)
		assert.Equal(t, 0.8, result.Value)
		assert.Equal(t, 0.9, result.Confidence)
		assert.Equal(t, "Good answer", result.Reasoning)
	})

	t.Run("invalid_value_repaired", func(t *testing.T) {
		content := `{"value": 85, "confidence": 0.9, "reasoning": "Good answer"}`

		result, err := ValidateAndRepairScore(content, true)
		require.NoError(t, err)
		assert.Equal(t, 0.85, result.Value) // Converted from percentage
		assert.Equal(t, 0.9, result.Confidence)
		assert.Equal(t, "Good answer", result.Reasoning)
	})

	t.Run("repair_disabled_validation_fails", func(t *testing.T) {
		content := `{"value": 1.5, "confidence": 0.9, "reasoning": "Good answer"}`

		result, err := ValidateAndRepairScore(content, false)
		require.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("json_repair_success", func(t *testing.T) {
		content := `{"value": 0.8, "confidence": 0.9, "reasoning": "Good answer",}` // Trailing comma

		result, err := ValidateAndRepairScore(content, true)
		require.NoError(t, err)
		assert.Equal(t, 0.8, result.Value)
	})

	t.Run("markdown_extraction", func(t *testing.T) {
		content := "Here's my analysis:\n```json\n{\"value\": 0.75, \"confidence\": 0.8, \"reasoning\": \"Decent response\"}\n```"

		result, err := ValidateAndRepairScore(content, true)
		require.NoError(t, err)
		assert.Equal(t, 0.75, result.Value)
		assert.Equal(t, 0.8, result.Confidence)
		assert.Equal(t, "Decent response", result.Reasoning)
	})

	t.Run("all_attempts_failed", func(t *testing.T) {
		content := "This is not JSON at all"

		result, err := ValidateAndRepairScore(content, true)
		require.Error(t, err)
		assert.Nil(t, result)

		var validationErr *llmerrors.ValidationError
		assert.ErrorAs(t, err, &validationErr)
		assert.Contains(t, validationErr.Message, "invalid score response format")
	})

	t.Run("with_dimensions", func(t *testing.T) {
		content := `{
			"value": 0.85,
			"confidence": 0.9,
			"reasoning": "Well-structured response",
			"dimensions": [
				{"name": "accuracy", "value": 0.9},
				{"name": "clarity", "value": 0.8}
			]
		}`

		result, err := ValidateAndRepairScore(content, false)
		require.NoError(t, err)
		assert.Equal(t, 0.85, result.Value)
		assert.Len(t, result.Dimensions, 2)
		assert.Equal(t, domain.Dimension("accuracy"), result.Dimensions[0].Name)
		assert.Equal(t, 0.9, result.Dimensions[0].Value)
	})
}

func TestValidateScoreData(t *testing.T) {
	t.Run("valid_score", func(t *testing.T) {
		score := &ScoreData{
			Value:      0.8,
			Confidence: 0.9,
			Reasoning:  "Good answer",
		}

		err := validateScoreData(score)
		assert.NoError(t, err)
	})

	t.Run("value_out_of_range_low", func(t *testing.T) {
		score := &ScoreData{
			Value:      -0.1,
			Confidence: 0.9,
			Reasoning:  "Test",
		}

		err := validateScoreData(score)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrScoreValueOutOfRange)
	})

	t.Run("value_out_of_range_high", func(t *testing.T) {
		score := &ScoreData{
			Value:      1.1,
			Confidence: 0.9,
			Reasoning:  "Test",
		}

		err := validateScoreData(score)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrScoreValueOutOfRange)
	})

	t.Run("confidence_out_of_range_low", func(t *testing.T) {
		score := &ScoreData{
			Value:      0.8,
			Confidence: -0.1,
			Reasoning:  "Test",
		}

		err := validateScoreData(score)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrConfidenceOutOfRange)
	})

	t.Run("confidence_out_of_range_high", func(t *testing.T) {
		score := &ScoreData{
			Value:      0.8,
			Confidence: 1.1,
			Reasoning:  "Test",
		}

		err := validateScoreData(score)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrConfidenceOutOfRange)
	})

	t.Run("empty_reasoning", func(t *testing.T) {
		score := &ScoreData{
			Value:      0.8,
			Confidence: 0.9,
			Reasoning:  "",
		}

		err := validateScoreData(score)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrReasoningEmpty)
	})

	t.Run("whitespace_only_reasoning", func(t *testing.T) {
		score := &ScoreData{
			Value:      0.8,
			Confidence: 0.9,
			Reasoning:  "   \t\n   ",
		}

		err := validateScoreData(score)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrReasoningEmpty)
	})

	t.Run("dimension_value_out_of_range", func(t *testing.T) {
		score := &ScoreData{
			Value:      0.8,
			Confidence: 0.9,
			Reasoning:  "Test",
			Dimensions: []domain.DimensionScore{
				{Name: "accuracy", Value: 1.5}, // Out of range
			},
		}

		err := validateScoreData(score)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrDimensionValueOutOfRange)
	})

	t.Run("dimension_empty_name", func(t *testing.T) {
		score := &ScoreData{
			Value:      0.8,
			Confidence: 0.9,
			Reasoning:  "Test",
			Dimensions: []domain.DimensionScore{
				{Name: "", Value: 0.8}, // Empty name
			},
		}

		err := validateScoreData(score)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrDimensionNameEmpty)
	})

	t.Run("valid_dimensions", func(t *testing.T) {
		score := &ScoreData{
			Value:      0.8,
			Confidence: 0.9,
			Reasoning:  "Test",
			Dimensions: []domain.DimensionScore{
				{Name: "accuracy", Value: 0.9},
				{Name: "clarity", Value: 0.7},
			},
		}

		err := validateScoreData(score)
		assert.NoError(t, err)
	})

	t.Run("boundary_values", func(t *testing.T) {
		score := &ScoreData{
			Value:      0.0, // Minimum valid
			Confidence: 1.0, // Maximum valid
			Reasoning:  "Test",
		}

		err := validateScoreData(score)
		assert.NoError(t, err)
	})
}

func TestRepairScoreData(t *testing.T) {
	t.Run("no_repair_needed", func(t *testing.T) {
		score := &ScoreData{
			Value:      0.8,
			Confidence: 0.9,
			Reasoning:  "Good answer",
		}

		result := repairScoreData(score)
		assert.Nil(t, result) // No repair needed
	})

	t.Run("clamp_negative_value", func(t *testing.T) {
		score := &ScoreData{
			Value:      -0.5,
			Confidence: 0.9,
			Reasoning:  "Test",
		}

		result := repairScoreData(score)
		require.NotNil(t, result)
		assert.Equal(t, 0.0, result.Value)
	})

	t.Run("convert_percentage_value", func(t *testing.T) {
		score := &ScoreData{
			Value:      85.0, // Percentage
			Confidence: 0.9,
			Reasoning:  "Test",
		}

		result := repairScoreData(score)
		require.NotNil(t, result)
		assert.Equal(t, 0.85, result.Value)
	})

	t.Run("clamp_excessive_value", func(t *testing.T) {
		score := &ScoreData{
			Value:      150.0, // > 100, should clamp to 1.0
			Confidence: 0.9,
			Reasoning:  "Test",
		}

		result := repairScoreData(score)
		require.NotNil(t, result)
		assert.Equal(t, 1.0, result.Value)
	})

	t.Run("convert_percentage_confidence", func(t *testing.T) {
		score := &ScoreData{
			Value:      0.8,
			Confidence: 90.0, // Percentage
			Reasoning:  "Test",
		}

		result := repairScoreData(score)
		require.NotNil(t, result)
		assert.Equal(t, 0.9, result.Confidence)
	})

	t.Run("default_empty_reasoning", func(t *testing.T) {
		score := &ScoreData{
			Value:      0.8,
			Confidence: 0.9,
			Reasoning:  "",
		}

		result := repairScoreData(score)
		require.NotNil(t, result)
		assert.Equal(t, "No reasoning provided", result.Reasoning)
	})

	t.Run("repair_dimensions", func(t *testing.T) {
		score := &ScoreData{
			Value:      0.8,
			Confidence: 0.9,
			Reasoning:  "Test",
			Dimensions: []domain.DimensionScore{
				{Name: "accuracy", Value: 95.0}, // Percentage
				{Name: "clarity", Value: -0.1},  // Negative
				{Name: "relevance", Value: 150}, // Too high
			},
		}

		result := repairScoreData(score)
		require.NotNil(t, result)
		assert.Equal(t, 0.95, result.Dimensions[0].Value)
		assert.Equal(t, 0.0, result.Dimensions[1].Value)
		assert.Equal(t, 1.0, result.Dimensions[2].Value)
	})

	t.Run("multiple_repairs", func(t *testing.T) {
		score := &ScoreData{
			Value:      85.0, // Percentage
			Confidence: -0.1, // Negative
			Reasoning:  "",   // Empty
		}

		result := repairScoreData(score)
		require.NotNil(t, result)
		assert.Equal(t, 0.85, result.Value)
		assert.Equal(t, 0.0, result.Confidence)
		assert.Equal(t, "No reasoning provided", result.Reasoning)
	})
}

func TestRepairJSON(t *testing.T) {
	t.Run("remove_trailing_comma", func(t *testing.T) {
		input := `{"value": 0.8, "confidence": 0.9,}`
		expected := `{"value": 0.8, "confidence": 0.9}`

		result := repairJSON(input)
		assert.Equal(t, expected, result)
	})

	t.Run("add_missing_closing_brace", func(t *testing.T) {
		input := `{"value": 0.8, "confidence": 0.9`
		expected := `{"value": 0.8, "confidence": 0.9}`

		result := repairJSON(input)
		assert.Equal(t, expected, result)
	})

	t.Run("add_missing_closing_bracket", func(t *testing.T) {
		input := `[{"value": 0.8}, {"value": 0.9`
		expected := `[{"value": 0.8}, {"value": 0.9}]`

		result := repairJSON(input)
		assert.Equal(t, expected, result)
	})

	t.Run("fix_unquoted_keys", func(t *testing.T) {
		input := `{value: 0.8, confidence: 0.9}`
		expected := `{"value": 0.8,"confidence": 0.9}`

		result := repairJSON(input)
		assert.Equal(t, expected, result)
	})

	t.Run("fix_single_quotes", func(t *testing.T) {
		input := `{'value': 0.8, 'confidence': 0.9}`
		expected := `{"value": 0.8, "confidence": 0.9}`

		result := repairJSON(input)
		assert.Equal(t, expected, result)
	})

	t.Run("remove_bom", func(t *testing.T) {
		input := "\ufeff{\"value\": 0.8}"
		expected := `{"value": 0.8}`

		result := repairJSON(input)
		assert.Equal(t, expected, result)
	})

	t.Run("trim_whitespace", func(t *testing.T) {
		input := "  \n  {\"value\": 0.8}  \n  "
		expected := `{"value": 0.8}`

		result := repairJSON(input)
		assert.Equal(t, expected, result)
	})

	t.Run("complex_repair", func(t *testing.T) {
		input := `{value: 0.8, confidence: 0.9, 'reasoning': 'Good',}`
		expected := `{"value": 0.8,"confidence": 0.9, 'reasoning': 'Good'}`

		result := repairJSON(input)
		assert.Equal(t, expected, result)
	})

	t.Run("do_not_change_valid_json", func(t *testing.T) {
		input := `{"value": 0.8, "confidence": 0.9}`

		result := repairJSON(input)
		assert.Equal(t, input, result) // Should be unchanged
	})
}

func TestExtractJSON(t *testing.T) {
	t.Run("extract_from_json_code_block", func(t *testing.T) {
		content := "Here's the result:\n```json\n{\"value\": 0.8}\n```\nEnd."
		expected := `{"value": 0.8}`

		result := extractJSON(content)
		assert.Equal(t, expected, result)
	})

	t.Run("extract_from_generic_code_block", func(t *testing.T) {
		content := "Result:\n```\n{\"value\": 0.8}\n```"
		expected := `{"value": 0.8}`

		result := extractJSON(content)
		assert.Equal(t, expected, result)
	})

	t.Run("extract_from_inline_code", func(t *testing.T) {
		content := "The score is `{\"value\": 0.8}` based on analysis."
		expected := `{"value": 0.8}`

		result := extractJSON(content)
		assert.Equal(t, expected, result)
	})

	t.Run("extract_from_text_with_braces", func(t *testing.T) {
		content := "Analysis shows {\"value\": 0.8, \"confidence\": 0.9} for this response."
		expected := `{"value": 0.8, "confidence": 0.9}`

		result := extractJSON(content)
		assert.Equal(t, expected, result)
	})

	t.Run("no_json_found", func(t *testing.T) {
		content := "This is just plain text with no JSON."

		result := extractJSON(content)
		assert.Equal(t, content, result) // Returns original if no extraction possible
	})

	t.Run("malformed_braces", func(t *testing.T) {
		content := "Some text } with backwards { braces."

		result := extractJSON(content)
		assert.Equal(t, content, result) // No valid extraction
	})

	t.Run("complex_markdown_with_json", func(t *testing.T) {
		content := `# Analysis Result

Here's the detailed scoring:

` + "```json" + `
{
  "value": 0.85,
  "confidence": 0.9,
  "reasoning": "Well-structured response"
}
` + "```" + `

This represents a high-quality answer.`

		expected := `{
  "value": 0.85,
  "confidence": 0.9,
  "reasoning": "Well-structured response"
}`

		result := extractJSON(content)
		assert.Equal(t, expected, result)
	})
}

func TestValidateGenerationResponse(t *testing.T) {
	t.Run("valid_response", func(t *testing.T) {
		content := "This is a valid response with meaningful content."

		err := ValidateGenerationResponse(content)
		assert.NoError(t, err)
	})

	t.Run("empty_response", func(t *testing.T) {
		content := ""

		err := ValidateGenerationResponse(content)
		require.Error(t, err)

		var validationErr *llmerrors.ValidationError
		assert.ErrorAs(t, err, &validationErr)
		assert.Contains(t, validationErr.Message, "empty generation response")
	})

	t.Run("whitespace_only_response", func(t *testing.T) {
		content := "   \t\n   "

		err := ValidateGenerationResponse(content)
		require.Error(t, err)

		var validationErr *llmerrors.ValidationError
		assert.ErrorAs(t, err, &validationErr)
	})

	t.Run("refusal_patterns_detected_but_allowed", func(t *testing.T) {
		refusalPatterns := []string{
			"I cannot help with that request.",
			"I can't provide that information.",
			"I am unable to assist with this.",
			"I'm unable to complete this task.",
			"As an AI, I cannot do that.",
			"As a language model, I cannot provide that.",
			"Error: Invalid request",
			"Exception: Processing failed",
		}

		for _, pattern := range refusalPatterns {
			err := ValidateGenerationResponse(pattern)
			// These should NOT fail validation - refusals are valid responses
			assert.NoError(t, err, "Pattern '%s' should be allowed", pattern)
		}
	})

	t.Run("mixed_case_refusal_patterns", func(t *testing.T) {
		content := "I Cannot Help With That Request."

		err := ValidateGenerationResponse(content)
		assert.NoError(t, err) // Should still pass validation
	})
}

func TestValidateProviderResponse(t *testing.T) {
	t.Run("nil_response", func(t *testing.T) {
		err := ValidateProviderResponse(nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNilResponse)
	})

	t.Run("valid_response", func(t *testing.T) {
		resp := &transport.Response{
			Content: "Valid response content",
			Usage: transport.NormalizedUsage{
				PromptTokens:     100,
				CompletionTokens: 50,
				TotalTokens:      150,
			},
		}

		err := ValidateProviderResponse(resp)
		assert.NoError(t, err)
	})

	t.Run("empty_content_non_tool_use", func(t *testing.T) {
		resp := &transport.Response{
			Content:      "",
			FinishReason: domain.FinishStop, // Not tool use
			Usage:        transport.NormalizedUsage{TotalTokens: 10},
		}

		err := ValidateProviderResponse(resp)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrEmptyResponseContent)
	})

	t.Run("empty_content_tool_use_allowed", func(t *testing.T) {
		resp := &transport.Response{
			Content:      "",
			FinishReason: domain.FinishToolUse, // Tool use allows empty content
			Usage:        transport.NormalizedUsage{TotalTokens: 10},
		}

		err := ValidateProviderResponse(resp)
		assert.NoError(t, err)
	})

	t.Run("negative_token_count", func(t *testing.T) {
		resp := &transport.Response{
			Content: "Valid content",
			Usage: transport.NormalizedUsage{
				TotalTokens: -10, // Negative
			},
		}

		err := ValidateProviderResponse(resp)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNegativeTokenCount)
	})

	t.Run("inconsistent_token_counts", func(t *testing.T) {
		resp := &transport.Response{
			Content: "Valid content",
			Usage: transport.NormalizedUsage{
				PromptTokens:     100,
				CompletionTokens: 50,
				TotalTokens:      200, // Should be 150, but this just logs a warning
			},
		}

		err := ValidateProviderResponse(resp)
		assert.NoError(t, err) // Should not fail, just log warning
	})

	t.Run("zero_token_counts", func(t *testing.T) {
		resp := &transport.Response{
			Content: "Valid content",
			Usage: transport.NormalizedUsage{
				PromptTokens:     0,
				CompletionTokens: 0,
				TotalTokens:      0,
			},
		}

		err := ValidateProviderResponse(resp)
		assert.NoError(t, err)
	})
}

func TestDefaultRepairConfig(t *testing.T) {
	config := DefaultRepairConfig()

	assert.Equal(t, 1, config.MaxAttempts)
	assert.True(t, config.EnableExtraction)
	assert.True(t, config.EnableClamping)
	assert.True(t, config.EnableDefaults)
}

func TestRepairConfigStruct(t *testing.T) {
	config := &RepairConfig{
		MaxAttempts:      3,
		EnableExtraction: false,
		EnableClamping:   false,
		EnableDefaults:   true,
	}

	assert.Equal(t, 3, config.MaxAttempts)
	assert.False(t, config.EnableExtraction)
	assert.False(t, config.EnableClamping)
	assert.True(t, config.EnableDefaults)
}

func TestTryParseRepairedJSON(t *testing.T) {
	t.Run("successful_repair", func(t *testing.T) {
		content := `{"value": 0.8, "confidence": 0.9, "reasoning": "Good",}`

		result := tryParseRepairedJSON(content)
		require.NotNil(t, result)
		assert.Equal(t, 0.8, result.Value)
		assert.Equal(t, 0.9, result.Confidence)
		assert.Equal(t, "Good", result.Reasoning)
	})

	t.Run("no_repair_needed", func(t *testing.T) {
		content := `{"value": 0.8, "confidence": 0.9, "reasoning": "Good"}`

		result := tryParseRepairedJSON(content)
		assert.Nil(t, result) // No repair was needed, so returns nil
	})

	t.Run("repair_with_validation_repair", func(t *testing.T) {
		content := `{"value": 85, "confidence": 0.9, "reasoning": "Good",}` // Needs JSON repair + validation repair

		result := tryParseRepairedJSON(content)
		require.NotNil(t, result)
		assert.Equal(t, 0.85, result.Value) // Converted from percentage
	})

	t.Run("repair_failed", func(t *testing.T) {
		content := `completely invalid json that cannot be repaired`

		result := tryParseRepairedJSON(content)
		assert.Nil(t, result)
	})
}

func TestTryParseExtractedJSON(t *testing.T) {
	t.Run("successful_extraction", func(t *testing.T) {
		content := "Result: ```json\n{\"value\": 0.75, \"confidence\": 0.8, \"reasoning\": \"Fair\"}\n```"

		result := tryParseExtractedJSON(content, true)
		require.NotNil(t, result)
		assert.Equal(t, 0.75, result.Value)
		assert.Equal(t, "Fair", result.Reasoning)
	})

	t.Run("extraction_with_repair", func(t *testing.T) {
		content := "Result: ```json\n{\"value\": 75, \"confidence\": 0.8, \"reasoning\": \"Fair\"}\n```"

		result := tryParseExtractedJSON(content, true)
		require.NotNil(t, result)
		assert.Equal(t, 0.75, result.Value) // Repaired from percentage
	})

	t.Run("extraction_repair_disabled", func(t *testing.T) {
		content := "Result: ```json\n{\"value\": 1.5, \"confidence\": 0.8, \"reasoning\": \"Fair\"}\n```"

		result := tryParseExtractedJSON(content, false)
		assert.Nil(t, result) // Repair disabled, validation fails
	})

	t.Run("no_extraction_possible", func(t *testing.T) {
		content := "This has no JSON content at all"

		result := tryParseExtractedJSON(content, true)
		assert.Nil(t, result)
	})

	t.Run("extraction_failed_json_parse", func(t *testing.T) {
		content := "Result: ```json\ninvalid json content\n```"

		result := tryParseExtractedJSON(content, true)
		assert.Nil(t, result)
	})
}

func TestValidationConstants(t *testing.T) {
	assert.Equal(t, 100, PercentageConversionFactor)
}

func TestValidationErrors(t *testing.T) {
	// Test that error variables are properly defined
	assert.NotNil(t, ErrScoreValueOutOfRange)
	assert.NotNil(t, ErrConfidenceOutOfRange)
	assert.NotNil(t, ErrReasoningEmpty)
	assert.NotNil(t, ErrDimensionValueOutOfRange)
	assert.NotNil(t, ErrDimensionNameEmpty)
	assert.NotNil(t, ErrNilResponse)
	assert.NotNil(t, ErrEmptyResponseContent)
	assert.NotNil(t, ErrNegativeTokenCount)

	// Test error messages contain expected content
	assert.Contains(t, ErrScoreValueOutOfRange.Error(), "score value")
	assert.Contains(t, ErrConfidenceOutOfRange.Error(), "confidence")
	assert.Contains(t, ErrReasoningEmpty.Error(), "reasoning")
	assert.Contains(t, ErrDimensionValueOutOfRange.Error(), "dimension value")
	assert.Contains(t, ErrDimensionNameEmpty.Error(), "dimension name")
}

func TestScoreDataStruct(t *testing.T) {
	score := ScoreData{
		Value:      0.85,
		Confidence: 0.9,
		Reasoning:  "Test reasoning",
		Dimensions: []domain.DimensionScore{
			{Name: "accuracy", Value: 0.9},
		},
	}

	assert.Equal(t, 0.85, score.Value)
	assert.Equal(t, 0.9, score.Confidence)
	assert.Equal(t, "Test reasoning", score.Reasoning)
	assert.Len(t, score.Dimensions, 1)
	assert.Equal(t, domain.Dimension("accuracy"), score.Dimensions[0].Name)
	assert.Equal(t, 0.9, score.Dimensions[0].Value)
}
