package transport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// CurrentCanonicalVersion defines the canonicalization format version.
// Increment when canonicalization logic changes to invalidate stale cache entries.
// Version changes force cache invalidation to prevent hash collisions between formats.
const CurrentCanonicalVersion = "v1.0"

// Validation errors for canonical payloads.
var (
	ErrTenantIDRequired  = errors.New("tenant_id is required")
	ErrOperationRequired = errors.New("operation is required")
	ErrProviderRequired  = errors.New("provider is required")
	ErrModelRequired     = errors.New("model is required")
	ErrVersionRequired   = errors.New("version is required")
	ErrInvalidOperation  = errors.New("invalid operation")
	ErrInvalidProvider   = errors.New("invalid provider")
)

// CanonicalPayload represents the normalized, stable form of a logical LLM request.
// It serves as the sole input to IdemKey hashing and MUST be deterministic across
// equivalent requests regardless of input variation. All fields undergo normalization
// to ensure identical requests produce identical keys for proper cache deduplication.
type CanonicalPayload struct {
	TenantID   string             `json:"tenant_id"`            // Required tenant identifier
	Operation  OperationType      `json:"operation"`            // generation|scoring
	Provider   string             `json:"provider"`             // openai|anthropic|google
	Model      string             `json:"model"`                // Model identifier
	System     string             `json:"system,omitempty"`     // Normalized system prompt
	Messages   []CanonicalMessage `json:"messages,omitempty"`   // Normalized messages
	Tools      []CanonicalTool    `json:"tools,omitempty"`      // Normalized tools
	Params     map[string]any     `json:"params,omitempty"`     // Normalized parameters
	Seed       *int64             `json:"seed,omitempty"`       // Optional seed for determinism
	Extensions map[string]any     `json:"extensions,omitempty"` // Provider-specific extensions
	Version    string             `json:"version"`              // Canonicalization version
}

// CanonicalMessage represents a normalized message in the conversation.
type CanonicalMessage struct {
	Role    string `json:"role"`    // system|user|assistant|tool
	Content string `json:"content"` // Normalized content
}

// CanonicalTool represents a normalized tool definition.
type CanonicalTool struct {
	Name        string         `json:"name"`
	Description string         `json:"desc,omitempty"`
	JSONSchema  map[string]any `json:"json_schema,omitempty"`
}

// IdemKey provides deterministic SHA-256 hex identification for canonical payloads.
// Keys are computed from stable JSON serialization, ensuring equivalent requests
// generate identical keys for cache lookup and deduplication.
type IdemKey string

// BuildCanonicalPayload transforms an LLM request into normalized canonical form.
// The normalization pipeline ensures equivalent requests produce identical
// canonical representations by applying consistent text normalization,
// parameter filtering, and message structuring across all provider types.
func BuildCanonicalPayload(req *Request) (*CanonicalPayload, error) {
	payload := &CanonicalPayload{
		TenantID:  req.TenantID,
		Operation: req.Operation,
		Provider:  strings.ToLower(strings.TrimSpace(req.Provider)),
		Model:     strings.TrimSpace(req.Model),
		Version:   CurrentCanonicalVersion,
	}

	// Normalize system prompt for consistent hashing.
	if req.SystemPrompt != "" {
		payload.System = normalizeText(req.SystemPrompt)
	}

	// Build normalized messages for provider-agnostic canonicalization.
	messages := []CanonicalMessage{}

	// System prompt lives in top-level System field to ensure consistent
	// canonicalization across providers. Provider adapters format as needed.

	switch req.Operation {
	case OpGeneration:
		if req.Question != "" {
			messages = append(messages, CanonicalMessage{
				Role:    "user",
				Content: normalizeText(req.Question),
			})
		}
	case OpScoring:
		content := fmt.Sprintf("Question: %s", normalizeText(req.Question))
		if len(req.Answers) > 0 {
			// Include answer IDs for scoring idempotency - IDs ensure cache
			// consistency while content is stored externally in artifact store.
			answerIDs := make([]string, len(req.Answers))
			for i, ans := range req.Answers {
				answerIDs[i] = ans.ID
			}
			sort.Strings(answerIDs) // Ensure deterministic ordering for consistent hashing.
			content = fmt.Sprintf("%s\nAnswers: %s", content, strings.Join(answerIDs, ","))
		}
		messages = append(messages, CanonicalMessage{
			Role:    "user",
			Content: content,
		})
	}

	payload.Messages = messages

	params := make(map[string]any)

	// Include only non-default parameters to minimize cache key variations.
	if req.MaxTokens > 0 {
		params["max_tokens"] = req.MaxTokens
	}
	if req.Temperature != 0.0 {
		params["temperature"] = req.Temperature
	}

	if len(params) > 0 {
		payload.Params = params
	}

	payload.Seed = req.Seed

	// Provider-specific extensions could be added here.
	// For now, we don't have any.

	return payload, nil
}

// BuildIdemKey generates a deterministic SHA-256 idempotency key.
// Keys are computed from stable JSON serialization with sorted keys,
// ensuring identical payloads always produce identical keys.
func BuildIdemKey(payload *CanonicalPayload) (IdemKey, error) {
	jsonBytes, err := stableJSON(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal canonical payload: %w", err)
	}

	hash := sha256.Sum256(jsonBytes)
	key := hex.EncodeToString(hash[:])

	return IdemKey(key), nil
}

// GenerateIdemKey builds canonical payload and generates the idempotency key.
// This convenience function combines payload normalization and key generation
// for direct use from LLM requests.
func GenerateIdemKey(req *Request) (IdemKey, error) {
	payload, err := BuildCanonicalPayload(req)
	if err != nil {
		return "", fmt.Errorf("failed to build canonical payload: %w", err)
	}

	return BuildIdemKey(payload)
}

// String returns the string representation of the idempotency key.
func (k IdemKey) String() string { return string(k) }

// normalizeText normalizes text content for consistent hash generation.
// Applies whitespace trimming, line ending normalization, and space collapsing
// to ensure equivalent text produces identical normalized output.
func normalizeText(text string) string {
	// Trim whitespace for consistent boundaries.
	text = strings.TrimSpace(text)

	// Normalize CRLF to LF for cross-platform consistency.
	text = strings.ReplaceAll(text, "\r\n", "\n")

	// Collapse spaces to eliminate formatting variations.
	text = collapseSpaces(text)

	return text
}

// collapseSpaces reduces multiple consecutive spaces to a single space.
func collapseSpaces(s string) string {
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}

// stableJSON produces deterministic JSON output with sorted keys.
// Ensures equivalent objects produce identical JSON regardless of
// field insertion order, enabling reliable cache key generation.
func stableJSON(v any) ([]byte, error) {
	// Use json.Marshal which handles struct tags and produces stable output
	// for our specific types. For maps, we need to ensure key ordering.

	// Initial marshal to normalize struct field ordering.
	tempJSON, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	// Parse back to normalize map key ordering.
	var normalized any
	if err := json.Unmarshal(tempJSON, &normalized); err != nil {
		return nil, err
	}

	// Re-marshal with deterministic key sorting.
	return json.Marshal(sortKeys(normalized))
}

// sortKeys recursively sorts map keys for stable JSON output.
func sortKeys(v any) any {
	switch v := v.(type) {
	case map[string]any:
		// Extract and sort map keys for deterministic JSON.
		sorted := make(map[string]any)
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			sorted[k] = sortKeys(v[k]) // Recursively sort nested maps and arrays.
		}
		return sorted

	case []any:
		// Recursively sort array elements to handle nested structures.
		sorted := make([]any, len(v))
		for i, elem := range v {
			sorted[i] = sortKeys(elem)
		}
		return sorted

	default:
		// Primitives don't require sorting.
		return v
	}
}

// ValidateCanonicalPayload validates canonical payload completeness and correctness.
// Ensures all required fields are present and values are within acceptable ranges
// for successful idempotency key generation and provider routing.
func ValidateCanonicalPayload(payload *CanonicalPayload) error {
	if payload.TenantID == "" {
		return ErrTenantIDRequired
	}
	if payload.Operation == "" {
		return ErrOperationRequired
	}
	if payload.Provider == "" {
		return ErrProviderRequired
	}
	if payload.Model == "" {
		return ErrModelRequired
	}
	if payload.Version == "" {
		return ErrVersionRequired
	}

	// Validate operation type against known operations.
	switch payload.Operation {
	case OpGeneration, OpScoring:
		// Valid operations.
	default:
		return fmt.Errorf("%w: %s", ErrInvalidOperation, payload.Operation)
	}

	// Validate provider against supported providers.
	switch payload.Provider {
	case "openai", "anthropic", "google":
		// Supported provider.
	default:
		return fmt.Errorf("%w: %s", ErrInvalidProvider, payload.Provider)
	}

	return nil
}

// ArePayloadsEquivalent determines if two payloads generate identical IdemKeys.
// Useful for testing canonicalization logic and debugging cache behavior
// by comparing the idempotency keys of potentially equivalent payloads.
func ArePayloadsEquivalent(p1, p2 *CanonicalPayload) (bool, error) {
	key1, err := BuildIdemKey(p1)
	if err != nil {
		return false, fmt.Errorf("failed to build key for p1: %w", err)
	}

	key2, err := BuildIdemKey(p2)
	if err != nil {
		return false, fmt.Errorf("failed to build key for p2: %w", err)
	}

	return key1 == key2, nil
}

// BuildCanonicalPayloadFromGenerate builds a canonical payload from generation input.
// This is used for fallback idempotency when the client doesn't provide an idem key.
// It mirrors the exact canonicalization that would be done by the LLM client.
func BuildCanonicalPayloadFromGenerate(
	tenantID string,
	question string,
	provider string,
	model string,
	systemPrompt string,
	maxTokens int,
	temperature float64,
) (*CanonicalPayload, error) {
	payload := &CanonicalPayload{
		TenantID:  tenantID,
		Operation: OpGeneration,
		Provider:  strings.ToLower(strings.TrimSpace(provider)),
		Model:     strings.TrimSpace(model),
		Version:   CurrentCanonicalVersion,
	}

	// Normalize system prompt for consistent hashing.
	if systemPrompt != "" {
		payload.System = normalizeText(systemPrompt)
	}

	// Build normalized messages for generation.
	messages := []CanonicalMessage{}
	if question != "" {
		messages = append(messages, CanonicalMessage{
			Role:    "user",
			Content: normalizeText(question),
		})
	}
	payload.Messages = messages

	params := make(map[string]any)
	// Include only non-default parameters to minimize cache key variations.
	if maxTokens > 0 {
		params["max_tokens"] = maxTokens
	}
	if temperature != 0.0 {
		params["temperature"] = temperature
	}

	if len(params) > 0 {
		payload.Params = params
	}

	return payload, nil
}

// HashCanonicalPayload generates a SHA-256 hash for a canonical payload.
// This provides a deterministic idempotency key for fallback scenarios.
func HashCanonicalPayload(payload *CanonicalPayload) string {
	key, err := BuildIdemKey(payload)
	if err != nil {
		// In case of error, return a simple hash of the tenant and operation
		// This ensures we always have some form of idempotency.
		fallback := fmt.Sprintf("%s:%s", payload.TenantID, payload.Operation)
		hash := sha256.Sum256([]byte(fallback))
		return hex.EncodeToString(hash[:])
	}
	return string(key)
}

// IdempotentCacheEntry is the persisted result keyed by IdemKey (success-only).
type IdempotentCacheEntry struct {
	Provider            string            `json:"provider"`
	Model               string            `json:"model"`
	RawResponse         []byte            `json:"raw_response"` // Exact provider JSON
	ResponseHeaders     map[string]string `json:"response_headers"`
	Usage               NormalizedUsage   `json:"usage"`
	EstimatedMilliCents int64             `json:"estimated_milli_cents"`
	StoredAtUnixMs      int64             `json:"stored_at_ms"`
}

// CacheKey constructs the complete Redis cache key.
// Uses hierarchical format llm:{tenant}:{operation}:{idemkey} to enable
// efficient tenant isolation and operation-specific cache management.
func CacheKey(tenantID string, operation OperationType, idemKey IdemKey) string {
	return fmt.Sprintf("llm:%s:%s:%s", tenantID, operation, idemKey)
}
