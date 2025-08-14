// Package scoring implements validation and processing for LLM judge responses.
// Provides schema validation, JSON repair, and business rule enforcement
// for scoring workflows in temporal activities.
package scoring

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"go.temporal.io/sdk/temporal"

	"github.com/ahrav/go-judgy/internal/domain"
)

// minReasoningLength defines the minimum character count for reasoning text.
const minReasoningLength = 10

// ScoreSchema defines the expected JSON structure for LLM judge responses.
// This schema enforces strict validation with business rules to ensure
// consistent and reliable scoring data across all judge models.
type ScoreSchema struct {
	// Score represents the normalized evaluation result (0.0-1.0).
	// Required field with strict range validation.
	Score float64 `json:"score" validate:"required,min=0,max=1"`

	// Reasoning provides the judge's explanation for the score.
	// Required field with minimum length to ensure meaningful explanations.
	Reasoning string `json:"reasoning" validate:"required,min=10"`

	// Confidence indicates the judge's confidence in the score (0.0-1.0).
	// Optional field - if not provided, defaults to reasonable value.
	Confidence *float64 `json:"confidence,omitempty"`

	// Metadata contains additional scoring context and dimensions.
	// Optional field for extensibility without breaking schema compatibility.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// scoringValidator implements domain.ScoreValidator for business-level validation.
// Encapsulates scoring schema validation, repair logic, and business rule enforcement
// while maintaining clean separation from transport-level concerns.
type scoringValidator struct {
	schemaVersion string
	blobThreshold int
}

// NewScoringValidator creates a new validator for scoring responses.
// The validator encapsulates schema version and business rules,
// providing pure, deterministic validation suitable for dependency injection.
//
// Parameters:
//
//	schemaVersion: Version identifier for metrics and debugging
//	blobThreshold: Used for size-based validation rules (not for storage decisions)
//
// Returns:
//
//	domain.ScoreValidator: Interface for clean dependency injection
func NewScoringValidator(schemaVersion string, blobThreshold int) domain.ScoreValidator {
	return &scoringValidator{
		schemaVersion: schemaVersion,
		blobThreshold: blobThreshold,
	}
}

// Validate implements domain.ScoreValidator with one-shot repair policy.
// Performs strict JSON validation followed by a single repair attempt
// if the initial validation fails. This implements Story 1.5's requirement
// for "one-shot repair policy" with non-retryable errors after repair failure.
//
// Validation Process:
// 1. Attempt strict JSON parsing and business rule validation
// 2. If invalid, apply one repair attempt with common fixes
// 3. Re-validate repaired content
// 4. Return non-retryable error if still invalid
//
// Parameters:
//
//	raw: Raw JSON bytes from LLM response
//
// Returns:
//
//	normalized: Valid JSON bytes (original or repaired)
//	repaired: true if repair was applied, false if original was valid
//	err: non-nil if validation failed after repair attempt
func (v *scoringValidator) Validate(raw []byte) ([]byte, bool, error) {
	// First attempt: strict JSON validation
	var schema ScoreSchema
	if err := json.Unmarshal(raw, &schema); err == nil {
		if err := v.validateSchema(&schema); err == nil {
			// Valid without repair.
			return raw, false, nil
		}
		// JSON parsed but business validation failed - skip repair for schema violations.
		return nil, false, nonRetryable("scoring schema validation", err, "business rule violation")
	}

	// Second attempt: one-shot repair
	repairedJSON := v.repairCommonJSONIssues(string(raw))
	if repairedJSON == string(raw) {
		// No repair applied, return original error.
		return nil, false, nonRetryable("scoring schema parsing", fmt.Errorf("JSON parse failed"), "malformed JSON")
	}

	// Try parsing repaired JSON
	var repairedSchema ScoreSchema
	if err := json.Unmarshal([]byte(repairedJSON), &repairedSchema); err != nil {
		// Repair didn't fix JSON parsing.
		return nil, false, nonRetryable("scoring schema repair", err, "JSON still invalid after repair")
	}

	// Validate repaired schema
	if err := v.validateSchema(&repairedSchema); err != nil {
		// Repair fixed JSON but business rules still violated.
		return nil, false, nonRetryable("scoring schema validation", err, "business rules violated after repair")
	}

	// Successfully repaired and validated.
	return []byte(repairedJSON), true, nil
}

// validateSchema performs business rule validation on parsed schema.
// Enforces scoring-specific constraints beyond basic JSON structure.
func (v *scoringValidator) validateSchema(schema *ScoreSchema) error {
	// Validate score range
	if schema.Score < 0 || schema.Score > 1 {
		return fmt.Errorf("score value %f outside valid range [0,1]", schema.Score)
	}

	// Validate reasoning length
	reasoning := strings.TrimSpace(schema.Reasoning)
	if len(reasoning) < minReasoningLength {
		return fmt.Errorf("reasoning too short: %d characters (minimum %d)", len(reasoning), minReasoningLength)
	}

	// Validate confidence if provided
	if schema.Confidence != nil {
		if *schema.Confidence < 0 || *schema.Confidence > 1 {
			return fmt.Errorf("confidence value %f outside valid range [0,1]", *schema.Confidence)
		}
	}

	return nil
}

// repairCommonJSONIssues applies one-shot repair for typical LLM output problems.
// Implements conservative repairs to avoid false positives while recovering
// from common formatting issues in LLM responses.
//
// Repair Strategy:
// - Remove markdown code fences
// - Fix trailing commas
// - Basic quote normalization
// - Trim whitespace
//
// Returns original string if no repairs are applicable.
func (v *scoringValidator) repairCommonJSONIssues(jsonStr string) string {
	original := jsonStr
	repaired := jsonStr

	// Remove markdown code fences that LLMs often wrap around JSON responses.
	repaired = strings.TrimPrefix(repaired, "```json")
	repaired = strings.TrimPrefix(repaired, "```")
	repaired = strings.TrimSuffix(repaired, "```")

	// Fix trailing commas that violate JSON spec but are common in LLM outputs.
	// Handle common cases: comma before closing brace with optional whitespace/newlines.
	repaired = strings.ReplaceAll(repaired, ",\n}", "\n}")     // Common case: comma + newline + brace
	repaired = strings.ReplaceAll(repaired, ",\r\n}", "\r\n}") // Windows line endings
	repaired = strings.ReplaceAll(repaired, ", }", " }")       // Space separated
	repaired = strings.ReplaceAll(repaired, ",}", "}")         // Direct case

	// Fix unquoted property names which are invalid JSON but common in LLM responses.
	unquotedKeyRegex := regexp.MustCompile(`(\{|,)\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*:`)
	repaired = unquotedKeyRegex.ReplaceAllString(repaired, `$1"$2":`)

	// Normalize single quotes to double quotes when safe to avoid quote conflicts.
	if !strings.Contains(repaired, `"`) && strings.Contains(repaired, `'`) {
		repaired = strings.ReplaceAll(repaired, `'`, `"`)
	}

	repaired = strings.TrimSpace(repaired)

	if repaired == original {
		return original
	}

	return repaired
}

// Error helpers for proper Temporal error classification

func nonRetryable(tag string, cause error, msg string) error {
	return temporal.NewNonRetryableApplicationError(msg, tag, cause)
}
