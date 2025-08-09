package activity

import (
	"context"

	"github.com/ahrav/go-judgy/internal/domain"
	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
)

// ScoreAnswers evaluates candidate answers using configured judge models.
// Processes answers through the LLM client with comprehensive error handling,
// JSON validation and repair, and all resilience patterns including idempotency,
// caching, rate limiting, circuit breaking, and retry logic.
func (a *Activities) ScoreAnswers(
	ctx context.Context,
	input domain.ScoreAnswersInput,
) (*domain.ScoreAnswersOutput, error) {
	if err := input.Validate(); err != nil {
		return nil, nonRetryable("ScoreAnswers", err, "invalid input")
	}

	output, err := a.llmClient.Score(ctx, input)
	if err != nil {
		if wfErr := llmerrors.ClassifyLLMError(err); wfErr != nil && wfErr.ShouldRetry() {
			return nil, retryable("ScoreAnswers", err, wfErr.Message)
		}
		return nil, nonRetryable("ScoreAnswers", err, "scoring failed")
	}

	if err := output.Validate(); err != nil {
		return nil, nonRetryable("ScoreAnswers", err, "invalid output")
	}

	return output, nil
}
