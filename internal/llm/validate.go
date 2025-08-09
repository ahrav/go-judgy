package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/ahrav/go-judgy/internal/domain"
)

// Validation constants.
const (
	PercentageConversionFactor = 100
)

// Validation errors for score data.
var (
	ErrScoreValueOutOfRange     = errors.New("score value must be between 0 and 1")
	ErrConfidenceOutOfRange     = errors.New("confidence must be between 0 and 1")
	ErrReasoningEmpty           = errors.New("reasoning cannot be empty")
	ErrDimensionValueOutOfRange = errors.New("dimension value must be between 0 and 1")
	ErrDimensionNameEmpty       = errors.New("dimension name cannot be empty")
	ErrNilResponse              = errors.New("nil response")
	ErrEmptyResponseContent     = errors.New("empty response content")
	ErrNegativeTokenCount       = errors.New("negative token count")
)

// ScoreData represents the expected structure of LLM scoring responses.
// It captures evaluation metrics with confidence levels and reasoning
// to support quality assessment and validation workflows.
type ScoreData struct {
	Value      float64                 `json:"value"`                // Score between 0 and 1.
	Confidence float64                 `json:"confidence"`           // Confidence between 0 and 1.
	Reasoning  string                  `json:"reasoning"`            // Explanation for the score.
	Dimensions []domain.DimensionScore `json:"dimensions,omitempty"` // Per-dimension scores.
}

// ValidateAndRepairScore validates and optionally repairs LLM scoring responses.
// It performs progressive validation with one-shot repair capabilities including
// JSON syntax fixing, value clamping, and content extraction from markdown.
func ValidateAndRepairScore(content string, enableRepair bool) (*ScoreData, error) {
	// First attempt: try to parse as-is.
	var score ScoreData
	if err := json.Unmarshal([]byte(content), &score); err == nil {
		if err := validateScoreData(&score); err == nil {
			return &score, nil
		}
		// Data parsed but validation failed, try repair.
		if enableRepair {
			if repaired := repairScoreData(&score); repaired != nil {
				return repaired, nil
			}
		}
	}

	// Second attempt: repair JSON if enabled.
	if enableRepair {
		if result := tryParseRepairedJSON(content); result != nil {
			return result, nil
		}
	}

	// Third attempt: extract JSON from markdown or text.
	if result := tryParseExtractedJSON(content, enableRepair); result != nil {
		return result, nil
	}

	// All attempts failed.
	return nil, &ValidationError{
		Message: "invalid score response format",
		Value:   content,
		Schema:  "ScoreData{value:float64, confidence:float64, reasoning:string}",
	}
}

// validateScoreData performs comprehensive validation of ScoreData fields.
// Checks value ranges, required fields, and dimension constraints to ensure
// scoring data meets quality standards for evaluation workflows.
func validateScoreData(score *ScoreData) error {
	if score.Value < 0 || score.Value > 1 {
		return fmt.Errorf("%w: got %f", ErrScoreValueOutOfRange, score.Value)
	}

	if score.Confidence < 0 || score.Confidence > 1 {
		return fmt.Errorf("%w: got %f", ErrConfidenceOutOfRange, score.Confidence)
	}

	if strings.TrimSpace(score.Reasoning) == "" {
		return ErrReasoningEmpty
	}

	for i, dim := range score.Dimensions {
		if dim.Value < 0 || dim.Value > 1 {
			return fmt.Errorf("%w at index %d: got %f", ErrDimensionValueOutOfRange, i, dim.Value)
		}
		if dim.Name == "" {
			return fmt.Errorf("%w at index %d", ErrDimensionNameEmpty, i)
		}
	}

	return nil
}

// repairScoreData applies intelligent fixes to malformed ScoreData.
// Handles percentage conversion, value clamping, and default reasoning
// to recover valid scoring data from LLM responses with common errors.
func repairScoreData(score *ScoreData) *ScoreData {
	repaired := *score
	modified := false

	// Clamp value to [0, 1].
	if repaired.Value < 0 {
		repaired.Value = 0
		modified = true
	} else if repaired.Value > 1 {
		// Check if it's a percentage (e.g., 85 instead of 0.85).
		if repaired.Value <= PercentageConversionFactor {
			repaired.Value /= 100
			modified = true
		} else {
			repaired.Value = 1
			modified = true
		}
	}

	// Clamp confidence to [0, 1].
	if repaired.Confidence < 0 {
		repaired.Confidence = 0
		modified = true
	} else if repaired.Confidence > 1 {
		// Check if it's a percentage.
		if repaired.Confidence <= PercentageConversionFactor {
			repaired.Confidence /= 100
			modified = true
		} else {
			repaired.Confidence = 1
			modified = true
		}
	}

	// Default reasoning if empty.
	if strings.TrimSpace(repaired.Reasoning) == "" {
		repaired.Reasoning = "No reasoning provided"
		modified = true
	}

	// Repair dimensions.
	for i := range repaired.Dimensions {
		if repaired.Dimensions[i].Value < 0 {
			repaired.Dimensions[i].Value = 0
			modified = true
		} else if repaired.Dimensions[i].Value > 1 {
			if repaired.Dimensions[i].Value <= PercentageConversionFactor {
				repaired.Dimensions[i].Value /= 100
				modified = true
			} else {
				repaired.Dimensions[i].Value = 1
				modified = true
			}
		}
	}

	if modified {
		return &repaired
	}
	return nil
}

// repairJSON fixes common JSON syntax errors in LLM responses.
// Handles trailing commas, missing brackets, unquoted keys, and quote
// formatting to recover valid JSON from malformed LLM output.
func repairJSON(content string) string {
	repaired := content

	// Remove trailing commas before closing braces/brackets.
	repaired = regexp.MustCompile(`,\s*([}\]])`).ReplaceAllString(repaired, "$1")

	// Add missing closing braces/brackets.
	openBraces := strings.Count(repaired, "{") - strings.Count(repaired, "}")
	openBrackets := strings.Count(repaired, "[") - strings.Count(repaired, "]")

	for i := 0; i < openBraces; i++ {
		repaired += "}"
	}
	for i := 0; i < openBrackets; i++ {
		repaired += "]"
	}

	// Fix unquoted keys (simple cases).
	repaired = regexp.MustCompile(`(\{|,)\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*:`).ReplaceAllString(repaired, `$1"$2":`)

	// Fix single quotes to double quotes.
	// This is risky but often helps with LLM responses.
	if !strings.Contains(repaired, `"`) && strings.Contains(repaired, `'`) {
		repaired = strings.ReplaceAll(repaired, `'`, `"`)
	}

	repaired = strings.TrimPrefix(repaired, "\ufeff")

	repaired = strings.TrimSpace(repaired)

	return repaired
}

// extractJSON extracts JSON objects from markdown or mixed text content.
// Searches for JSON in code blocks, inline code, and text patterns
// to handle LLM responses that embed JSON in conversational formats.
func extractJSON(content string) string {
	// Look for JSON in markdown code blocks.
	patterns := []string{
		"```json\n(.*?)\n```",
		"```\n(.*?)\n```",
		"`(\\{.*?\\})`",
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if matches := re.FindStringSubmatch(content); len(matches) > 1 {
			return matches[1]
		}
	}

	// Look for JSON object in the content.
	// Find the first { and last } and extract.
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")

	if start != -1 && end != -1 && end > start {
		return content[start : end+1]
	}

	return content
}

// ValidateGenerationResponse validates LLM answer generation responses.
// Performs content presence checks and error pattern detection while
// allowing refusal responses to pass through for proper handling.
func ValidateGenerationResponse(content string) error {
	if strings.TrimSpace(content) == "" {
		return &ValidationError{
			Message: "empty generation response",
		}
	}

	// Check for common error patterns.
	lowerContent := strings.ToLower(content)
	errorPatterns := []string{
		"i cannot",
		"i can't",
		"i am unable",
		"i'm unable",
		"as an ai",
		"as a language model",
		"error:",
		"exception:",
	}

	for _, pattern := range errorPatterns {
		if strings.Contains(lowerContent, pattern) {
			// This might be a refusal or error response.
			// Log it but don't fail validation.
			// The content will be stored and scored accordingly.
			break
		}
	}

	return nil
}

// ValidateProviderResponse validates LLM provider response structure.
// Checks response completeness, content validity, and token count consistency
// to ensure provider responses meet minimum quality requirements.
func ValidateProviderResponse(resp *LLMResponse) error {
	if resp == nil {
		return ErrNilResponse
	}

	// Check for empty content (might be valid for some operations).
	if resp.Content == "" && resp.FinishReason != domain.FinishToolUse {
		// Empty content is only valid for tool use responses.
		return ErrEmptyResponseContent
	}

	if resp.Usage.TotalTokens < 0 {
		return ErrNegativeTokenCount
	}

	// Check for consistency in token counts.
	if resp.Usage.TotalTokens > 0 {
		expectedTotal := resp.Usage.PromptTokens + resp.Usage.CompletionTokens
		if expectedTotal > 0 && resp.Usage.TotalTokens != expectedTotal {
			// Log warning but don't fail.
			// Some providers might include other tokens in total.
		}
	}

	return nil
}

// RepairConfig controls JSON repair behavior for LLM response processing.
// Configures repair strategy limits and feature toggles to balance
// repair success rates with processing overhead and safety.
type RepairConfig struct {
	MaxAttempts      int  // Maximum repair attempts.
	EnableExtraction bool // Enable extraction from markdown.
	EnableClamping   bool // Enable value clamping.
	EnableDefaults   bool // Enable default value filling.
}

// DefaultRepairConfig provides conservative repair settings for production use.
// Enables one-shot repair with all repair features to maximize compatibility
// with LLM response variations while maintaining safety boundaries.
func DefaultRepairConfig() *RepairConfig {
	return &RepairConfig{
		MaxAttempts:      1, // One-shot repair as per requirements.
		EnableExtraction: true,
		EnableClamping:   true,
		EnableDefaults:   true,
	}
}

// tryParseRepairedJSON attempts to parse JSON after applying repairs.
// Returns parsed and validated ScoreData if successful, nil otherwise.
func tryParseRepairedJSON(content string) *ScoreData {
	repairedJSON := repairJSON(content)
	if repairedJSON == content {
		return nil
	}

	var score ScoreData
	if err := json.Unmarshal([]byte(repairedJSON), &score); err != nil {
		return nil
	}

	if err := validateScoreData(&score); err == nil {
		return &score
	}

	return repairScoreData(&score)
}

// tryParseExtractedJSON attempts to parse JSON extracted from markdown or text.
// Returns parsed and validated ScoreData if successful, nil otherwise.
func tryParseExtractedJSON(content string, enableRepair bool) *ScoreData {
	extracted := extractJSON(content)
	if extracted == "" || extracted == content {
		return nil
	}

	var score ScoreData
	if err := json.Unmarshal([]byte(extracted), &score); err != nil {
		return nil
	}

	if err := validateScoreData(&score); err == nil {
		return &score
	}

	if !enableRepair {
		return nil
	}

	return repairScoreData(&score)
}
