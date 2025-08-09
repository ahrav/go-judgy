package activity

import (
	"context"
	"fmt"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm"
)

// Activities provides activity functions with proper dependency injection.
// Holds LLM client and other dependencies needed for Temporal activities.
type Activities struct{ llmClient llm.Client }

// NewActivities creates a new Activities instance with the provided LLM client.
// Used for both production (with real client) and testing (with mock client).
func NewActivities(client llm.Client) *Activities {
	return &Activities{llmClient: client}
}

// InitializeLLMClient creates an LLM client with comprehensive configuration.
// Returns the client for dependency injection rather than setting global state.
// Must be called during worker startup to establish the client with middleware
// pipeline including caching, circuit breaking, rate limiting, and observability.
func InitializeLLMClient(cfg *llm.Config) (llm.Client, error) {
	if cfg == nil {
		cfg = llm.DefaultConfig()
	}

	client, err := llm.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize LLM client: %w", err)
	}

	return client, nil
}

// GenerateAnswers produces candidate answers using configured LLM providers.
// Processes generation requests through the LLM client with comprehensive
// resilience patterns including idempotency, caching, rate limiting,
// circuit breaking, retry logic, and cost tracking for production reliability.
func (a *Activities) GenerateAnswers(
	ctx context.Context,
	input domain.GenerateAnswersInput,
) (*domain.GenerateAnswersOutput, error) {
	if err := input.Validate(); err != nil {
		return nil, nonRetryable("GenerateAnswers", err, "invalid input")
	}

	output, err := a.llmClient.Generate(ctx, input)
	if err != nil {
		if wfErr := llm.ClassifyLLMError(err); wfErr != nil && wfErr.ShouldRetry() {
			return nil, retryable("GenerateAnswers", err, wfErr.Message)
		}
		return nil, nonRetryable("GenerateAnswers", err, "generation failed")
	}

	if err := output.Validate(); err != nil {
		return nil, nonRetryable("GenerateAnswers", err, "invalid output")
	}

	return output, nil
}
