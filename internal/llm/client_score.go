package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// Token and scoring constants.
const (
	DefaultMaxTokens          = 1000
	DefaultScoringTemperature = 0.1
)

// Score implements Client.Score with validator injection and repair capabilities.
// Processes scoring requests through the middleware pipeline with automatic
// JSON validation using injected validators and structured error handling for judge responses.
// Executes transport-level repairs if enabled, then domain-level validation via injected validator.
func (c *client) Score(ctx context.Context, in domain.ScoreAnswersInput) (*domain.ScoreAnswersOutput, error) {
	output := &domain.ScoreAnswersOutput{
		Scores: make([]domain.Score, 0, len(in.Answers)),
	}

	for _, answer := range in.Answers {
		req := &transport.Request{
			Operation:     transport.OpScoring,
			Provider:      in.Config.Provider,
			Model:         in.Config.Model,
			TenantID:      transport.ExtractTenantID(ctx),
			Question:      in.Question,
			Answers:       []domain.Answer{answer},
			MaxTokens:     DefaultMaxTokens,          // Scoring typically needs less tokens
			Temperature:   DefaultScoringTemperature, // Low temperature for consistent scoring
			Timeout:       time.Duration(in.Config.Timeout) * time.Second,
			TraceID:       transport.ExtractTraceID(ctx),
			ArtifactStore: newArtifactStoreAdapter(c.artifactStore),
		}

		key, err := transport.GenerateIdemKey(req)
		if err != nil {
			score := transport.CreateInvalidScore(answer.ID, fmt.Errorf("failed to generate idempotency key: %w", err))
			output.Scores = append(output.Scores, *score)
			continue
		}
		req.IdempotencyKey = key.String()

		resp, err := c.handler.Handle(ctx, req)
		if err != nil {
			score := transport.CreateInvalidScore(answer.ID, err)
			output.Scores = append(output.Scores, *score)
			continue
		}

		score, err := c.processScoreWithValidator(resp, answer.ID, req, in.Validator, in.RepairPolicy)
		if err != nil {
			score = transport.CreateInvalidScore(answer.ID, err)
		}

		output.Scores = append(output.Scores, *score)

		output.TokensUsed += resp.Usage.TotalTokens
		output.CallsMade++
	}

	return output, nil
}

// processScoreWithValidator applies validator injection pattern for clean architecture.
// Handles transport-level repairs if enabled, then executes injected domain validator if provided.
// Falls back to existing business validation if no validator is injected.
func (c *client) processScoreWithValidator(
	resp *transport.Response,
	answerID string,
	req *transport.Request,
	validator domain.ScoreValidator,
	repairPolicy *domain.RepairPolicy,
) (*domain.Score, error) {
	content := resp.Content

	if repairPolicy != nil && repairPolicy.AllowTransportRepairs {
		content = c.applyTransportRepairs(content)
	}

	if validator != nil {
		return c.createScoreWithDomainValidator(resp, answerID, req, content, validator)
	}

	scoreValidator := newBusinessScoreValidator()
	enableRepair := repairPolicy == nil || repairPolicy.MaxAttempts > 0

	return c.createScoreWithBusinessValidator(resp, answerID, req, content, scoreValidator, enableRepair)
}

// createScoreWithDomainValidator creates a score using the injected domain validator.
func (c *client) createScoreWithDomainValidator(
	resp *transport.Response,
	answerID string,
	req *transport.Request,
	content string,
	validator domain.ScoreValidator,
) (*domain.Score, error) {
	normalizedJSON, repaired, err := validator.Validate([]byte(content))
	if err != nil {
		return transport.CreateInvalidScore(answerID, err), nil
	}

	var scoreData struct {
		Value      float64                 `json:"value"`
		Confidence float64                 `json:"confidence"`
		Reasoning  string                  `json:"reasoning"`
		Dimensions []domain.DimensionScore `json:"dimensions,omitempty"`
	}

	if err := json.Unmarshal(normalizedJSON, &scoreData); err != nil {
		return transport.CreateInvalidScore(answerID, fmt.Errorf("failed to parse validated JSON: %w", err)), nil
	}

	scoreID := uuid.New().String()
	provenance := domain.ScoreProvenance{
		JudgeID:            fmt.Sprintf("%s-%s", req.Provider, req.Model),
		Provider:           req.Provider,
		Model:              req.Model,
		ProviderRequestIDs: resp.ProviderRequestIDs,
		ScoredAt:           time.Now(),
	}

	evidence := domain.ScoreEvidence{
		InlineReasoning: scoreData.Reasoning, // Use inline reasoning, scoring activities will handle blob-if-large
		Dimensions:      scoreData.Dimensions,
	}

	score, err := domain.MakeScore(scoreID, answerID, time.Now(), provenance, evidence)
	if err != nil {
		return transport.CreateInvalidScore(answerID, fmt.Errorf("failed to create score: %w", err)), nil
	}

	score.SetScoreValues(scoreData.Value, scoreData.Confidence)

	score.ScoreUsage = domain.ScoreUsage{
		LatencyMs:  resp.Usage.LatencyMs,
		TokensUsed: resp.Usage.TotalTokens,
		CallsUsed:  1,
		CostCents:  domain.Cents(resp.EstimatedCostMilliCents / 1000), // Convert to cents
	}

	if repaired {
		// In a full implementation, we might want to track repair metrics here
		// For now, the repair fact is captured in the validator's return value
		// TODO: Add repair tracking
	}

	return score, nil
}

// createScoreWithBusinessValidator creates a score using the existing business validator.
func (c *client) createScoreWithBusinessValidator(
	resp *transport.Response,
	answerID string,
	req *transport.Request,
	content string,
	validator transport.ScoreValidator,
	enableRepair bool,
) (*domain.Score, error) {
	scoreData, err := validator.ValidateAndRepairScore(content, enableRepair)
	if err != nil {
		return transport.CreateInvalidScore(answerID, err), nil
	}

	scoreID := uuid.New().String()
	provenance := domain.ScoreProvenance{
		JudgeID:            fmt.Sprintf("%s-%s", req.Provider, req.Model),
		Provider:           req.Provider,
		Model:              req.Model,
		ProviderRequestIDs: resp.ProviderRequestIDs,
		ScoredAt:           time.Now(),
	}

	evidence := domain.ScoreEvidence{
		InlineReasoning: scoreData.Reasoning, // Use inline reasoning, scoring activities will handle blob-if-large
		Dimensions:      scoreData.Dimensions,
	}

	score, err := domain.MakeScore(scoreID, answerID, time.Now(), provenance, evidence)
	if err != nil {
		return transport.CreateInvalidScore(answerID, fmt.Errorf("failed to create score: %w", err)), nil
	}

	score.SetScoreValues(scoreData.Value, scoreData.Confidence)

	score.ScoreUsage = domain.ScoreUsage{
		LatencyMs:  resp.Usage.LatencyMs,
		TokensUsed: resp.Usage.TotalTokens,
		CallsUsed:  1,
		CostCents:  domain.Cents(resp.EstimatedCostMilliCents / 1000), // Convert to cents
	}

	return score, nil
}

// applyTransportRepairs applies minimal transport-level fixes such as removing
// markdown code fences and fixing trailing commas before domain validation.
// TODO: Can this be optimized?
func (c *client) applyTransportRepairs(content string) string {
	repaired := content

	// Remove markdown code fences (common in LLM responses).
	repaired = strings.TrimPrefix(repaired, "```json")
	repaired = strings.TrimPrefix(repaired, "```")
	repaired = strings.TrimSuffix(repaired, "```")

	// Fix trailing commas before closing braces/brackets.
	repaired = regexp.MustCompile(`,\s*([}]])`).ReplaceAllString(repaired, `$1`)

	// Trim whitespace.
	repaired = strings.TrimSpace(repaired)

	return repaired
}
