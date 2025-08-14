package scoring

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ahrav/go-judgy/internal/domain"
)

// ErrNotImplemented indicates a method has not been implemented yet.
var ErrNotImplemented = errors.New("not implemented")

// ScorePlan defines the expected behavior for scoring a single answer.
// This enables table-driven testing with per-answer control over responses,
// errors, metadata, and resource usage.
type ScorePlan struct {
	// AnswerID identifies which answer this plan applies to
	AnswerID string

	// Result is the domain.Score to return (nil for errors)
	Result *domain.Score

	// RawJSON is the raw JSON response for validator testing
	// If set, the mock will simulate validator behavior
	RawJSON string

	// Error specifies what error to return (mutually exclusive with Result)
	Error error

	// ErrorType classifies the error for retry behavior testing
	ErrorType string // "retryable", "non-retryable", or "transport"

	// Usage tracking for resource accounting tests
	Usage domain.ScoreUsage

	// Provider metadata for correlation testing
	Provider   string
	Model      string
	RequestIDs []string

	// CostCents for cost calculation testing
	CostCents domain.Cents

	// LatencyMs for latency tracking testing
	LatencyMs int64
}

// scriptedMockClient provides per-answer scriptable LLM client behavior.
// It enables comprehensive testing of all scoring scenarios including
// partial failures, repair logic, and resource tracking.
type scriptedMockClient struct {
	// Configuration
	plans []ScorePlan

	// State tracking (atomic for thread safety)
	currentScoreIndex int32
	generateCalls     int64
	scoreCalls        int64

	// Validator tracking for injection verification (protected by mutex)
	mu               sync.Mutex
	lastValidator    domain.ScoreValidator
	lastRepairPolicy *domain.RepairPolicy
}

// mockLLMClient provides simple controllable LLM client behavior for basic testing.
// Kept for backward compatibility with existing tests.
type mockLLMClient struct {
	// Control behavior
	generateReturnsError bool
	scoreReturnsError    bool
	generateError        error
	scoreError           error

	// Track calls (atomic for thread safety)
	generateCalls int64
	scoreCalls    int64
}

// Generate simulates LLM answer generation.
// Returns configured errors or minimal valid responses for testing activity logic.
func (m *mockLLMClient) Generate(
	ctx context.Context, input domain.GenerateAnswersInput,
) (*domain.GenerateAnswersOutput, error) {
	atomic.AddInt64(&m.generateCalls, 1)

	if m.generateReturnsError {
		if m.generateError != nil {
			return nil, m.generateError
		}
		// Return the expected "not implemented" error for backward compatibility
		return nil, fmt.Errorf("GenerateAnswers not implemented: %w", ErrNotImplemented)
	}

	// This shouldn't be called in scoring tests, but provide a valid response anyway
	return &domain.GenerateAnswersOutput{
		Answers: []domain.Answer{
			{
				ID: "test-answer-1",
				ContentRef: domain.ArtifactRef{
					Key:  "test-answer-key",
					Kind: domain.ArtifactAnswer,
				},
			},
		},
		TokensUsed:    100,
		CallsMade:     1,
		CostCents:     domain.Cents(25),
		ClientIdemKey: "test-idem-key",
	}, nil
}

// Score simulates LLM answer scoring with controllable error behavior.
// Returns configurable errors or minimal valid scores for testing activity logic.
func (m *mockLLMClient) Score(_ context.Context, in domain.ScoreAnswersInput) (*domain.ScoreAnswersOutput, error) {
	atomic.AddInt64(&m.scoreCalls, 1)

	if m.scoreReturnsError {
		if m.scoreError != nil {
			return nil, m.scoreError
		}
		// Return the expected "not implemented" error for backward compatibility
		return nil, fmt.Errorf("ScoreAnswers not implemented: %w", ErrNotImplemented)
	}

	// Return a valid output with minimal data
	scores := make([]domain.Score, 0, len(in.Answers))
	for _, answer := range in.Answers {
		scores = append(scores, domain.Score{
			ID:         fmt.Sprintf("score-%s", answer.ID),
			AnswerID:   answer.ID,
			Value:      0.75,
			Confidence: 0.85,
			ScoreEvidence: domain.ScoreEvidence{
				InlineReasoning: "Mock reasoning",
			},
			ScoreProvenance: domain.ScoreProvenance{
				JudgeID:  "mock-judge",
				Provider: "mock",
				Model:    "mock-model",
				ScoredAt: time.Now(),
			},
			ScoreUsage: domain.ScoreUsage{
				LatencyMs:  100,
				TokensUsed: 50,
			},
			ScoreValidity: domain.ScoreValidity{
				Valid: true,
			},
		})
	}

	return &domain.ScoreAnswersOutput{
		Scores:     scores,
		TokensUsed: 50,
		CallsMade:  1,
	}, nil
}

// GetCallCounts returns the number of calls made for testing verification.
func (m *mockLLMClient) GetCallCounts() (generate, score int64) {
	return atomic.LoadInt64(&m.generateCalls), atomic.LoadInt64(&m.scoreCalls)
}

// newMockLLMClient creates a mock client configured to return "not implemented" errors.
// This supports testing stub activity behavior during development phases.
func newMockLLMClient() *mockLLMClient {
	return &mockLLMClient{
		generateReturnsError: true,
		scoreReturnsError:    true,
	}
}

// newScriptedMockClient creates a scriptable mock client with the given plans.
// Plans are executed in sequence for each scoring request.
func newScriptedMockClient(plans ...ScorePlan) *scriptedMockClient {
	return &scriptedMockClient{
		plans: plans,
	}
}

// Generate simulates LLM answer generation for scriptedMockClient.
// Always returns "not implemented" error as generation is not our focus.
func (m *scriptedMockClient) Generate(
	ctx context.Context, input domain.GenerateAnswersInput,
) (*domain.GenerateAnswersOutput, error) {
	atomic.AddInt64(&m.generateCalls, 1)
	return nil, fmt.Errorf("GenerateAnswers not implemented: %w", ErrNotImplemented)
}

// Score simulates LLM answer scoring with scriptable per-answer behavior.
// Executes plans in sequence to enable complex testing scenarios.
// Note: Since the activity processes answers individually, this mock should only
// receive single-answer requests and should return the appropriate result or error
// for that single answer based on the configured plan.
func (m *scriptedMockClient) Score(ctx context.Context, input domain.ScoreAnswersInput) (*domain.ScoreAnswersOutput, error) {
	atomic.AddInt64(&m.scoreCalls, 1)

	// Store validator and repair policy for verification
	m.mu.Lock()
	m.lastValidator = input.Validator
	m.lastRepairPolicy = input.RepairPolicy
	m.mu.Unlock()

	// The activity calls this with single answers, so we should only get 1 answer
	if len(input.Answers) != 1 {
		return nil, fmt.Errorf("scriptedMockClient expects single answer, got %d", len(input.Answers))
	}

	answer := input.Answers[0]
	plan := m.findPlanForAnswer(answer.ID)
	if plan == nil {
		// No plan found - return default error
		return nil, fmt.Errorf("no plan configured for answer %s", answer.ID)
	}

	// Handle error case - return the error as specified in the plan
	if plan.Error != nil {
		// For non-retryable errors, return as-is
		// For retryable/transport errors, return the plain error
		// The Activities will classify it and wrap it appropriately
		return nil, plan.Error
	}

	// Handle success case with result
	if plan.Result != nil {
		score := *plan.Result

		// Override usage data from plan if explicitly provided
		// We detect explicit provision by checking if the plan was configured with WithUsage
		// by looking if UsageSet flag (we need to add) or any non-default usage values
		score.ScoreUsage = plan.Usage
		score.CostCents = plan.CostCents
		if plan.LatencyMs > 0 {
			score.LatencyMs = plan.LatencyMs
		}

		// Override provenance data from plan if provided
		if plan.Provider != "" {
			score.Provider = plan.Provider
		}
		if plan.Model != "" {
			score.Model = plan.Model
		}

		// Ensure IDs pass domain validation. If non-UUID provided in tests like "answer-1",
		// keep as-is to satisfy tests that assert those exact IDs.

		return &domain.ScoreAnswersOutput{
			Scores:     []domain.Score{score},
			TokensUsed: score.TokensUsed,
			CallsMade:  score.CallsUsed,
			CostCents:  score.CostCents,
		}, nil
	}

	// No result and no error - should not happen
	return nil, fmt.Errorf("plan for answer %s has neither result nor error", answer.ID)
}

// findPlanForAnswer locates the plan for the given answer ID.
// Returns nil if no plan is configured for the answer.
func (m *scriptedMockClient) findPlanForAnswer(answerID string) *ScorePlan {
	for i := range m.plans {
		if m.plans[i].AnswerID == answerID {
			return &m.plans[i]
		}
	}
	return nil
}

// GetCallCounts returns the number of calls made for testing verification.
func (m *scriptedMockClient) GetCallCounts() (generate, score int64) {
	return atomic.LoadInt64(&m.generateCalls), atomic.LoadInt64(&m.scoreCalls)
}

// GetLastValidator returns the last validator that was passed in for verification.
func (m *scriptedMockClient) GetLastValidator() domain.ScoreValidator {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastValidator
}

// GetLastRepairPolicy returns the last repair policy that was passed in for verification.
func (m *scriptedMockClient) GetLastRepairPolicy() *domain.RepairPolicy {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastRepairPolicy
}
