package llm

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ahrav/go-judgy/internal/domain"
)

// responseToAnswer converts an LLM response to a domain Answer.
func responseToAnswer(resp *Request, req *LLMRequest) *domain.Answer {
	id := uuid.New().String()

	contentRef := domain.ArtifactRef{
		Key:  fmt.Sprintf("answers/%s/%s.txt", time.Now().Format("2006/01"), id),
		Size: int64(len(resp.Content)),
		Kind: domain.ArtifactAnswer,
	}

	answer := &domain.Answer{
		ID:         id,
		ContentRef: contentRef,
		AnswerProvenance: domain.AnswerProvenance{
			Provider:           req.Provider,
			Model:              req.Model,
			GeneratedAt:        time.Now(),
			TraceID:            req.TraceID,
			ProviderRequestIDs: resp.ProviderRequestIDs,
		},
		AnswerUsage: domain.AnswerUsage{
			LatencyMillis:    resp.Usage.LatencyMs,
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
			CallsUsed:        1,
		},
		AnswerCost: domain.AnswerCost{
			EstimatedCost: domain.Cents(resp.EstimatedCostMilliCents / 1000), // Convert to cents for domain precision.
		},
		AnswerState: domain.AnswerState{
			FinishReason: resp.FinishReason,
			Truncated:    resp.FinishReason == domain.FinishLength,
			RetryCount:   0, // Updated by retry middleware during processing.
		},
	}

	return answer
}

// responseToScore converts an LLM response to a domain Score.
func responseToScore(resp *Request, answerID string, req *LLMRequest, disableJSONRepair bool) (*domain.Score, error) {
	scoreData, err := ValidateAndRepairScore(resp.Content, !disableJSONRepair)
	if err != nil {
		return nil, fmt.Errorf("invalid score response: %w", err)
	}

	id := uuid.New().String()

	reasonRef := domain.ArtifactRef{
		Key:  fmt.Sprintf("rationales/%s/%s.txt", time.Now().Format("2006/01"), id),
		Size: int64(len(scoreData.Reasoning)),
		Kind: domain.ArtifactJudgeRationale,
	}

	score := &domain.Score{
		ID:         id,
		AnswerID:   answerID,
		Value:      scoreData.Value,
		Confidence: scoreData.Confidence,
		ScoreEvidence: domain.ScoreEvidence{
			ReasonRef:  reasonRef,
			Dimensions: scoreData.Dimensions,
		},
		ScoreProvenance: domain.ScoreProvenance{
			JudgeID:  fmt.Sprintf("%s-%s", req.Provider, req.Model),
			Provider: req.Provider,
			Model:    req.Model,
			ScoredAt: time.Now(),
		},
		ScoreUsage: domain.ScoreUsage{
			LatencyMs:  resp.Usage.LatencyMs,
			TokensUsed: resp.Usage.TotalTokens,
			CallsUsed:  1,
			CostCents:  domain.Cents(resp.EstimatedCostMilliCents / 1000), // Convert for domain precision.
		},
		ScoreValidity: domain.ScoreValidity{
			Valid: true,
		},
	}

	return score, nil
}

// createInvalidScore creates a score marked as invalid due to an error.
func createInvalidScore(answerID string, err error) *domain.Score {
	id := uuid.New().String()

	return &domain.Score{
		ID:         id,
		AnswerID:   answerID,
		Value:      0,
		Confidence: 0,
		ScoreEvidence: domain.ScoreEvidence{
			ReasonRef: domain.ArtifactRef{
				Key:  fmt.Sprintf("errors/%s/%s.txt", time.Now().Format("2006/01"), id),
				Size: 0,
				Kind: domain.ArtifactJudgeRationale,
			},
		},
		ScoreProvenance: domain.ScoreProvenance{
			JudgeID:  "error",
			Provider: "error",
			Model:    "error",
			ScoredAt: time.Now(),
		},
		ScoreValidity: domain.ScoreValidity{
			Valid: false,
			Error: err.Error(),
		},
	}
}

// extractTenantID retrieves tenant identifier from request context.
// Returns "default" as placeholder until context-based tenant extraction
// is implemented with authentication integration.
func extractTenantID(_ context.Context) string {
	// TODO: Extract from context or auth
	return "default"
}

// extractTraceID retrieves or generates trace identifier for request correlation.
// Returns generated UUID as placeholder until context-based trace extraction
// is implemented with distributed tracing integration.
func extractTraceID(_ context.Context) string {
	// TODO: Extract from context or generate
	return uuid.New().String()
}
