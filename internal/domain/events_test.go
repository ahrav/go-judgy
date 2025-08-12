package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventType_Constants(t *testing.T) {
	tests := []struct {
		name     string
		event    EventType
		expected string
	}{
		{
			name:     "CandidateProduced event type",
			event:    EventTypeCandidateProduced,
			expected: "CandidateProduced",
		},
		{
			name:     "LLMUsage event type",
			event:    EventTypeLLMUsage,
			expected: "LLMUsage",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, string(tt.event))
		})
	}
}

func TestEventEnvelope_Validate(t *testing.T) {
	validTenantID := uuid.New()
	validTime := time.Now()
	validPayload := json.RawMessage(`{"test": "data"}`)

	tests := []struct {
		name     string
		envelope EventEnvelope
		wantErr  bool
		errMsg   string
	}{
		{
			name: "valid envelope",
			envelope: EventEnvelope{
				IdempotencyKey: "test-key",
				EventType:      EventTypeCandidateProduced,
				Version:        1,
				OccurredAt:     validTime,
				TenantID:       validTenantID,
				WorkflowID:     "workflow-123",
				RunID:          "run-456",
				Sequence:       0,
				Payload:        validPayload,
				Producer:       "test-producer",
			},
			wantErr: false,
		},
		{
			name: "missing idempotency key",
			envelope: EventEnvelope{
				IdempotencyKey: "",
				EventType:      EventTypeCandidateProduced,
				Version:        1,
				OccurredAt:     validTime,
				TenantID:       validTenantID,
				WorkflowID:     "workflow-123",
				RunID:          "run-456",
				Sequence:       0,
				Payload:        validPayload,
				Producer:       "test-producer",
			},
			wantErr: true,
			errMsg:  "IdempotencyKey",
		},
		{
			name: "missing event type",
			envelope: EventEnvelope{
				IdempotencyKey: "test-key",
				EventType:      "",
				Version:        1,
				OccurredAt:     validTime,
				TenantID:       validTenantID,
				WorkflowID:     "workflow-123",
				RunID:          "run-456",
				Sequence:       0,
				Payload:        validPayload,
				Producer:       "test-producer",
			},
			wantErr: true,
			errMsg:  "EventType",
		},
		{
			name: "zero version",
			envelope: EventEnvelope{
				IdempotencyKey: "test-key",
				EventType:      EventTypeCandidateProduced,
				Version:        0,
				OccurredAt:     validTime,
				TenantID:       validTenantID,
				WorkflowID:     "workflow-123",
				RunID:          "run-456",
				Sequence:       0,
				Payload:        validPayload,
				Producer:       "test-producer",
			},
			wantErr: true,
			errMsg:  "Version",
		},
		{
			name: "negative version",
			envelope: EventEnvelope{
				IdempotencyKey: "test-key",
				EventType:      EventTypeCandidateProduced,
				Version:        -1,
				OccurredAt:     validTime,
				TenantID:       validTenantID,
				WorkflowID:     "workflow-123",
				RunID:          "run-456",
				Sequence:       0,
				Payload:        validPayload,
				Producer:       "test-producer",
			},
			wantErr: true,
			errMsg:  "Version",
		},
		{
			name: "zero occurred_at time",
			envelope: EventEnvelope{
				IdempotencyKey: "test-key",
				EventType:      EventTypeCandidateProduced,
				Version:        1,
				OccurredAt:     time.Time{},
				TenantID:       validTenantID,
				WorkflowID:     "workflow-123",
				RunID:          "run-456",
				Sequence:       0,
				Payload:        validPayload,
				Producer:       "test-producer",
			},
			wantErr: true,
			errMsg:  "OccurredAt",
		},
		{
			name: "zero tenant ID",
			envelope: EventEnvelope{
				IdempotencyKey: "test-key",
				EventType:      EventTypeCandidateProduced,
				Version:        1,
				OccurredAt:     validTime,
				TenantID:       uuid.UUID{},
				WorkflowID:     "workflow-123",
				RunID:          "run-456",
				Sequence:       0,
				Payload:        validPayload,
				Producer:       "test-producer",
			},
			wantErr: true,
			errMsg:  "TenantID",
		},
		{
			name: "missing workflow ID",
			envelope: EventEnvelope{
				IdempotencyKey: "test-key",
				EventType:      EventTypeCandidateProduced,
				Version:        1,
				OccurredAt:     validTime,
				TenantID:       validTenantID,
				WorkflowID:     "",
				RunID:          "run-456",
				Sequence:       0,
				Payload:        validPayload,
				Producer:       "test-producer",
			},
			wantErr: true,
			errMsg:  "WorkflowID",
		},
		{
			name: "missing run ID",
			envelope: EventEnvelope{
				IdempotencyKey: "test-key",
				EventType:      EventTypeCandidateProduced,
				Version:        1,
				OccurredAt:     validTime,
				TenantID:       validTenantID,
				WorkflowID:     "workflow-123",
				RunID:          "",
				Sequence:       0,
				Payload:        validPayload,
				Producer:       "test-producer",
			},
			wantErr: true,
			errMsg:  "RunID",
		},
		{
			name: "negative sequence",
			envelope: EventEnvelope{
				IdempotencyKey: "test-key",
				EventType:      EventTypeCandidateProduced,
				Version:        1,
				OccurredAt:     validTime,
				TenantID:       validTenantID,
				WorkflowID:     "workflow-123",
				RunID:          "run-456",
				Sequence:       -1,
				Payload:        validPayload,
				Producer:       "test-producer",
			},
			wantErr: true,
			errMsg:  "Sequence",
		},
		{
			name: "nil payload",
			envelope: EventEnvelope{
				IdempotencyKey: "test-key",
				EventType:      EventTypeCandidateProduced,
				Version:        1,
				OccurredAt:     validTime,
				TenantID:       validTenantID,
				WorkflowID:     "workflow-123",
				RunID:          "run-456",
				Sequence:       0,
				Payload:        nil,
				Producer:       "test-producer",
			},
			wantErr: true,
			errMsg:  "Payload",
		},
		{
			name: "missing producer",
			envelope: EventEnvelope{
				IdempotencyKey: "test-key",
				EventType:      EventTypeCandidateProduced,
				Version:        1,
				OccurredAt:     validTime,
				TenantID:       validTenantID,
				WorkflowID:     "workflow-123",
				RunID:          "run-456",
				Sequence:       0,
				Payload:        validPayload,
				Producer:       "",
			},
			wantErr: true,
			errMsg:  "Producer",
		},
		{
			name: "valid envelope with team ID",
			envelope: EventEnvelope{
				IdempotencyKey: "test-key",
				EventType:      EventTypeCandidateProduced,
				Version:        1,
				OccurredAt:     validTime,
				TenantID:       validTenantID,
				TeamID:         &validTenantID,
				WorkflowID:     "workflow-123",
				RunID:          "run-456",
				Sequence:       0,
				ArtifactRefs:   []string{"ref1", "ref2"},
				Payload:        validPayload,
				Producer:       "test-producer",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.envelope.Validate()
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCandidateProducedPayload_Validate(t *testing.T) {
	tests := []struct {
		name    string
		payload CandidateProducedPayload
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid payload",
			payload: CandidateProducedPayload{
				AnswerID:         uuid.New().String(),
				Provider:         "openai",
				Model:            "gpt-4",
				LatencyMs:        150,
				PromptTokens:     100,
				CompletionTokens: 200,
				TotalTokens:      300,
				FinishReason:     FinishStop,
			},
			wantErr: false,
		},
		{
			name: "missing answer ID",
			payload: CandidateProducedPayload{
				AnswerID:         "",
				Provider:         "openai",
				Model:            "gpt-4",
				LatencyMs:        150,
				PromptTokens:     100,
				CompletionTokens: 200,
				TotalTokens:      300,
			},
			wantErr: true,
			errMsg:  "AnswerID",
		},
		{
			name: "invalid answer ID format",
			payload: CandidateProducedPayload{
				AnswerID:         "not-a-uuid",
				Provider:         "openai",
				Model:            "gpt-4",
				LatencyMs:        150,
				PromptTokens:     100,
				CompletionTokens: 200,
				TotalTokens:      300,
			},
			wantErr: true,
			errMsg:  "uuid",
		},
		{
			name: "missing provider",
			payload: CandidateProducedPayload{
				AnswerID:         uuid.New().String(),
				Provider:         "",
				Model:            "gpt-4",
				LatencyMs:        150,
				PromptTokens:     100,
				CompletionTokens: 200,
				TotalTokens:      300,
			},
			wantErr: true,
			errMsg:  "Provider",
		},
		{
			name: "missing model",
			payload: CandidateProducedPayload{
				AnswerID:         uuid.New().String(),
				Provider:         "openai",
				Model:            "",
				LatencyMs:        150,
				PromptTokens:     100,
				CompletionTokens: 200,
				TotalTokens:      300,
			},
			wantErr: true,
			errMsg:  "Model",
		},
		{
			name: "negative latency",
			payload: CandidateProducedPayload{
				AnswerID:         uuid.New().String(),
				Provider:         "openai",
				Model:            "gpt-4",
				LatencyMs:        -1,
				PromptTokens:     100,
				CompletionTokens: 200,
				TotalTokens:      300,
			},
			wantErr: true,
			errMsg:  "LatencyMs",
		},
		{
			name: "negative prompt tokens",
			payload: CandidateProducedPayload{
				AnswerID:         uuid.New().String(),
				Provider:         "openai",
				Model:            "gpt-4",
				LatencyMs:        150,
				PromptTokens:     -1,
				CompletionTokens: 200,
				TotalTokens:      300,
			},
			wantErr: true,
			errMsg:  "PromptTokens",
		},
		{
			name: "negative completion tokens",
			payload: CandidateProducedPayload{
				AnswerID:         uuid.New().String(),
				Provider:         "openai",
				Model:            "gpt-4",
				LatencyMs:        150,
				PromptTokens:     100,
				CompletionTokens: -1,
				TotalTokens:      300,
			},
			wantErr: true,
			errMsg:  "CompletionTokens",
		},
		{
			name: "negative total tokens",
			payload: CandidateProducedPayload{
				AnswerID:         uuid.New().String(),
				Provider:         "openai",
				Model:            "gpt-4",
				LatencyMs:        150,
				PromptTokens:     100,
				CompletionTokens: 200,
				TotalTokens:      -1,
			},
			wantErr: true,
			errMsg:  "TotalTokens",
		},
		{
			name: "zero values allowed",
			payload: CandidateProducedPayload{
				AnswerID:         uuid.New().String(),
				Provider:         "openai",
				Model:            "gpt-4",
				LatencyMs:        0,
				PromptTokens:     0,
				CompletionTokens: 0,
				TotalTokens:      0,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.payload.Validate()
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestLLMUsagePayload_Validate(t *testing.T) {
	tests := []struct {
		name    string
		payload LLMUsagePayload
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid payload",
			payload: LLMUsagePayload{
				TotalTokens:        300,
				TotalCalls:         5,
				CostCents:          150,
				Provider:           "openai",
				Models:             []string{"gpt-4", "gpt-3.5-turbo"},
				ProviderRequestIDs: []string{"req-1", "req-2"},
				CacheHit:           false,
			},
			wantErr: false,
		},
		{
			name: "negative total tokens",
			payload: LLMUsagePayload{
				TotalTokens:        -1,
				TotalCalls:         5,
				CostCents:          150,
				Provider:           "openai",
				Models:             []string{"gpt-4"},
				ProviderRequestIDs: []string{"req-1"},
				CacheHit:           false,
			},
			wantErr: true,
			errMsg:  "TotalTokens",
		},
		{
			name: "negative total calls",
			payload: LLMUsagePayload{
				TotalTokens:        300,
				TotalCalls:         -1,
				CostCents:          150,
				Provider:           "openai",
				Models:             []string{"gpt-4"},
				ProviderRequestIDs: []string{"req-1"},
				CacheHit:           false,
			},
			wantErr: true,
			errMsg:  "TotalCalls",
		},
		{
			name: "negative cost cents",
			payload: LLMUsagePayload{
				TotalTokens:        300,
				TotalCalls:         5,
				CostCents:          -1,
				Provider:           "openai",
				Models:             []string{"gpt-4"},
				ProviderRequestIDs: []string{"req-1"},
				CacheHit:           false,
			},
			wantErr: true,
			errMsg:  "CostCents",
		},
		{
			name: "missing provider",
			payload: LLMUsagePayload{
				TotalTokens:        300,
				TotalCalls:         5,
				CostCents:          150,
				Provider:           "",
				Models:             []string{"gpt-4"},
				ProviderRequestIDs: []string{"req-1"},
				CacheHit:           false,
			},
			wantErr: true,
			errMsg:  "Provider",
		},
		{
			name: "empty models slice",
			payload: LLMUsagePayload{
				TotalTokens:        300,
				TotalCalls:         5,
				CostCents:          150,
				Provider:           "openai",
				Models:             []string{},
				ProviderRequestIDs: []string{"req-1"},
				CacheHit:           false,
			},
			wantErr: true,
			errMsg:  "Models",
		},
		{
			name: "nil models slice",
			payload: LLMUsagePayload{
				TotalTokens:        300,
				TotalCalls:         5,
				CostCents:          150,
				Provider:           "openai",
				Models:             nil,
				ProviderRequestIDs: []string{"req-1"},
				CacheHit:           false,
			},
			wantErr: true,
			errMsg:  "Models",
		},
		{
			name: "zero values allowed",
			payload: LLMUsagePayload{
				TotalTokens:        0,
				TotalCalls:         0,
				CostCents:          0,
				Provider:           "openai",
				Models:             []string{"gpt-4"},
				ProviderRequestIDs: nil, // Optional field
				CacheHit:           true,
			},
			wantErr: false,
		},
		{
			name: "cache hit true",
			payload: LLMUsagePayload{
				TotalTokens:        300,
				TotalCalls:         5,
				CostCents:          150,
				Provider:           "openai",
				Models:             []string{"gpt-4"},
				ProviderRequestIDs: []string{"req-1"},
				CacheHit:           true,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.payload.Validate()
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestNewEventEnvelope(t *testing.T) {
	tenantID := uuid.New()
	workflowID := "workflow-123"
	runID := "run-456"
	payload := json.RawMessage(`{"test": "data"}`)
	producer := "test-producer"
	artifactRefs := []string{"ref1", "ref2"}

	tests := []struct {
		name         string
		eventType    EventType
		tenantID     uuid.UUID
		workflowID   string
		runID        string
		payload      json.RawMessage
		producer     string
		artifactRefs []string
	}{
		{
			name:         "candidate produced event",
			eventType:    EventTypeCandidateProduced,
			tenantID:     tenantID,
			workflowID:   workflowID,
			runID:        runID,
			payload:      payload,
			producer:     producer,
			artifactRefs: artifactRefs,
		},
		{
			name:         "LLM usage event",
			eventType:    EventTypeLLMUsage,
			tenantID:     tenantID,
			workflowID:   workflowID,
			runID:        runID,
			payload:      payload,
			producer:     producer,
			artifactRefs: artifactRefs,
		},
		{
			name:         "empty artifact refs",
			eventType:    EventTypeCandidateProduced,
			tenantID:     tenantID,
			workflowID:   workflowID,
			runID:        runID,
			payload:      payload,
			producer:     producer,
			artifactRefs: []string{},
		},
		{
			name:         "nil artifact refs",
			eventType:    EventTypeCandidateProduced,
			tenantID:     tenantID,
			workflowID:   workflowID,
			runID:        runID,
			payload:      payload,
			producer:     producer,
			artifactRefs: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envelope := NewEventEnvelope(
				tt.eventType,
				tt.tenantID,
				tt.workflowID,
				tt.runID,
				tt.payload,
				tt.producer,
				tt.artifactRefs,
			)

			// Verify required fields are set
			assert.Equal(t, tt.eventType, envelope.EventType)
			assert.Equal(t, 1, envelope.Version)
			assert.Equal(t, tt.tenantID, envelope.TenantID)
			assert.Equal(t, tt.workflowID, envelope.WorkflowID)
			assert.Equal(t, tt.runID, envelope.RunID)
			assert.Equal(t, 0, envelope.Sequence)
			assert.Equal(t, tt.payload, envelope.Payload)
			assert.Equal(t, tt.producer, envelope.Producer)
			assert.Equal(t, tt.artifactRefs, envelope.ArtifactRefs)

			// OccurredAt should be set to a reasonable time
			assert.WithinDuration(t, time.Now(), envelope.OccurredAt, time.Second)

			// IdempotencyKey should be empty (not set by constructor)
			assert.Empty(t, envelope.IdempotencyKey)

			// TeamID should be nil (not set by constructor)
			assert.Nil(t, envelope.TeamID)
		})
	}
}

func TestGenerateIdempotencyKey(t *testing.T) {
	tests := []struct {
		name                  string
		clientIdempotencyKey  string
		eventSuffix           string
		wantConsistentResults bool
		wantNonEmpty          bool
	}{
		{
			name:                  "standard inputs",
			clientIdempotencyKey:  "client-key-123",
			eventSuffix:           ":cand:0",
			wantConsistentResults: true,
			wantNonEmpty:          true,
		},
		{
			name:                  "empty client key",
			clientIdempotencyKey:  "",
			eventSuffix:           ":cand:0",
			wantConsistentResults: true,
			wantNonEmpty:          true,
		},
		{
			name:                  "empty suffix",
			clientIdempotencyKey:  "client-key-123",
			eventSuffix:           "",
			wantConsistentResults: true,
			wantNonEmpty:          true,
		},
		{
			name:                  "both empty",
			clientIdempotencyKey:  "",
			eventSuffix:           "",
			wantConsistentResults: true,
			wantNonEmpty:          true,
		},
		{
			name:                  "special characters",
			clientIdempotencyKey:  "client-key-!@#$%^&*()",
			eventSuffix:           ":cand:999",
			wantConsistentResults: true,
			wantNonEmpty:          true,
		},
		{
			name:                  "unicode characters",
			clientIdempotencyKey:  "client-key-你好",
			eventSuffix:           ":cand:0",
			wantConsistentResults: true,
			wantNonEmpty:          true,
		},
		{
			name:                  "very long inputs",
			clientIdempotencyKey:  string(make([]byte, 10000)),
			eventSuffix:           string(make([]byte, 1000)),
			wantConsistentResults: true,
			wantNonEmpty:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Generate key multiple times to test consistency
			key1 := GenerateIdempotencyKey(tt.clientIdempotencyKey, tt.eventSuffix)
			key2 := GenerateIdempotencyKey(tt.clientIdempotencyKey, tt.eventSuffix)

			if tt.wantNonEmpty {
				assert.NotEmpty(t, key1)
			}

			if tt.wantConsistentResults {
				assert.Equal(t, key1, key2, "should generate consistent results")
			}

			// Key should be hex-encoded SHA256 (64 characters)
			assert.Len(t, key1, 64)
			assert.Regexp(t, "^[a-f0-9]{64}$", key1)
		})
	}

	// Test different inputs produce different keys
	t.Run("different inputs produce different keys", func(t *testing.T) {
		key1 := GenerateIdempotencyKey("key1", "suffix1")
		key2 := GenerateIdempotencyKey("key2", "suffix1")
		key3 := GenerateIdempotencyKey("key1", "suffix2")

		assert.NotEqual(t, key1, key2, "different client keys should produce different results")
		assert.NotEqual(t, key1, key3, "different suffixes should produce different results")
		assert.NotEqual(t, key2, key3, "both different should produce different results")
	})
}

func TestCandidateProducedIdempotencyKey(t *testing.T) {
	tests := []struct {
		name                 string
		clientIdempotencyKey string
		index                int
		expectedSuffix       string
	}{
		{
			name:                 "index zero",
			clientIdempotencyKey: "client-key-123",
			index:                0,
			expectedSuffix:       ":cand:0",
		},
		{
			name:                 "positive index",
			clientIdempotencyKey: "client-key-123",
			index:                5,
			expectedSuffix:       ":cand:5",
		},
		{
			name:                 "large index",
			clientIdempotencyKey: "client-key-123",
			index:                999999,
			expectedSuffix:       ":cand:999999",
		},
		{
			name:                 "negative index",
			clientIdempotencyKey: "client-key-123",
			index:                -1,
			expectedSuffix:       ":cand:-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := CandidateProducedIdempotencyKey(tt.clientIdempotencyKey, tt.index)

			// Should match GenerateIdempotencyKey with expected suffix
			expectedKey := GenerateIdempotencyKey(tt.clientIdempotencyKey, tt.expectedSuffix)
			assert.Equal(t, expectedKey, key)

			// Should be valid SHA256 hex
			assert.Len(t, key, 64)
			assert.Regexp(t, "^[a-f0-9]{64}$", key)
		})
	}

	// Test different indices produce different keys
	t.Run("different indices produce different keys", func(t *testing.T) {
		clientKey := "test-client-key"
		key0 := CandidateProducedIdempotencyKey(clientKey, 0)
		key1 := CandidateProducedIdempotencyKey(clientKey, 1)
		key2 := CandidateProducedIdempotencyKey(clientKey, 2)

		assert.NotEqual(t, key0, key1)
		assert.NotEqual(t, key1, key2)
		assert.NotEqual(t, key0, key2)
	})
}

func TestLLMUsageIdempotencyKey(t *testing.T) {
	tests := []struct {
		name                 string
		clientIdempotencyKey string
		expectedSuffix       string
	}{
		{
			name:                 "standard client key",
			clientIdempotencyKey: "client-key-123",
			expectedSuffix:       ":generate:1",
		},
		{
			name:                 "empty client key",
			clientIdempotencyKey: "",
			expectedSuffix:       ":generate:1",
		},
		{
			name:                 "special characters",
			clientIdempotencyKey: "client-key-!@#$",
			expectedSuffix:       ":generate:1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := LLMUsageIdempotencyKey(tt.clientIdempotencyKey)

			// Should match GenerateIdempotencyKey with fixed suffix
			expectedKey := GenerateIdempotencyKey(tt.clientIdempotencyKey, tt.expectedSuffix)
			assert.Equal(t, expectedKey, key)

			// Should be valid SHA256 hex
			assert.Len(t, key, 64)
			assert.Regexp(t, "^[a-f0-9]{64}$", key)
		})
	}

	// Test consistency
	t.Run("consistent results for same input", func(t *testing.T) {
		clientKey := "test-client-key"
		key1 := LLMUsageIdempotencyKey(clientKey)
		key2 := LLMUsageIdempotencyKey(clientKey)
		assert.Equal(t, key1, key2)
	})
}

func TestNewCandidateProducedEvent(t *testing.T) {
	validTenantID := uuid.New()
	validWorkflowID := "workflow-123"
	validRunID := "run-456"
	clientIdempotencyKey := "client-key-123"

	// Create a valid answer for testing
	validAnswer := Answer{
		ID: uuid.New().String(),
		ContentRef: ArtifactRef{
			Key:  "answers/test-answer.txt",
			Size: 100,
			Kind: ArtifactAnswer,
		},
		AnswerProvenance: AnswerProvenance{
			Provider:    "openai",
			Model:       "gpt-4",
			GeneratedAt: time.Now(),
		},
		AnswerUsage: AnswerUsage{
			PromptTokens:     100,
			CompletionTokens: 200,
			TotalTokens:      300,
		},
		AnswerCost: AnswerCost{
			EstimatedCost: 150,
		},
		AnswerState: AnswerState{
			FinishReason: FinishStop,
		},
	}

	tests := []struct {
		name                 string
		tenantID             uuid.UUID
		workflowID           string
		runID                string
		answer               Answer
		clientIdempotencyKey string
		index                int
		wantErr              bool
		errMsg               string
	}{
		{
			name:                 "valid event creation",
			tenantID:             validTenantID,
			workflowID:           validWorkflowID,
			runID:                validRunID,
			answer:               validAnswer,
			clientIdempotencyKey: clientIdempotencyKey,
			index:                0,
			wantErr:              false,
		},
		{
			name:       "answer with missing ID",
			tenantID:   validTenantID,
			workflowID: validWorkflowID,
			runID:      validRunID,
			answer: Answer{
				ID: "", // Invalid
				ContentRef: ArtifactRef{
					Key:  "answers/test-answer.txt",
					Size: 100,
					Kind: ArtifactAnswer,
				},
				AnswerProvenance: AnswerProvenance{
					Provider:    "openai",
					Model:       "gpt-4",
					GeneratedAt: time.Now(),
				},
			},
			clientIdempotencyKey: clientIdempotencyKey,
			index:                0,
			wantErr:              true,
			errMsg:               "invalid candidate produced payload",
		},
		{
			name:       "answer with negative latency",
			tenantID:   validTenantID,
			workflowID: validWorkflowID,
			runID:      validRunID,
			answer: Answer{
				ID: uuid.New().String(),
				ContentRef: ArtifactRef{
					Key:  "answers/test-answer.txt",
					Size: 100,
					Kind: ArtifactAnswer,
				},
				AnswerProvenance: AnswerProvenance{
					Provider:    "openai",
					Model:       "gpt-4",
					GeneratedAt: time.Now(),
				},
				AnswerUsage: AnswerUsage{
					LatencyMillis: -1, // Invalid
					PromptTokens:  100,
				},
			},
			clientIdempotencyKey: clientIdempotencyKey,
			index:                0,
			wantErr:              true,
			errMsg:               "invalid candidate produced payload",
		},
		{
			name:                 "zero tenant ID",
			tenantID:             uuid.UUID{}, // Invalid
			workflowID:           validWorkflowID,
			runID:                validRunID,
			answer:               validAnswer,
			clientIdempotencyKey: clientIdempotencyKey,
			index:                0,
			wantErr:              true,
			errMsg:               "invalid event envelope",
		},
		{
			name:                 "empty workflow ID",
			tenantID:             validTenantID,
			workflowID:           "", // Invalid
			runID:                validRunID,
			answer:               validAnswer,
			clientIdempotencyKey: clientIdempotencyKey,
			index:                0,
			wantErr:              true,
			errMsg:               "invalid event envelope",
		},
		{
			name:                 "empty run ID",
			tenantID:             validTenantID,
			workflowID:           validWorkflowID,
			runID:                "", // Invalid
			answer:               validAnswer,
			clientIdempotencyKey: clientIdempotencyKey,
			index:                0,
			wantErr:              true,
			errMsg:               "invalid event envelope",
		},
		{
			name:                 "negative index",
			tenantID:             validTenantID,
			workflowID:           validWorkflowID,
			runID:                validRunID,
			answer:               validAnswer,
			clientIdempotencyKey: clientIdempotencyKey,
			index:                -1,
			wantErr:              false, // Negative index is allowed
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envelope, err := NewCandidateProducedEvent(
				tt.tenantID,
				tt.workflowID,
				tt.runID,
				tt.answer,
				tt.clientIdempotencyKey,
				tt.index,
			)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Empty(t, envelope.IdempotencyKey, "envelope should be zero value on error")
			} else {
				require.NoError(t, err)

				// Verify envelope structure
				assert.Equal(t, EventTypeCandidateProduced, envelope.EventType)
				assert.Equal(t, 1, envelope.Version)
				assert.Equal(t, tt.tenantID, envelope.TenantID)
				assert.Equal(t, tt.workflowID, envelope.WorkflowID)
				assert.Equal(t, tt.runID, envelope.RunID)
				assert.Equal(t, 0, envelope.Sequence)
				assert.Equal(t, "activity.generate_answers", envelope.Producer)
				assert.Equal(t, []string{tt.answer.ContentRef.Key}, envelope.ArtifactRefs)

				// Verify idempotency key was set
				expectedKey := CandidateProducedIdempotencyKey(tt.clientIdempotencyKey, tt.index)
				assert.Equal(t, expectedKey, envelope.IdempotencyKey)

				// Verify payload content
				var payload CandidateProducedPayload
				err := json.Unmarshal(envelope.Payload, &payload)
				require.NoError(t, err)

				assert.Equal(t, tt.answer.ID, payload.AnswerID)
				assert.Equal(t, tt.answer.Provider, payload.Provider)
				assert.Equal(t, tt.answer.Model, payload.Model)
				assert.Equal(t, tt.answer.LatencyMillis, payload.LatencyMs)
				assert.Equal(t, tt.answer.PromptTokens, payload.PromptTokens)
				assert.Equal(t, tt.answer.CompletionTokens, payload.CompletionTokens)
				assert.Equal(t, tt.answer.TotalTokens, payload.TotalTokens)
				assert.Equal(t, tt.answer.FinishReason, payload.FinishReason)
			}
		})
	}
}

func TestNewLLMUsageEvent(t *testing.T) {
	validTenantID := uuid.New()
	validWorkflowID := "workflow-123"
	validRunID := "run-456"
	clientIdempotencyKey := "client-key-123"
	artifactRefs := []string{"ref1", "ref2"}

	validOutput := &GenerateAnswersOutput{
		Answers: []Answer{
			{
				ID: uuid.New().String(),
				ContentRef: ArtifactRef{
					Key:  "answers/test-answer.txt",
					Size: 100,
					Kind: ArtifactAnswer,
				},
			},
		},
		TokensUsed: 300,
		CallsMade:  5,
		CostCents:  150,
	}

	tests := []struct {
		name                 string
		tenantID             uuid.UUID
		workflowID           string
		runID                string
		output               *GenerateAnswersOutput
		provider             string
		models               []string
		providerRequestIDs   []string
		cacheHit             bool
		clientIdempotencyKey string
		artifactRefs         []string
		wantErr              bool
		errMsg               string
	}{
		{
			name:                 "valid event creation",
			tenantID:             validTenantID,
			workflowID:           validWorkflowID,
			runID:                validRunID,
			output:               validOutput,
			provider:             "openai",
			models:               []string{"gpt-4"},
			providerRequestIDs:   []string{"req-123"},
			cacheHit:             false,
			clientIdempotencyKey: clientIdempotencyKey,
			artifactRefs:         artifactRefs,
			wantErr:              false,
		},
		{
			name:       "output with negative tokens",
			tenantID:   validTenantID,
			workflowID: validWorkflowID,
			runID:      validRunID,
			output: &GenerateAnswersOutput{
				TokensUsed: -1, // Invalid
				CallsMade:  5,
				CostCents:  150,
			},
			provider:             "openai",
			models:               []string{"gpt-4"},
			providerRequestIDs:   []string{"req-123"},
			cacheHit:             false,
			clientIdempotencyKey: clientIdempotencyKey,
			artifactRefs:         artifactRefs,
			wantErr:              true,
			errMsg:               "invalid LLM usage payload",
		},
		{
			name:                 "empty provider",
			tenantID:             validTenantID,
			workflowID:           validWorkflowID,
			runID:                validRunID,
			output:               validOutput,
			provider:             "", // Invalid
			models:               []string{"gpt-4"},
			providerRequestIDs:   []string{"req-123"},
			cacheHit:             false,
			clientIdempotencyKey: clientIdempotencyKey,
			artifactRefs:         artifactRefs,
			wantErr:              true,
			errMsg:               "invalid LLM usage payload",
		},
		{
			name:                 "empty models slice",
			tenantID:             validTenantID,
			workflowID:           validWorkflowID,
			runID:                validRunID,
			output:               validOutput,
			provider:             "openai",
			models:               []string{}, // Invalid
			providerRequestIDs:   []string{"req-123"},
			cacheHit:             false,
			clientIdempotencyKey: clientIdempotencyKey,
			artifactRefs:         artifactRefs,
			wantErr:              true,
			errMsg:               "invalid LLM usage payload",
		},
		{
			name:                 "zero tenant ID",
			tenantID:             uuid.UUID{}, // Invalid
			workflowID:           validWorkflowID,
			runID:                validRunID,
			output:               validOutput,
			provider:             "openai",
			models:               []string{"gpt-4"},
			providerRequestIDs:   []string{"req-123"},
			cacheHit:             false,
			clientIdempotencyKey: clientIdempotencyKey,
			artifactRefs:         artifactRefs,
			wantErr:              true,
			errMsg:               "invalid event envelope",
		},
		{
			name:                 "cache hit true",
			tenantID:             validTenantID,
			workflowID:           validWorkflowID,
			runID:                validRunID,
			output:               validOutput,
			provider:             "openai",
			models:               []string{"gpt-4"},
			providerRequestIDs:   []string{"req-123"},
			cacheHit:             true, // Valid
			clientIdempotencyKey: clientIdempotencyKey,
			artifactRefs:         artifactRefs,
			wantErr:              false,
		},
		{
			name:                 "nil provider request IDs",
			tenantID:             validTenantID,
			workflowID:           validWorkflowID,
			runID:                validRunID,
			output:               validOutput,
			provider:             "openai",
			models:               []string{"gpt-4"},
			providerRequestIDs:   nil, // Optional field
			cacheHit:             false,
			clientIdempotencyKey: clientIdempotencyKey,
			artifactRefs:         artifactRefs,
			wantErr:              false,
		},
		{
			name:                 "multiple models",
			tenantID:             validTenantID,
			workflowID:           validWorkflowID,
			runID:                validRunID,
			output:               validOutput,
			provider:             "openai",
			models:               []string{"gpt-4", "gpt-3.5-turbo", "claude-3"},
			providerRequestIDs:   []string{"req-1", "req-2", "req-3"},
			cacheHit:             false,
			clientIdempotencyKey: clientIdempotencyKey,
			artifactRefs:         artifactRefs,
			wantErr:              false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envelope, err := NewLLMUsageEvent(
				tt.tenantID,
				tt.workflowID,
				tt.runID,
				tt.output,
				tt.provider,
				tt.models,
				tt.providerRequestIDs,
				tt.cacheHit,
				tt.clientIdempotencyKey,
				tt.artifactRefs,
			)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Empty(t, envelope.IdempotencyKey, "envelope should be zero value on error")
			} else {
				require.NoError(t, err)

				// Verify envelope structure
				assert.Equal(t, EventTypeLLMUsage, envelope.EventType)
				assert.Equal(t, 1, envelope.Version)
				assert.Equal(t, tt.tenantID, envelope.TenantID)
				assert.Equal(t, tt.workflowID, envelope.WorkflowID)
				assert.Equal(t, tt.runID, envelope.RunID)
				assert.Equal(t, 0, envelope.Sequence)
				assert.Equal(t, "activity.generate_answers", envelope.Producer)
				assert.Equal(t, tt.artifactRefs, envelope.ArtifactRefs)

				// Verify idempotency key was set
				expectedKey := LLMUsageIdempotencyKey(tt.clientIdempotencyKey)
				assert.Equal(t, expectedKey, envelope.IdempotencyKey)

				// Verify payload content
				var payload LLMUsagePayload
				err := json.Unmarshal(envelope.Payload, &payload)
				require.NoError(t, err)

				assert.Equal(t, tt.output.TokensUsed, payload.TotalTokens)
				assert.Equal(t, tt.output.CallsMade, payload.TotalCalls)
				assert.Equal(t, tt.output.CostCents, payload.CostCents)
				assert.Equal(t, tt.provider, payload.Provider)
				assert.Equal(t, tt.models, payload.Models)
				assert.Equal(t, tt.providerRequestIDs, payload.ProviderRequestIDs)
				assert.Equal(t, tt.cacheHit, payload.CacheHit)
			}
		})
	}
}

// TestNoOpEventSink_Append has been removed since NoOpEventSink is now in pkg/events package

// TestNewNoOpEventSink has been removed since NoOpEventSink is now in pkg/events package

// TestEventSink_Interface has been removed since EventSink interface is now in pkg/events package

// Test edge cases for JSON marshaling in constructor functions
func TestEventCreation_JSONMarshaling(t *testing.T) {
	t.Run("payload marshaling preserves data", func(t *testing.T) {
		// Create answer with specific data
		answer := Answer{
			ID: uuid.New().String(),
			ContentRef: ArtifactRef{
				Key:  "answers/test-answer.txt",
				Size: 100,
				Kind: ArtifactAnswer,
			},
			AnswerProvenance: AnswerProvenance{
				Provider:    "openai",
				Model:       "gpt-4",
				GeneratedAt: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
			},
			AnswerUsage: AnswerUsage{
				LatencyMillis:    150,
				PromptTokens:     100,
				CompletionTokens: 200,
				TotalTokens:      300,
			},
			AnswerCost: AnswerCost{
				EstimatedCost: 75,
			},
			AnswerState: AnswerState{
				FinishReason: FinishLength,
			},
		}

		envelope, err := NewCandidateProducedEvent(
			uuid.New(),
			"workflow-123",
			"run-456",
			answer,
			"client-key-123",
			0,
		)
		require.NoError(t, err)

		// Unmarshal and verify all fields were preserved
		var payload CandidateProducedPayload
		err = json.Unmarshal(envelope.Payload, &payload)
		require.NoError(t, err)

		assert.Equal(t, answer.ID, payload.AnswerID)
		assert.Equal(t, answer.Provider, payload.Provider)
		assert.Equal(t, answer.Model, payload.Model)
		assert.Equal(t, answer.LatencyMillis, payload.LatencyMs)
		assert.Equal(t, answer.PromptTokens, payload.PromptTokens)
		assert.Equal(t, answer.CompletionTokens, payload.CompletionTokens)
		assert.Equal(t, answer.TotalTokens, payload.TotalTokens)
		assert.Equal(t, answer.FinishReason, payload.FinishReason)
	})

	t.Run("usage payload marshaling preserves data", func(t *testing.T) {
		output := &GenerateAnswersOutput{
			TokensUsed: 42000,
			CallsMade:  25,
			CostCents:  999,
		}

		envelope, err := NewLLMUsageEvent(
			uuid.New(),
			"workflow-123",
			"run-456",
			output,
			"anthropic",
			[]string{"claude-3", "claude-2"},
			[]string{"req-1", "req-2", "req-3"},
			true,
			"client-key-123",
			[]string{"artifact-1", "artifact-2"},
		)
		require.NoError(t, err)

		// Unmarshal and verify all fields were preserved
		var payload LLMUsagePayload
		err = json.Unmarshal(envelope.Payload, &payload)
		require.NoError(t, err)

		assert.Equal(t, output.TokensUsed, payload.TotalTokens)
		assert.Equal(t, output.CallsMade, payload.TotalCalls)
		assert.Equal(t, output.CostCents, payload.CostCents)
		assert.Equal(t, "anthropic", payload.Provider)
		assert.Equal(t, []string{"claude-3", "claude-2"}, payload.Models)
		assert.Equal(t, []string{"req-1", "req-2", "req-3"}, payload.ProviderRequestIDs)
		assert.Equal(t, true, payload.CacheHit)
	})
}

// Test boundary conditions for numeric fields
func TestEventCreation_BoundaryConditions(t *testing.T) {
	t.Run("maximum values", func(t *testing.T) {
		answer := Answer{
			ID: uuid.New().String(),
			ContentRef: ArtifactRef{
				Key:  "answers/test-answer.txt",
				Size: 100,
				Kind: ArtifactAnswer,
			},
			AnswerProvenance: AnswerProvenance{
				Provider:    "openai",
				Model:       "gpt-4",
				GeneratedAt: time.Now(),
			},
			AnswerUsage: AnswerUsage{
				LatencyMillis:    9223372036854775807, // Max int64
				PromptTokens:     9223372036854775807,
				CompletionTokens: 9223372036854775807,
				TotalTokens:      9223372036854775807,
			},
			AnswerCost: AnswerCost{
				EstimatedCost: 9223372036854775807,
			},
			AnswerState: AnswerState{
				FinishReason: FinishStop,
			},
		}

		envelope, err := NewCandidateProducedEvent(
			uuid.New(),
			"workflow-123",
			"run-456",
			answer,
			"client-key-123",
			9223372036854775807, // Max int
		)
		require.NoError(t, err)

		var payload CandidateProducedPayload
		err = json.Unmarshal(envelope.Payload, &payload)
		require.NoError(t, err)

		assert.Equal(t, answer.LatencyMillis, payload.LatencyMs)
		assert.Equal(t, answer.PromptTokens, payload.PromptTokens)
		assert.Equal(t, answer.CompletionTokens, payload.CompletionTokens)
		assert.Equal(t, answer.TotalTokens, payload.TotalTokens)
	})

	t.Run("zero values", func(t *testing.T) {
		output := &GenerateAnswersOutput{
			TokensUsed: 0,
			CallsMade:  0,
			CostCents:  0,
		}

		envelope, err := NewLLMUsageEvent(
			uuid.New(),
			"workflow-123",
			"run-456",
			output,
			"openai",
			[]string{"gpt-4"},
			[]string{},
			false,
			"client-key-123",
			[]string{},
		)
		require.NoError(t, err)

		var payload LLMUsagePayload
		err = json.Unmarshal(envelope.Payload, &payload)
		require.NoError(t, err)

		assert.Equal(t, int64(0), payload.TotalTokens)
		assert.Equal(t, int64(0), payload.TotalCalls)
		assert.Equal(t, Cents(0), payload.CostCents)
		assert.Equal(t, false, payload.CacheHit)
	})
}
