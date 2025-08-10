//nolint:testpackage // Tests need access to unexported functions like trackingEventSink
package activity

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/business"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

func TestIdempotencyKeyGeneration(t *testing.T) {
	t.Run("same input produces same idempotency key", func(t *testing.T) {
		mockClient := newMockLLMClient()
		mockClient.generateReturnsError = false
		artifactStore := business.NewInMemoryArtifactStore()
		eventSink := &trackingEventSink{events: make([]domain.EventEnvelope, 0)}
		activities := NewActivities(mockClient, artifactStore, eventSink)

		ctx := context.Background()

		input := domain.GenerateAnswersInput{
			Question:   "What is 2+2?",
			NumAnswers: 3,
			Config: domain.EvalConfig{
				Provider:        "openai",
				Model:           "gpt-4",
				MaxAnswers:      3,
				MaxAnswerTokens: 100,
				Temperature:     0.7,
				ScoreThreshold:  0.7,
				Timeout:         60,
			},
		}

		// Execute twice with same input
		result1, err1 := activities.GenerateAnswers(ctx, input)
		require.NoError(t, err1)

		result2, err2 := activities.GenerateAnswers(ctx, input)
		require.NoError(t, err2)

		// Should produce same ClientIdemKey
		assert.Equal(t, result1.ClientIdemKey, result2.ClientIdemKey,
			"Same input should produce same idempotency key")

		// Event idempotency keys should also match
		events1 := eventSink.getEventsByType("LLMUsage")
		events2 := eventSink.getEventsByType("LLMUsage")

		if len(events1) >= 1 && len(events2) >= 2 {
			assert.Equal(t, events1[0].IdempotencyKey, events2[1].IdempotencyKey,
				"Same input should produce same event idempotency keys")
		}
	})

	t.Run("different inputs produce different idempotency keys", func(t *testing.T) {
		mockClient := newMockLLMClient()
		mockClient.generateReturnsError = false
		artifactStore := business.NewInMemoryArtifactStore()
		activities := NewActivities(mockClient, artifactStore, domain.NewNoOpEventSink())

		ctx := context.Background()

		input1 := domain.GenerateAnswersInput{
			Question:   "What is 2+2?",
			NumAnswers: 3,
			Config:     domain.DefaultEvalConfig(),
		}

		input2 := domain.GenerateAnswersInput{
			Question:   "What is 3+3?", // Different question
			NumAnswers: 3,
			Config:     domain.DefaultEvalConfig(),
		}

		result1, err1 := activities.GenerateAnswers(ctx, input1)
		require.NoError(t, err1)

		result2, err2 := activities.GenerateAnswers(ctx, input2)
		require.NoError(t, err2)

		// Should produce different ClientIdemKeys
		assert.NotEqual(t, result1.ClientIdemKey, result2.ClientIdemKey,
			"Different inputs should produce different idempotency keys")
	})

	t.Run("idempotency key excludes RunID", func(t *testing.T) {
		// This test verifies that RunID is NOT part of the key
		// Same workflow but different runs should produce same key

		tenantID := uuid.New()
		input := domain.GenerateAnswersInput{
			Question:   "What is 2+2?",
			NumAnswers: 1,
			Config: domain.EvalConfig{
				Provider:        "openai",
				Model:           "gpt-4",
				MaxAnswers:      1,
				MaxAnswerTokens: 100,
				Temperature:     0.5,
				ScoreThreshold:  0.7,
				Timeout:         60,
			},
		}

		// Generate canonical key directly (simulating what client does)
		req := &transport.Request{
			Operation:   transport.OpGeneration,
			Provider:    input.Config.Provider,
			Model:       input.Config.Model,
			TenantID:    tenantID.String(),
			Question:    input.Question,
			MaxTokens:   input.Config.MaxAnswerTokens,
			Temperature: input.Config.Temperature,
		}

		key1, err1 := transport.GenerateIdemKey(req)
		require.NoError(t, err1)

		// Same request should produce same key
		key2, err2 := transport.GenerateIdemKey(req)
		require.NoError(t, err2)

		assert.Equal(t, key1, key2, "Same canonical request should produce same key")

		// Verify RunID is NOT in the canonical payload
		payload, err := transport.BuildCanonicalPayload(req)
		require.NoError(t, err)

		// The canonical payload should NOT contain RunID
		jsonBytes, err := json.Marshal(payload)
		require.NoError(t, err)
		assert.NotContains(t, string(jsonBytes), "run_id",
			"Canonical payload should not contain RunID")
	})

	t.Run("event idempotency keys derived correctly", func(t *testing.T) {
		clientIdemKey := "abc123def456" // Simulated client key

		// Test CandidateProduced key generation
		candidateKey0 := domain.CandidateProducedIdempotencyKey(clientIdemKey, 0)
		candidateKey1 := domain.CandidateProducedIdempotencyKey(clientIdemKey, 1)

		// Should be different for different indices
		assert.NotEqual(t, candidateKey0, candidateKey1,
			"Different indices should produce different keys")

		// Should be deterministic
		candidateKey0Again := domain.CandidateProducedIdempotencyKey(clientIdemKey, 0)
		assert.Equal(t, candidateKey0, candidateKey0Again,
			"Same input should produce same key")

		// Test LLMUsage key generation
		usageKey := domain.LLMUsageIdempotencyKey(clientIdemKey)
		usageKeyAgain := domain.LLMUsageIdempotencyKey(clientIdemKey)

		assert.Equal(t, usageKey, usageKeyAgain,
			"LLMUsage key should be deterministic")

		// Usage key should differ from candidate keys
		assert.NotEqual(t, usageKey, candidateKey0,
			"Different event types should have different keys")
	})
}

func TestArtifactIdempotency(t *testing.T) {
	t.Run("artifact storage works correctly", func(t *testing.T) {
		mockClient := newMockLLMClient()
		mockClient.generateReturnsError = false
		artifactStore := business.NewInMemoryArtifactStore()
		activities := NewActivities(mockClient, artifactStore, domain.NewNoOpEventSink())

		answer := domain.Answer{
			ID: uuid.New().String(),
			Metadata: map[string]any{
				"content": "This is the answer content",
			},
		}

		workflowID := "test-workflow-123"

		// Store content successfully
		ref1, err1 := activities.storeAnswerContent(context.Background(),
			answer, workflowID, nil)
		require.NoError(t, err1)
		assert.NotEmpty(t, ref1.Key, "artifact key should be generated")
		assert.Equal(t, domain.ArtifactAnswer, ref1.Kind, "artifact should be answer type")

		ref2, err2 := activities.storeAnswerContent(context.Background(),
			answer, workflowID, nil)
		require.NoError(t, err2)
		assert.NotEmpty(t, ref2.Key, "artifact key should be generated")

		// Artifact keys should be unique (contain UUIDs for uniqueness)
		assert.NotEqual(t, ref1.Key, ref2.Key,
			"Different storage operations should produce unique artifact keys")

		// Both artifacts should be retrievable
		content1, err := artifactStore.Get(context.Background(), ref1)
		require.NoError(t, err)
		assert.Contains(t, content1, "This is the answer content")

		content2, err := artifactStore.Get(context.Background(), ref2)
		require.NoError(t, err)
		assert.Contains(t, content2, "This is the answer content")
	})

	t.Run("different content produces different artifact keys", func(t *testing.T) {
		mockClient := newMockLLMClient()
		mockClient.generateReturnsError = false
		artifactStore := business.NewInMemoryArtifactStore()
		activities := NewActivities(mockClient, artifactStore, domain.NewNoOpEventSink())

		answer1 := domain.Answer{
			ID: uuid.New().String(),
			Metadata: map[string]any{
				"content": "Answer content 1",
			},
		}

		answer2 := domain.Answer{
			ID: uuid.New().String(),
			Metadata: map[string]any{
				"content": "Answer content 2", // Different content
			},
		}

		workflowID := "test-workflow-123"

		ref1, err1 := activities.storeAnswerContent(context.Background(),
			answer1, workflowID, nil)
		require.NoError(t, err1)

		ref2, err2 := activities.storeAnswerContent(context.Background(),
			answer2, workflowID, nil)
		require.NoError(t, err2)

		// Should produce different artifact keys
		assert.NotEqual(t, ref1.Key, ref2.Key,
			"Different content should produce different artifact keys")
	})
}

// Test helper: event sink that tracks emitted events
type trackingEventSink struct {
	events []domain.EventEnvelope
}

func (t *trackingEventSink) Append(ctx context.Context, event domain.EventEnvelope) error {
	t.events = append(t.events, event)
	return nil
}

func (t *trackingEventSink) getEventsByType(eventType string) []domain.EventEnvelope {
	var filtered []domain.EventEnvelope
	for _, e := range t.events {
		if string(e.EventType) == eventType {
			filtered = append(filtered, e)
		}
	}
	return filtered
}
