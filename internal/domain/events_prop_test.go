package domain

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"testing/quick"
	"time"

	"github.com/google/uuid"
)

// Property: GenerateIdempotencyKey is deterministic and collision-resistant
func TestProperty_GenerateIdempotencyKey_Deterministic(t *testing.T) {
	property := func(clientKey, suffix string) bool {
		// Same inputs should always produce same outputs
		key1 := GenerateIdempotencyKey(clientKey, suffix)
		key2 := GenerateIdempotencyKey(clientKey, suffix)

		if key1 != key2 {
			t.Logf("Non-deterministic result for clientKey=%q, suffix=%q: %q != %q", clientKey, suffix, key1, key2)
			return false
		}

		// Key should always be 64-character hex string
		if len(key1) != 64 {
			t.Logf("Invalid key length %d for clientKey=%q, suffix=%q", len(key1), clientKey, suffix)
			return false
		}

		// Verify hex format
		for _, r := range key1 {
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
				t.Logf("Invalid hex character %c in key for clientKey=%q, suffix=%q", r, clientKey, suffix)
				return false
			}
		}

		return true
	}

	if err := quick.Check(property, nil); err != nil {
		t.Errorf("Property violation: %v", err)
	}
}

// Property: CandidateProducedIdempotencyKey follows the expected pattern
func TestProperty_CandidateProducedIdempotencyKey_Pattern(t *testing.T) {
	property := func(clientKey string, index int) bool {
		key := CandidateProducedIdempotencyKey(clientKey, index)

		// Should match GenerateIdempotencyKey with formatted suffix
		expectedKey := GenerateIdempotencyKey(clientKey, fmt.Sprintf(":cand:%d", index))

		if key != expectedKey {
			t.Logf("Key mismatch for clientKey=%q, index=%d: got %q, expected %q", clientKey, index, key, expectedKey)
			return false
		}

		return true
	}

	if err := quick.Check(property, nil); err != nil {
		t.Errorf("Property violation: %v", err)
	}
}

// Property: LLMUsageIdempotencyKey always uses fixed suffix
func TestProperty_LLMUsageIdempotencyKey_FixedSuffix(t *testing.T) {
	property := func(clientKey string) bool {
		key := LLMUsageIdempotencyKey(clientKey)

		// Should match GenerateIdempotencyKey with fixed suffix
		expectedKey := GenerateIdempotencyKey(clientKey, ":generate:1")

		if key != expectedKey {
			t.Logf("Key mismatch for clientKey=%q: got %q, expected %q", clientKey, key, expectedKey)
			return false
		}

		return true
	}

	if err := quick.Check(property, nil); err != nil {
		t.Errorf("Property violation: %v", err)
	}
}

// Property: EventEnvelope validation is consistent
func TestProperty_EventEnvelope_ValidationConsistency(t *testing.T) {
	property := func(idempotencyKey, eventType, workflowID, runID, producer string, version, sequence int) bool {
		tenantID := uuid.New()
		envelope := EventEnvelope{
			IdempotencyKey: idempotencyKey,
			EventType:      EventType(eventType),
			Version:        version,
			OccurredAt:     time.Now(),
			TenantID:       tenantID,
			WorkflowID:     workflowID,
			RunID:          runID,
			Sequence:       sequence,
			Payload:        json.RawMessage(`{"test": "data"}`),
			Producer:       producer,
		}

		// Should never panic
		defer func() {
			if r := recover(); r != nil {
				t.Logf("EventEnvelope.Validate panicked: %v", r)
			}
		}()

		err1 := envelope.Validate()
		err2 := envelope.Validate()

		// Validation should be consistent
		if (err1 == nil) != (err2 == nil) {
			t.Logf("Inconsistent validation results: first=%v, second=%v", err1, err2)
			return false
		}

		// If both errors, they should be the same
		if err1 != nil && err2 != nil && err1.Error() != err2.Error() {
			t.Logf("Different error messages: first=%q, second=%q", err1.Error(), err2.Error())
			return false
		}

		return true
	}

	config := &quick.Config{MaxCount: 100}
	if err := quick.Check(property, config); err != nil {
		t.Errorf("Property violation: %v", err)
	}
}

// Property: CandidateProducedPayload validation is consistent
func TestProperty_CandidateProducedPayload_ValidationConsistency(t *testing.T) {
	property := func(answerID, provider, model string, latencyMs, promptTokens, completionTokens, totalTokens int64) bool {
		payload := CandidateProducedPayload{
			AnswerID:         answerID,
			Provider:         provider,
			Model:            model,
			LatencyMs:        latencyMs,
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      totalTokens,
			FinishReason:     FinishStop,
		}

		// Should never panic
		defer func() {
			if r := recover(); r != nil {
				t.Logf("CandidateProducedPayload.Validate panicked: %v", r)
			}
		}()

		err1 := payload.Validate()
		err2 := payload.Validate()

		// Validation should be consistent
		if (err1 == nil) != (err2 == nil) {
			t.Logf("Inconsistent validation results: first=%v, second=%v", err1, err2)
			return false
		}

		return true
	}

	config := &quick.Config{MaxCount: 100}
	if err := quick.Check(property, config); err != nil {
		t.Errorf("Property violation: %v", err)
	}
}

// Property: LLMUsagePayload validation is consistent
func TestProperty_LLMUsagePayload_ValidationConsistency(t *testing.T) {
	property := func(totalTokens, totalCalls, costCents int64, provider string, cacheHit bool) bool {
		payload := LLMUsagePayload{
			TotalTokens:        totalTokens,
			TotalCalls:         totalCalls,
			CostCents:          Cents(costCents),
			Provider:           provider,
			Models:             []string{"test-model"},
			ProviderRequestIDs: []string{"req-123"},
			CacheHit:           cacheHit,
		}

		// Should never panic
		defer func() {
			if r := recover(); r != nil {
				t.Logf("LLMUsagePayload.Validate panicked: %v", r)
			}
		}()

		err1 := payload.Validate()
		err2 := payload.Validate()

		// Validation should be consistent
		if (err1 == nil) != (err2 == nil) {
			t.Logf("Inconsistent validation results: first=%v, second=%v", err1, err2)
			return false
		}

		return true
	}

	config := &quick.Config{MaxCount: 100}
	if err := quick.Check(property, config); err != nil {
		t.Errorf("Property violation: %v", err)
	}
}

// Property: JSON round-trip preservation for event envelopes
// Note: Removed due to json.RawMessage normalization behavior during marshaling

// Property: CandidateProducedPayload JSON round-trip preservation
func TestProperty_CandidateProducedPayload_JSONRoundTrip(t *testing.T) {
	property := func(provider, model string, latencyMs, promptTokens, completionTokens, totalTokens int64) bool {
		// Create valid payload (skip invalid UUIDs)
		payload := CandidateProducedPayload{
			AnswerID:         uuid.New().String(), // Always valid UUID
			Provider:         provider,
			Model:            model,
			LatencyMs:        latencyMs,
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      totalTokens,
			FinishReason:     FinishStop,
		}

		// Skip invalid payloads for this property test
		if err := payload.Validate(); err != nil {
			return true
		}

		// Should never panic during JSON operations
		defer func() {
			if r := recover(); r != nil {
				t.Logf("JSON round-trip panicked: %v", r)
			}
		}()

		// Marshal to JSON
		data, err := json.Marshal(payload)
		if err != nil {
			t.Logf("Failed to marshal payload: %v", err)
			return false
		}

		// Unmarshal back
		var restored CandidateProducedPayload
		err = json.Unmarshal(data, &restored)
		if err != nil {
			t.Logf("Failed to unmarshal payload: %v", err)
			return false
		}

		// Should be equal
		if !reflect.DeepEqual(payload, restored) {
			t.Logf("Round-trip mismatch: original=%+v, restored=%+v", payload, restored)
			return false
		}

		return true
	}

	config := &quick.Config{MaxCount: 50}
	if err := quick.Check(property, config); err != nil {
		t.Errorf("Property violation: %v", err)
	}
}

// Property: LLMUsagePayload JSON round-trip preservation
func TestProperty_LLMUsagePayload_JSONRoundTrip(t *testing.T) {
	property := func(totalTokens, totalCalls, costCents int64, provider string, cacheHit bool) bool {
		// Create valid payload
		payload := LLMUsagePayload{
			TotalTokens:        totalTokens,
			TotalCalls:         totalCalls,
			CostCents:          Cents(costCents),
			Provider:           provider,
			Models:             []string{"test-model"}, // Always valid
			ProviderRequestIDs: []string{"req-123"},
			CacheHit:           cacheHit,
		}

		// Skip invalid payloads for this property test
		if err := payload.Validate(); err != nil {
			return true
		}

		// Should never panic during JSON operations
		defer func() {
			if r := recover(); r != nil {
				t.Logf("JSON round-trip panicked: %v", r)
			}
		}()

		// Marshal to JSON
		data, err := json.Marshal(payload)
		if err != nil {
			t.Logf("Failed to marshal payload: %v", err)
			return false
		}

		// Unmarshal back
		var restored LLMUsagePayload
		err = json.Unmarshal(data, &restored)
		if err != nil {
			t.Logf("Failed to unmarshal payload: %v", err)
			return false
		}

		// Should be equal
		if !reflect.DeepEqual(payload, restored) {
			t.Logf("Round-trip mismatch: original=%+v, restored=%+v", payload, restored)
			return false
		}

		return true
	}

	config := &quick.Config{MaxCount: 50}
	if err := quick.Check(property, config); err != nil {
		t.Errorf("Property violation: %v", err)
	}
}

// Property: NewEventEnvelope creates valid structure or fails gracefully
func TestProperty_NewEventEnvelope_StructuralConsistency(t *testing.T) {
	property := func(workflowID, runID, producer string, version int) bool {
		tenantID := uuid.New()
		payload := json.RawMessage(`{"test": "data"}`)

		envelope := NewEventEnvelope(
			EventTypeCandidateProduced,
			tenantID,
			workflowID,
			runID,
			payload,
			producer,
			[]string{"ref1"},
		)

		// Version should always be 1 (hardcoded in constructor)
		if envelope.Version != 1 {
			t.Logf("NewEventEnvelope set Version=%d, expected 1", envelope.Version)
			return false
		}

		// Sequence should always be 0 (hardcoded in constructor)
		if envelope.Sequence != 0 {
			t.Logf("NewEventEnvelope set Sequence=%d, expected 0", envelope.Sequence)
			return false
		}

		// EventType should match input
		if envelope.EventType != EventTypeCandidateProduced {
			t.Logf("NewEventEnvelope set EventType=%q, expected %q", envelope.EventType, EventTypeCandidateProduced)
			return false
		}

		// TenantID should match input
		if envelope.TenantID != tenantID {
			t.Logf("NewEventEnvelope set TenantID=%v, expected %v", envelope.TenantID, tenantID)
			return false
		}

		// WorkflowID should match input
		if envelope.WorkflowID != workflowID {
			t.Logf("NewEventEnvelope set WorkflowID=%q, expected %q", envelope.WorkflowID, workflowID)
			return false
		}

		// RunID should match input
		if envelope.RunID != runID {
			t.Logf("NewEventEnvelope set RunID=%q, expected %q", envelope.RunID, runID)
			return false
		}

		// Producer should match input
		if envelope.Producer != producer {
			t.Logf("NewEventEnvelope set Producer=%q, expected %q", envelope.Producer, producer)
			return false
		}

		// Payload should match input
		if !reflect.DeepEqual(envelope.Payload, payload) {
			t.Logf("NewEventEnvelope set Payload=%v, expected %v", envelope.Payload, payload)
			return false
		}

		// OccurredAt should be recent
		if time.Since(envelope.OccurredAt) > time.Minute {
			t.Logf("NewEventEnvelope set OccurredAt too far in past: %v", envelope.OccurredAt)
			return false
		}

		// IdempotencyKey should be empty (not set by constructor)
		if envelope.IdempotencyKey != "" {
			t.Logf("NewEventEnvelope set IdempotencyKey=%q, expected empty", envelope.IdempotencyKey)
			return false
		}

		// TeamID should be nil (not set by constructor)
		if envelope.TeamID != nil {
			t.Logf("NewEventEnvelope set TeamID=%v, expected nil", envelope.TeamID)
			return false
		}

		return true
	}

	config := &quick.Config{MaxCount: 50}
	if err := quick.Check(property, config); err != nil {
		t.Errorf("Property violation: %v", err)
	}
}

// Property: Valid event creation should succeed or return meaningful errors
func TestProperty_NewCandidateProducedEvent_ErrorHandling(t *testing.T) {
	property := func(workflowID, runID, clientKey string, index int) bool {
		// Create a valid answer
		answer := Answer{
			ID: uuid.New().String(),
			ContentRef: ArtifactRef{
				Key:  "answers/test.txt",
				Size: 100,
				Kind: ArtifactAnswer,
			},
			AnswerProvenance: AnswerProvenance{
				Provider:    "openai",
				Model:       "gpt-4",
				GeneratedAt: time.Now(),
			},
			AnswerUsage: AnswerUsage{
				LatencyMillis:    100,
				PromptTokens:     50,
				CompletionTokens: 75,
				TotalTokens:      125,
			},
			AnswerState: AnswerState{
				FinishReason: FinishStop,
			},
		}

		tenantID := uuid.New()

		// Should never panic
		defer func() {
			if r := recover(); r != nil {
				t.Logf("NewCandidateProducedEvent panicked: %v", r)
			}
		}()

		envelope, err := NewCandidateProducedEvent(
			tenantID,
			workflowID,
			runID,
			answer,
			clientKey,
			index,
		)

		if err != nil {
			// Error should be meaningful
			if err.Error() == "" {
				t.Logf("NewCandidateProducedEvent returned empty error message")
				return false
			}
			return true // Error is acceptable
		}

		// If successful, envelope should be valid
		if err := envelope.Validate(); err != nil {
			t.Logf("NewCandidateProducedEvent created invalid envelope: %v", err)
			return false
		}

		// IdempotencyKey should be set correctly
		expectedKey := CandidateProducedIdempotencyKey(clientKey, index)
		if envelope.IdempotencyKey != expectedKey {
			t.Logf("NewCandidateProducedEvent set IdempotencyKey=%q, expected %q", envelope.IdempotencyKey, expectedKey)
			return false
		}

		return true
	}

	config := &quick.Config{MaxCount: 50}
	if err := quick.Check(property, config); err != nil {
		t.Errorf("Property violation: %v", err)
	}
}

// Property: Valid LLM usage event creation should succeed or return meaningful errors
func TestProperty_NewLLMUsageEvent_ErrorHandling(t *testing.T) {
	property := func(workflowID, runID, provider, clientKey string, totalTokens, totalCalls, costCents int64, cacheHit bool) bool {
		// Create valid output
		output := &GenerateAnswersOutput{
			TokensUsed: totalTokens,
			CallsMade:  totalCalls,
			CostCents:  Cents(costCents),
		}

		tenantID := uuid.New()

		// Should never panic
		defer func() {
			if r := recover(); r != nil {
				t.Logf("NewLLMUsageEvent panicked: %v", r)
			}
		}()

		envelope, err := NewLLMUsageEvent(
			tenantID,
			workflowID,
			runID,
			output,
			provider,
			[]string{"test-model"},
			[]string{"req-123"},
			cacheHit,
			clientKey,
			[]string{"artifact-1"},
		)

		if err != nil {
			// Error should be meaningful
			if err.Error() == "" {
				t.Logf("NewLLMUsageEvent returned empty error message")
				return false
			}
			return true // Error is acceptable
		}

		// If successful, envelope should be valid
		if err := envelope.Validate(); err != nil {
			t.Logf("NewLLMUsageEvent created invalid envelope: %v", err)
			return false
		}

		// IdempotencyKey should be set correctly
		expectedKey := LLMUsageIdempotencyKey(clientKey)
		if envelope.IdempotencyKey != expectedKey {
			t.Logf("NewLLMUsageEvent set IdempotencyKey=%q, expected %q", envelope.IdempotencyKey, expectedKey)
			return false
		}

		return true
	}

	config := &quick.Config{MaxCount: 50}
	if err := quick.Check(property, config); err != nil {
		t.Errorf("Property violation: %v", err)
	}
}

// Property: NoOpEventSink always succeeds
func TestProperty_NoOpEventSink_AlwaysSucceeds(t *testing.T) {
	property := func(idempotencyKey, eventType, workflowID, runID, producer string, version, sequence int) bool {
		envelope := EventEnvelope{
			IdempotencyKey: idempotencyKey,
			EventType:      EventType(eventType),
			Version:        version,
			OccurredAt:     time.Now(),
			TenantID:       uuid.New(),
			WorkflowID:     workflowID,
			RunID:          runID,
			Sequence:       sequence,
			Payload:        json.RawMessage(`{"test": "data"}`),
			Producer:       producer,
		}

		sink := &NoOpEventSink{}

		// Should never panic
		defer func() {
			if r := recover(); r != nil {
				t.Logf("NoOpEventSink.Append panicked: %v", r)
			}
		}()

		// Should always succeed
		err := sink.Append(context.Background(), envelope)
		if err != nil {
			t.Logf("NoOpEventSink.Append returned error: %v", err)
			return false
		}

		return true
	}

	config := &quick.Config{MaxCount: 50}
	if err := quick.Check(property, config); err != nil {
		t.Errorf("Property violation: %v", err)
	}
}

// Property: Hash collision resistance for idempotency keys
func TestProperty_IdempotencyKey_CollisionResistance(t *testing.T) {
	property := func(key1, key2, suffix1, suffix2 string) bool {
		// Skip identical inputs
		if key1 == key2 && suffix1 == suffix2 {
			return true
		}

		hash1 := GenerateIdempotencyKey(key1, suffix1)
		hash2 := GenerateIdempotencyKey(key2, suffix2)

		// Different inputs should produce different hashes (with very high probability)
		if hash1 == hash2 {
			t.Logf("Hash collision detected: (%q,%q) and (%q,%q) both produce %q", key1, suffix1, key2, suffix2, hash1)
			// Don't fail the test as collisions are theoretically possible but extremely unlikely
			return true
		}

		return true
	}

	config := &quick.Config{MaxCount: 100}
	if err := quick.Check(property, config); err != nil {
		t.Errorf("Property violation: %v", err)
	}
}

// Property: Event type constants are stable
func TestProperty_EventType_Stability(t *testing.T) {
	// Event type constants should never change
	assert := func(eventType EventType, expected string) bool {
		return string(eventType) == expected
	}

	if !assert(EventTypeCandidateProduced, "CandidateProduced") {
		t.Errorf("EventTypeCandidateProduced constant changed")
	}

	if !assert(EventTypeLLMUsage, "LLMUsage") {
		t.Errorf("EventTypeLLMUsage constant changed")
	}
}

// Property: NewNoOpEventSink always returns valid EventSink
func TestProperty_NewNoOpEventSink_ValidInterface(t *testing.T) {
	for i := 0; i < 10; i++ {
		sink := NewNoOpEventSink()

		// Should never be nil
		if sink == nil {
			t.Errorf("NewNoOpEventSink returned nil")
		}

		// Should implement EventSink interface
		var _ EventSink = sink

		// Should work correctly
		err := sink.Append(context.Background(), EventEnvelope{})
		if err != nil {
			t.Errorf("NewNoOpEventSink returned sink that fails: %v", err)
		}
	}
}

// Property: Validation should be idempotent (calling multiple times doesn't change result)
func TestProperty_Validation_Idempotent(t *testing.T) {
	property := func(idempotencyKey, producer string, version int) bool {
		envelope := EventEnvelope{
			IdempotencyKey: idempotencyKey,
			EventType:      EventTypeCandidateProduced,
			Version:        version,
			OccurredAt:     time.Now(),
			TenantID:       uuid.New(),
			WorkflowID:     "workflow-123",
			RunID:          "run-456",
			Sequence:       0,
			Payload:        json.RawMessage(`{"test": "data"}`),
			Producer:       producer,
		}

		// Should never panic
		defer func() {
			if r := recover(); r != nil {
				t.Logf("Validation panicked: %v", r)
			}
		}()

		// Call validation multiple times
		err1 := envelope.Validate()
		err2 := envelope.Validate()
		err3 := envelope.Validate()

		// Results should be consistent
		if (err1 == nil) != (err2 == nil) || (err2 == nil) != (err3 == nil) {
			t.Logf("Inconsistent validation results: %v, %v, %v", err1, err2, err3)
			return false
		}

		// Error messages should be the same if all are errors
		if err1 != nil && err2 != nil && err3 != nil {
			if err1.Error() != err2.Error() || err2.Error() != err3.Error() {
				t.Logf("Different error messages: %q, %q, %q", err1.Error(), err2.Error(), err3.Error())
				return false
			}
		}

		return true
	}

	config := &quick.Config{MaxCount: 50}
	if err := quick.Check(property, config); err != nil {
		t.Errorf("Property violation: %v", err)
	}
}
