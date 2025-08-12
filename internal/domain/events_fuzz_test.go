package domain

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

func FuzzGenerateIdempotencyKey(f *testing.F) {
	// Seed corpus with edge cases
	f.Add("", "")
	f.Add("normal-key", ":cand:0")
	f.Add("client-key-123", ":generate:1")
	f.Add("key-with-special-chars!@#$%^&*()", ":suffix:999")
	f.Add("very-long-"+string(make([]byte, 1000)), ":short")
	f.Add("short", ":very-long-"+string(make([]byte, 1000)))
	f.Add("unicode-你好", ":cand:1")
	f.Add("key\x00with\x00nulls", ":suffix\x00with\x00nulls")
	f.Add("key\nwith\nlines", ":suffix\nwith\nlines")
	f.Add("key\twith\ttabs", ":suffix\twith\ttabs")

	f.Fuzz(func(t *testing.T, clientKey, suffix string) {
		// Function should never panic
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("GenerateIdempotencyKey panicked with clientKey=%q, suffix=%q: %v", clientKey, suffix, r)
			}
		}()

		key := GenerateIdempotencyKey(clientKey, suffix)

		// Key should always be non-empty (even for empty inputs)
		if key == "" {
			t.Errorf("GenerateIdempotencyKey returned empty key for clientKey=%q, suffix=%q", clientKey, suffix)
		}

		// Key should always be 64 characters (SHA256 hex)
		if len(key) != 64 {
			t.Errorf("GenerateIdempotencyKey returned key of length %d, expected 64 for clientKey=%q, suffix=%q", len(key), clientKey, suffix)
		}

		// Key should be valid hex
		for i, r := range key {
			if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
				t.Errorf("GenerateIdempotencyKey returned invalid hex character %c at position %d for clientKey=%q, suffix=%q", r, i, clientKey, suffix)
				break
			}
		}

		// Same inputs should produce same outputs (deterministic)
		key2 := GenerateIdempotencyKey(clientKey, suffix)
		if key != key2 {
			t.Errorf("GenerateIdempotencyKey not deterministic: first=%q, second=%q for clientKey=%q, suffix=%q", key, key2, clientKey, suffix)
		}

		// Different inputs should produce different outputs (with high probability)
		if clientKey != "" || suffix != "" {
			keyDiff := GenerateIdempotencyKey(clientKey+"x", suffix)
			if key == keyDiff {
				t.Logf("Hash collision detected (very unlikely): clientKey=%q, suffix=%q", clientKey, suffix)
			}
		}
	})
}

func FuzzCandidateProducedIdempotencyKey(f *testing.F) {
	// Seed corpus with edge cases
	f.Add("", 0)
	f.Add("normal-key", 1)
	f.Add("client-key-123", -1)
	f.Add("special-chars!@#$", 999999)
	f.Add("unicode-你好", 42)
	f.Add(string(make([]byte, 10000)), 0) // Very long key
	f.Add("key\x00with\x00nulls", 1)
	f.Add("key\nwith\nlines", 2)

	f.Fuzz(func(t *testing.T, clientKey string, index int) {
		// Function should never panic
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("CandidateProducedIdempotencyKey panicked with clientKey=%q, index=%d: %v", clientKey, index, r)
			}
		}()

		key := CandidateProducedIdempotencyKey(clientKey, index)

		// Key should always be non-empty
		if key == "" {
			t.Errorf("CandidateProducedIdempotencyKey returned empty key for clientKey=%q, index=%d", clientKey, index)
		}

		// Key should always be 64 characters (SHA256 hex)
		if len(key) != 64 {
			t.Errorf("CandidateProducedIdempotencyKey returned key of length %d, expected 64 for clientKey=%q, index=%d", len(key), clientKey, index)
		}

		// Key should be valid hex
		for i, r := range key {
			if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
				t.Errorf("CandidateProducedIdempotencyKey returned invalid hex character %c at position %d for clientKey=%q, index=%d", r, i, clientKey, index)
				break
			}
		}

		// Should be deterministic
		key2 := CandidateProducedIdempotencyKey(clientKey, index)
		if key != key2 {
			t.Errorf("CandidateProducedIdempotencyKey not deterministic: first=%q, second=%q for clientKey=%q, index=%d", key, key2, clientKey, index)
		}

		// Should match pattern from GenerateIdempotencyKey
		expectedSuffix := fmt.Sprintf(":cand:%d", index)
		expectedKey := GenerateIdempotencyKey(clientKey, expectedSuffix)
		if key != expectedKey {
			t.Errorf("CandidateProducedIdempotencyKey=%q does not match GenerateIdempotencyKey=%q for clientKey=%q, index=%d", key, expectedKey, clientKey, index)
		}

		// Different indices should produce different keys (with high probability)
		if index != index+1 { // Avoid integer overflow edge case
			keyDiff := CandidateProducedIdempotencyKey(clientKey, index+1)
			if key == keyDiff {
				t.Logf("Hash collision detected (very unlikely) for consecutive indices: clientKey=%q, index=%d", clientKey, index)
			}
		}
	})
}

func FuzzLLMUsageIdempotencyKey(f *testing.F) {
	// Seed corpus with edge cases
	f.Add("")
	f.Add("normal-key")
	f.Add("client-key-123")
	f.Add("special-chars!@#$%^&*()")
	f.Add("unicode-你好世界")
	f.Add(string(make([]byte, 10000))) // Very long key
	f.Add("key\x00with\x00nulls")
	f.Add("key\nwith\nlines")
	f.Add("key\twith\ttabs")
	f.Add("key with spaces")

	f.Fuzz(func(t *testing.T, clientKey string) {
		// Function should never panic
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("LLMUsageIdempotencyKey panicked with clientKey=%q: %v", clientKey, r)
			}
		}()

		key := LLMUsageIdempotencyKey(clientKey)

		// Key should always be non-empty
		if key == "" {
			t.Errorf("LLMUsageIdempotencyKey returned empty key for clientKey=%q", clientKey)
		}

		// Key should always be 64 characters (SHA256 hex)
		if len(key) != 64 {
			t.Errorf("LLMUsageIdempotencyKey returned key of length %d, expected 64 for clientKey=%q", len(key), clientKey)
		}

		// Key should be valid hex
		for i, r := range key {
			if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
				t.Errorf("LLMUsageIdempotencyKey returned invalid hex character %c at position %d for clientKey=%q", r, i, clientKey)
				break
			}
		}

		// Should be deterministic
		key2 := LLMUsageIdempotencyKey(clientKey)
		if key != key2 {
			t.Errorf("LLMUsageIdempotencyKey not deterministic: first=%q, second=%q for clientKey=%q", key, key2, clientKey)
		}

		// Should match pattern from GenerateIdempotencyKey
		expectedKey := GenerateIdempotencyKey(clientKey, ":generate:1")
		if key != expectedKey {
			t.Errorf("LLMUsageIdempotencyKey=%q does not match GenerateIdempotencyKey=%q for clientKey=%q", key, expectedKey, clientKey)
		}
	})
}

func FuzzNewCandidateProducedEvent(f *testing.F) {
	// Seed corpus with valid and edge case JSON payloads
	validUUID := uuid.New().String()
	f.Add(validUUID, "openai", "gpt-4", int64(100), int64(50), int64(75), int64(125), "stop")
	f.Add("", "openai", "gpt-4", int64(100), int64(50), int64(75), int64(125), "stop")                                                    // Invalid UUID
	f.Add(validUUID, "", "gpt-4", int64(100), int64(50), int64(75), int64(125), "stop")                                                   // Empty provider
	f.Add(validUUID, "openai", "", int64(100), int64(50), int64(75), int64(125), "stop")                                                  // Empty model
	f.Add(validUUID, "openai", "gpt-4", int64(-1), int64(50), int64(75), int64(125), "stop")                                              // Negative latency
	f.Add(validUUID, "openai", "gpt-4", int64(100), int64(-1), int64(75), int64(125), "stop")                                             // Negative prompt tokens
	f.Add(validUUID, "openai", "gpt-4", int64(100), int64(50), int64(-1), int64(125), "stop")                                             // Negative completion tokens
	f.Add(validUUID, "openai", "gpt-4", int64(100), int64(50), int64(75), int64(-1), "stop")                                              // Negative total tokens
	f.Add(validUUID, "openai", "gpt-4", int64(0), int64(0), int64(0), int64(0), "")                                                       // Zero values
	f.Add(validUUID, "provider\x00with\x00nulls", "model\nwith\nlines", int64(9223372036854775807), int64(0), int64(0), int64(0), "stop") // Edge cases

	f.Fuzz(func(t *testing.T, answerID, provider, model string, latencyMs, promptTokens, completionTokens, totalTokens int64, finishReason string) {
		// Function should never panic
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("NewCandidateProducedEvent panicked: %v", r)
			}
		}()

		// Create answer with fuzzed values
		answer := Answer{
			ID: answerID,
			ContentRef: ArtifactRef{
				Key:  "answers/fuzz-test.txt",
				Size: 100,
				Kind: ArtifactAnswer,
			},
			AnswerProvenance: AnswerProvenance{
				Provider:    provider,
				Model:       model,
				GeneratedAt: time.Now(),
			},
			AnswerUsage: AnswerUsage{
				LatencyMillis:    latencyMs,
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				TotalTokens:      totalTokens,
			},
			AnswerState: AnswerState{
				FinishReason: FinishReason(finishReason),
			},
		}

		tenantID := uuid.New()
		envelope, err := NewCandidateProducedEvent(
			tenantID,
			"workflow-123",
			"run-456",
			answer,
			"client-key-123",
			0,
		)

		if err != nil {
			// Errors are expected for invalid inputs
			// Verify error message contains useful information
			if err.Error() == "" {
				t.Errorf("NewCandidateProducedEvent returned empty error message")
			}
			return
		}

		// If no error, verify the envelope is valid
		if envelope.IdempotencyKey == "" {
			t.Errorf("NewCandidateProducedEvent created envelope with empty IdempotencyKey")
		}

		if len(envelope.IdempotencyKey) != 64 {
			t.Errorf("NewCandidateProducedEvent created envelope with IdempotencyKey length %d, expected 64", len(envelope.IdempotencyKey))
		}

		// Payload should be valid JSON
		var payload CandidateProducedPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			t.Errorf("NewCandidateProducedEvent created envelope with invalid JSON payload: %v", err)
		}

		// Envelope validation should pass if creation succeeded
		if err := envelope.Validate(); err != nil {
			t.Errorf("NewCandidateProducedEvent created invalid envelope: %v", err)
		}
	})
}

func FuzzNewLLMUsageEvent(f *testing.F) {
	// Seed corpus with valid and edge case values
	f.Add(int64(100), int64(5), int64(150), "openai", "gpt-4", true, "client-key")
	f.Add(int64(0), int64(0), int64(0), "openai", "gpt-4", false, "client-key")
	f.Add(int64(-1), int64(5), int64(150), "openai", "gpt-4", false, "client-key")                                         // Negative tokens
	f.Add(int64(100), int64(-1), int64(150), "openai", "gpt-4", false, "client-key")                                       // Negative calls
	f.Add(int64(100), int64(5), int64(-1), "openai", "gpt-4", false, "client-key")                                         // Negative cost
	f.Add(int64(100), int64(5), int64(150), "", "gpt-4", false, "client-key")                                              // Empty provider
	f.Add(int64(100), int64(5), int64(150), "openai", "", false, "client-key")                                             // Empty model
	f.Add(int64(9223372036854775807), int64(9223372036854775807), int64(9223372036854775807), "openai", "gpt-4", true, "") // Max values
	f.Add(int64(100), int64(5), int64(150), "provider\x00with\x00nulls", "model\nwith\nlines", false, "key\twith\ttabs")

	f.Fuzz(func(t *testing.T, totalTokens, totalCalls, costCents int64, provider, model string, cacheHit bool, clientKey string) {
		// Function should never panic
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("NewLLMUsageEvent panicked: %v", r)
			}
		}()

		// Create output with fuzzed values
		output := &GenerateAnswersOutput{
			TokensUsed: totalTokens,
			CallsMade:  totalCalls,
			CostCents:  Cents(costCents),
		}

		tenantID := uuid.New()
		envelope, err := NewLLMUsageEvent(
			tenantID,
			"workflow-123",
			"run-456",
			output,
			provider,
			[]string{model}, // Single model for simplicity
			[]string{"req-123"},
			cacheHit,
			clientKey,
			[]string{"artifact-1"},
		)

		if err != nil {
			// Errors are expected for invalid inputs
			// Verify error message contains useful information
			if err.Error() == "" {
				t.Errorf("NewLLMUsageEvent returned empty error message")
			}
			return
		}

		// If no error, verify the envelope is valid
		if envelope.IdempotencyKey == "" {
			t.Errorf("NewLLMUsageEvent created envelope with empty IdempotencyKey")
		}

		if len(envelope.IdempotencyKey) != 64 {
			t.Errorf("NewLLMUsageEvent created envelope with IdempotencyKey length %d, expected 64", len(envelope.IdempotencyKey))
		}

		// Payload should be valid JSON
		var payload LLMUsagePayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			t.Errorf("NewLLMUsageEvent created envelope with invalid JSON payload: %v", err)
		}

		// Envelope validation should pass if creation succeeded
		if err := envelope.Validate(); err != nil {
			t.Errorf("NewLLMUsageEvent created invalid envelope: %v", err)
		}

		// Verify payload data matches input
		if payload.TotalTokens != totalTokens {
			t.Errorf("NewLLMUsageEvent payload TotalTokens=%d, expected %d", payload.TotalTokens, totalTokens)
		}
		if payload.TotalCalls != totalCalls {
			t.Errorf("NewLLMUsageEvent payload TotalCalls=%d, expected %d", payload.TotalCalls, totalCalls)
		}
		if payload.CostCents != Cents(costCents) {
			t.Errorf("NewLLMUsageEvent payload CostCents=%d, expected %d", payload.CostCents, costCents)
		}
		if payload.Provider != provider {
			t.Errorf("NewLLMUsageEvent payload Provider=%q, expected %q", payload.Provider, provider)
		}
		if len(payload.Models) != 1 || payload.Models[0] != model {
			t.Errorf("NewLLMUsageEvent payload Models=%v, expected [%q]", payload.Models, model)
		}
		if payload.CacheHit != cacheHit {
			t.Errorf("NewLLMUsageEvent payload CacheHit=%v, expected %v", payload.CacheHit, cacheHit)
		}
	})
}

func FuzzEventEnvelope_Validate(f *testing.F) {
	validTenantID := uuid.New()
	validTime := time.Now()

	// Seed with valid and invalid envelopes
	f.Add("valid-key", "CandidateProduced", 1, validTime.Unix(), validTenantID.String(), "workflow-123", "run-456", 0, `{"test":"data"}`, "producer")
	f.Add("", "CandidateProduced", 1, validTime.Unix(), validTenantID.String(), "workflow-123", "run-456", 0, `{"test":"data"}`, "producer")                          // Empty key
	f.Add("valid-key", "", 1, validTime.Unix(), validTenantID.String(), "workflow-123", "run-456", 0, `{"test":"data"}`, "producer")                                  // Empty event type
	f.Add("valid-key", "CandidateProduced", 0, validTime.Unix(), validTenantID.String(), "workflow-123", "run-456", 0, `{"test":"data"}`, "producer")                 // Zero version
	f.Add("valid-key", "CandidateProduced", 1, int64(0), validTenantID.String(), "workflow-123", "run-456", 0, `{"test":"data"}`, "producer")                         // Zero time
	f.Add("valid-key", "CandidateProduced", 1, validTime.Unix(), "00000000-0000-0000-0000-000000000000", "workflow-123", "run-456", 0, `{"test":"data"}`, "producer") // Zero UUID
	f.Add("valid-key", "CandidateProduced", 1, validTime.Unix(), validTenantID.String(), "", "run-456", 0, `{"test":"data"}`, "producer")                             // Empty workflow
	f.Add("valid-key", "CandidateProduced", 1, validTime.Unix(), validTenantID.String(), "workflow-123", "", 0, `{"test":"data"}`, "producer")                        // Empty run
	f.Add("valid-key", "CandidateProduced", 1, validTime.Unix(), validTenantID.String(), "workflow-123", "run-456", -1, `{"test":"data"}`, "producer")                // Negative sequence
	f.Add("valid-key", "CandidateProduced", 1, validTime.Unix(), validTenantID.String(), "workflow-123", "run-456", 0, `{}`, "producer")                              // Minimal valid payload
	f.Add("valid-key", "CandidateProduced", 1, validTime.Unix(), validTenantID.String(), "workflow-123", "run-456", 0, `{"test":"data"}`, "")                         // Empty producer

	f.Fuzz(func(t *testing.T, idempotencyKey, eventType string, version int, occurredAtUnix int64, tenantIDStr, workflowID, runID string, sequence int, payloadJSON, producer string) {
		// Function should never panic
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("EventEnvelope.Validate panicked: %v", r)
			}
		}()

		// Parse tenant ID from string
		tenantID, _ := uuid.Parse(tenantIDStr)
		occurredAt := time.Unix(occurredAtUnix, 0)

		envelope := EventEnvelope{
			IdempotencyKey: idempotencyKey,
			EventType:      EventType(eventType),
			Version:        version,
			OccurredAt:     occurredAt,
			TenantID:       tenantID,
			WorkflowID:     workflowID,
			RunID:          runID,
			Sequence:       sequence,
			Payload:        json.RawMessage(payloadJSON),
			Producer:       producer,
		}

		err := envelope.Validate()

		// Validation should either succeed or return a meaningful error
		if err != nil {
			if err.Error() == "" {
				t.Errorf("EventEnvelope.Validate returned empty error message")
			}
		}

		// If validation succeeds, all required fields should be non-zero/non-empty
		if err == nil {
			if envelope.IdempotencyKey == "" {
				t.Errorf("Valid envelope has empty IdempotencyKey")
			}
			if envelope.EventType == "" {
				t.Errorf("Valid envelope has empty EventType")
			}
			if envelope.Version < 1 {
				t.Errorf("Valid envelope has Version=%d, expected >=1", envelope.Version)
			}
			if envelope.OccurredAt.IsZero() {
				t.Errorf("Valid envelope has zero OccurredAt")
			}
			if envelope.TenantID == (uuid.UUID{}) {
				t.Errorf("Valid envelope has zero TenantID")
			}
			if envelope.WorkflowID == "" {
				t.Errorf("Valid envelope has empty WorkflowID")
			}
			if envelope.RunID == "" {
				t.Errorf("Valid envelope has empty RunID")
			}
			if envelope.Sequence < 0 {
				t.Errorf("Valid envelope has Sequence=%d, expected >=0", envelope.Sequence)
			}
			// Skip payload check - empty payload might be valid JSON (e.g., "", "null", etc.)
			if envelope.Producer == "" {
				t.Errorf("Valid envelope has empty Producer")
			}
		}
	})
}

func FuzzPayloadValidation(f *testing.F) {
	validUUID := uuid.New().String()

	// Seed corpus for CandidateProducedPayload
	f.Add(validUUID, "openai", "gpt-4", int64(100), int64(50), int64(75), int64(125), "stop")
	f.Add("", "openai", "gpt-4", int64(100), int64(50), int64(75), int64(125), "stop")           // Invalid UUID
	f.Add("not-a-uuid", "openai", "gpt-4", int64(100), int64(50), int64(75), int64(125), "stop") // Invalid UUID format
	f.Add(validUUID, "", "gpt-4", int64(100), int64(50), int64(75), int64(125), "stop")          // Empty provider
	f.Add(validUUID, "openai", "", int64(100), int64(50), int64(75), int64(125), "stop")         // Empty model

	f.Fuzz(func(t *testing.T, answerID, provider, model string, latencyMs, promptTokens, completionTokens, totalTokens int64, finishReason string) {
		// Test CandidateProducedPayload validation
		payload := CandidateProducedPayload{
			AnswerID:         answerID,
			Provider:         provider,
			Model:            model,
			LatencyMs:        latencyMs,
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      totalTokens,
			FinishReason:     FinishReason(finishReason),
		}

		// Should never panic
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("CandidateProducedPayload.Validate panicked: %v", r)
			}
		}()

		err := payload.Validate()

		// If validation passes, verify required constraints
		if err == nil {
			// AnswerID should be valid UUID if not empty
			if answerID != "" {
				if _, parseErr := uuid.Parse(answerID); parseErr != nil {
					t.Errorf("Valid payload has invalid UUID format: %q", answerID)
				}
			}

			// Numeric fields should be non-negative
			if latencyMs < 0 {
				t.Errorf("Valid payload has negative LatencyMs: %d", latencyMs)
			}
			if promptTokens < 0 {
				t.Errorf("Valid payload has negative PromptTokens: %d", promptTokens)
			}
			if completionTokens < 0 {
				t.Errorf("Valid payload has negative CompletionTokens: %d", completionTokens)
			}
			if totalTokens < 0 {
				t.Errorf("Valid payload has negative TotalTokens: %d", totalTokens)
			}
		}

		// Check that valid UTF-8 strings don't cause validation failures for non-content reasons
		if utf8.ValidString(answerID) && utf8.ValidString(provider) && utf8.ValidString(model) && utf8.ValidString(finishReason) {
			if err != nil && answerID != "" && provider != "" && model != "" && latencyMs >= 0 && promptTokens >= 0 && completionTokens >= 0 && totalTokens >= 0 {
				// This might indicate a UUID validation issue
				if _, parseErr := uuid.Parse(answerID); parseErr != nil {
					// Expected validation failure for invalid UUID
				} else {
					t.Logf("Unexpected validation failure for apparently valid inputs: %v", err)
				}
			}
		}
	})
}

func FuzzLLMUsagePayloadValidation(f *testing.F) {
	// Seed corpus for LLMUsagePayload
	f.Add(int64(100), int64(5), int64(150), "openai", "gpt-4", true)
	f.Add(int64(0), int64(0), int64(0), "openai", "gpt-4", false)
	f.Add(int64(-1), int64(5), int64(150), "openai", "gpt-4", false)                                                   // Negative tokens
	f.Add(int64(100), int64(-1), int64(150), "openai", "gpt-4", false)                                                 // Negative calls
	f.Add(int64(100), int64(5), int64(-1), "openai", "gpt-4", false)                                                   // Negative cost
	f.Add(int64(100), int64(5), int64(150), "", "gpt-4", false)                                                        // Empty provider
	f.Add(int64(100), int64(5), int64(150), "openai", "", false)                                                       // Empty model
	f.Add(int64(9223372036854775807), int64(9223372036854775807), int64(9223372036854775807), "openai", "gpt-4", true) // Max values

	f.Fuzz(func(t *testing.T, totalTokens, totalCalls, costCents int64, provider, model string, cacheHit bool) {
		// Create models slice (never empty if model is not empty)
		var models []string
		if model != "" {
			models = []string{model}
		}

		payload := LLMUsagePayload{
			TotalTokens:        totalTokens,
			TotalCalls:         totalCalls,
			CostCents:          Cents(costCents),
			Provider:           provider,
			Models:             models,
			ProviderRequestIDs: []string{"req-123"},
			CacheHit:           cacheHit,
		}

		// Should never panic
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("LLMUsagePayload.Validate panicked: %v", r)
			}
		}()

		err := payload.Validate()

		// If validation passes, verify constraints
		if err == nil {
			if totalTokens < 0 {
				t.Errorf("Valid payload has negative TotalTokens: %d", totalTokens)
			}
			if totalCalls < 0 {
				t.Errorf("Valid payload has negative TotalCalls: %d", totalCalls)
			}
			if costCents < 0 {
				t.Errorf("Valid payload has negative CostCents: %d", costCents)
			}
			if provider == "" {
				t.Errorf("Valid payload has empty Provider")
			}
			if len(models) == 0 {
				t.Errorf("Valid payload has empty Models slice")
			}
		}

		// Check that valid UTF-8 strings don't cause validation failures for non-content reasons
		if utf8.ValidString(provider) && utf8.ValidString(model) {
			if err != nil && provider != "" && model != "" && totalTokens >= 0 && totalCalls >= 0 && costCents >= 0 {
				t.Logf("Unexpected validation failure for apparently valid inputs: %v", err)
			}
		}
	})
}

func FuzzJSON_Marshaling(f *testing.F) {
	// Test JSON marshaling robustness for event creation
	f.Add(`{"answer_id":"test","provider":"openai"}`)
	f.Add(`{}`)
	f.Add(`null`)
	f.Add(`[]`)
	f.Add(`""`)
	f.Add(`123`)
	f.Add(`true`)
	f.Add(`{"answer_id":"test","latency_ms":-1}`)
	f.Add(`{"answer_id":"","provider":"openai"}`)
	f.Add(`{"unicode":"你好世界"}`)
	f.Add(`{"control_chars":"\u0000\u0001\u0002"}`)
	f.Add(`{"large_number":9223372036854775807}`)

	f.Fuzz(func(t *testing.T, inputJSON string) {
		// Should never panic when creating events with arbitrary JSON
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("JSON processing panicked with input %q: %v", inputJSON, r)
			}
		}()

		// Test that raw JSON can be safely used as payload
		rawPayload := json.RawMessage(inputJSON)

		envelope := NewEventEnvelope(
			EventTypeCandidateProduced,
			uuid.New(),
			"workflow-123",
			"run-456",
			rawPayload,
			"test-producer",
			[]string{},
		)

		// Envelope creation should succeed with any JSON
		if envelope.EventType == "" {
			t.Errorf("NewEventEnvelope failed to set EventType with JSON: %q", inputJSON)
		}

		// The payload should be preserved as-is
		if string(envelope.Payload) != inputJSON {
			t.Errorf("NewEventEnvelope modified payload: got %q, expected %q", string(envelope.Payload), inputJSON)
		}

		// Validation may fail (and that's OK), but shouldn't panic
		_ = envelope.Validate()
	})
}
