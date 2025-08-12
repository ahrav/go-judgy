//nolint:testpackage // Tests need access to unexported functions like trackingEventSink
package generation

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
	"github.com/ahrav/go-judgy/pkg/activity"
)

func TestIdempotencyKeyGeneration(t *testing.T) {
	t.Run("same input produces same idempotency key", func(t *testing.T) {
		// Use proper EventSink from test helpers
		eventSink := NewCapturingEventSink()
		mockClient := NewEnhancedMockClient()
		mockClient.generateReturnsError = false
		mockClient.numAnswers = 3

		base := activity.NewBaseActivities(eventSink)
		artifactStore := business.NewInMemoryArtifactStore()
		activities := NewActivities(base, mockClient, artifactStore)

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

		// Capture events from first execution
		events1 := eventSink.GetEvents()
		candidateEvents1 := eventSink.GetEventsByType("generation.candidate_produced")
		usageEvents1 := eventSink.GetEventsByType("generation.llm_usage")

		// Reset for second execution
		eventSink.Reset()

		result2, err2 := activities.GenerateAnswers(ctx, input)
		require.NoError(t, err2)

		// Capture events from second execution
		candidateEvents2 := eventSink.GetEventsByType("generation.candidate_produced")
		usageEvents2 := eventSink.GetEventsByType("generation.llm_usage")

		// Should produce same ClientIdemKey
		assert.Equal(t, result1.ClientIdemKey, result2.ClientIdemKey,
			"Same input should produce same idempotency key")

		// Event counts should be correct
		assert.Len(t, candidateEvents1, 3, "Should emit 3 CandidateProduced events")
		assert.Len(t, usageEvents1, 1, "Should emit 1 LLMUsage event")
		assert.Len(t, candidateEvents2, 3, "Second run should also emit 3 CandidateProduced events")
		assert.Len(t, usageEvents2, 1, "Second run should also emit 1 LLMUsage event")

		// Event idempotency keys should match
		for i := 0; i < 3; i++ {
			assert.Equal(t, candidateEvents1[i].IdempotencyKey, candidateEvents2[i].IdempotencyKey,
				"CandidateProduced event %d should have same idempotency key", i)
		}
		assert.Equal(t, usageEvents1[0].IdempotencyKey, usageEvents2[0].IdempotencyKey,
			"LLMUsage events should have same idempotency key")

		// Verify total event count
		assert.Len(t, events1, 4, "First run should emit 4 events total")
	})

	t.Run("different inputs produce different idempotency keys", func(t *testing.T) {
		eventSink := NewCapturingEventSink()
		mockClient := NewEnhancedMockClient()
		mockClient.generateReturnsError = false

		base := activity.NewBaseActivities(eventSink)
		artifactStore := business.NewInMemoryArtifactStore()
		activities := NewActivities(base, mockClient, artifactStore)

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
	t.Run("artifact storage produces deterministic keys", func(t *testing.T) {
		eventSink := NewCapturingEventSink()
		mockClient := NewEnhancedMockClient()
		mockClient.generateReturnsError = false

		base := activity.NewBaseActivities(eventSink)
		artifactStore := business.NewInMemoryArtifactStore()
		activities := NewActivities(base, mockClient, artifactStore)

		answer := domain.Answer{
			ID: uuid.New().String(),
			Metadata: map[string]any{
				"content": "This is the answer content",
			},
		}

		workflowID := "test-workflow-123"

		// Store content successfully
		ref1, err1 := activities.StoreAnswerContent(context.Background(),
			answer, workflowID, "test-tenant", "test-idem-key", 0)
		require.NoError(t, err1)
		assert.NotEmpty(t, ref1.Key, "artifact key should be generated")
		assert.Equal(t, domain.ArtifactAnswer, ref1.Kind, "artifact should be answer type")

		ref2, err2 := activities.StoreAnswerContent(context.Background(),
			answer, workflowID, "test-tenant", "test-idem-key", 0)
		require.NoError(t, err2)
		assert.NotEmpty(t, ref2.Key, "artifact key should be generated")

		// Artifact keys should be idempotent - same inputs produce same key
		assert.Equal(t, ref1.Key, ref2.Key,
			"Same inputs should produce identical artifact keys for idempotency")

		// Both artifacts should be retrievable
		content1, err := artifactStore.Get(context.Background(), ref1)
		require.NoError(t, err)
		assert.Contains(t, content1, "This is the answer content")

		content2, err := artifactStore.Get(context.Background(), ref2)
		require.NoError(t, err)
		assert.Contains(t, content2, "This is the answer content")
	})

	t.Run("different answers produce different artifact keys", func(t *testing.T) {
		eventSink := NewCapturingEventSink()
		mockClient := NewEnhancedMockClient()
		mockClient.generateReturnsError = false

		base := activity.NewBaseActivities(eventSink)
		artifactStore := business.NewInMemoryArtifactStore()
		activities := NewActivities(base, mockClient, artifactStore)

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

		ref1, err1 := activities.StoreAnswerContent(context.Background(),
			answer1, workflowID, "test-tenant", "test-idem-key-1", 0)
		require.NoError(t, err1)

		ref2, err2 := activities.StoreAnswerContent(context.Background(),
			answer2, workflowID, "test-tenant", "test-idem-key-2", 1)
		require.NoError(t, err2)

		// Should produce different artifact keys
		assert.NotEqual(t, ref1.Key, ref2.Key,
			"Different content should produce different artifact keys")
	})
}

// Note: trackingEventSink has been replaced with the more comprehensive
// CapturingEventSink from test_helpers.go which provides:
// - Thread-safe event collection
// - Idempotency key tracking
// - Failure simulation for resilience testing
// - Better assertion helpers
