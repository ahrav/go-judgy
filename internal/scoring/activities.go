package scoring

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm"
	"github.com/ahrav/go-judgy/internal/llm/business"
	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
	pkgactivity "github.com/ahrav/go-judgy/pkg/activity"
)

// ProgressReporter is a function type for reporting progress during long-running operations.
// This abstraction allows clean separation between business logic (progress reporting)
// and infrastructure concerns (heartbeat implementation).
type ProgressReporter func(message string)

// NewTemporalProgressReporter creates a ProgressReporter that converts progress messages
// to Temporal heartbeats. This is the production implementation used in workflows.
func NewTemporalProgressReporter(
	ctx context.Context, baseActivities pkgactivity.BaseActivities,
) ProgressReporter {
	return func(message string) {
		baseActivities.RecordHeartbeat(ctx, message)
	}
}

// Activities handles scoring-specific Temporal activities for answer evaluation.
// It orchestrates judge model scoring, blob storage management, and event emission
// within Temporal workflow contexts.
//
// The Activities struct coordinates multiple concerns:
//   - LLM client interaction for judge model scoring
//   - Artifact storage for large rationale text (blob-if-large policy)
//   - Event emission for observability and cost tracking
//   - Error classification for proper Temporal retry behavior
//   - Partial failure handling with graceful degradation
//
// All operations are designed for idempotency and handle both individual
// and batch scoring scenarios with comprehensive resilience patterns.
type Activities struct {
	pkgactivity.BaseActivities
	llmClient        llm.Client
	artifactStore    business.ArtifactStore // For blob-if-large rationale storage
	events           *EventEmitter
	blobThreshold    int              // Configurable threshold for blob vs inline storage
	progressReporter ProgressReporter // For progress reporting during long operations
}

// NewActivities creates a new Activities instance with dependency injection.
// The constructor validates the blob threshold and initializes event emission.
//
// Dependencies:
//   - base: Common Temporal activity infrastructure
//   - client: LLM client for judge model interaction
//   - store: Artifact storage for large rationale text
//   - blobThreshold: Size threshold for blob vs inline storage
//   - progressReporter: Function for reporting progress during long operations
//
// Returns a fully configured Activities instance ready for workflow registration.
func NewActivities(
	base pkgactivity.BaseActivities,
	client llm.Client,
	store business.ArtifactStore,
	blobThreshold int,
	progressReporter ProgressReporter,
) *Activities {
	// Clamp blob threshold to domain-defined acceptable bounds for consistency.
	validThreshold, _ := domain.ValidateBlobThreshold(blobThreshold)

	return &Activities{
		BaseActivities:   base,
		llmClient:        client,
		artifactStore:    store,
		events:           NewEventEmitter(base),
		blobThreshold:    validThreshold,
		progressReporter: progressReporter,
	}
}

// ScoreAnswers evaluates candidate answers using configured judge models.
// This activity implements batched scoring with strict JSON schema enforcement,
// blob-if-large rationale storage, partial failure handling, and comprehensive
// resilience patterns including idempotency, caching, rate limiting, circuit breaking, and retry logic.
//
// The operation:
// 1. Validates input parameters
// 2. Processes answers in batch with individual error handling
// 3. Calls LLM client for judge scoring with schema validation
// 4. Applies blob-if-large policy for rationale storage
// 5. Handles partial failures gracefully
// 6. Emits events for observability and analytics
// 7. Returns structured scoring results with usage metrics
//
// Returns non-retryable errors for validation failures and retryable errors
// for transient provider issues. Uses heartbeats for long-running operations.
func (a *Activities) ScoreAnswers(
	ctx context.Context,
	input domain.ScoreAnswersInput,
) (*domain.ScoreAnswersOutput, error) {
	if err := input.Validate(); err != nil {
		return nil, nonRetryable("ScoreAnswers", err, "invalid input")
	}

	wfCtx := a.GetWorkflowContext(ctx)
	pkgactivity.SafeLog(ctx, "Starting ScoreAnswers activity",
		"workflow_id", wfCtx.WorkflowID,
		"activity_id", wfCtx.ActivityID,
		"answers_to_score", len(input.Answers),
		"blob_threshold", a.blobThreshold)

	output, err := a.processBatchScoring(ctx, input, wfCtx)
	if err != nil {
		return nil, err // Already wrapped with appropriate retry classification.
	}

	if err := output.Validate(); err != nil {
		return nil, nonRetryable("ScoreAnswers", err, "invalid output")
	}

	// Emit scoring events for observability - best-effort, doesn't fail activity.
	a.emitScoringEvents(ctx, output, wfCtx)

	pkgactivity.SafeLog(ctx, "ScoreAnswers completed",
		"scores_generated", len(output.Scores),
		"valid_scores", countValidScores(output.Scores),
		"tokens_used", output.TokensUsed,
		"cost_cents", output.CostCents)

	return output, nil
}

// processBatchScoring processes answers individually to enable partial failure handling.
// Returns aggregated results even if some individual scores fail.
// Uses heartbeats for long-running batch operations.
// Implements bounded concurrency while preserving output order and stable,
// index-based idempotency keys.
func (a *Activities) processBatchScoring(
	ctx context.Context,
	input domain.ScoreAnswersInput,
	wfCtx pkgactivity.WorkflowContext,
) (*domain.ScoreAnswersOutput, error) {
	// Bounded concurrency to optimize throughput without overwhelming provider/middleware.
	const maxConcurrency = 10
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup

	// Pre-allocate scores slice to preserve order deterministically.
	orderedScores := make([]domain.Score, len(input.Answers))

	for i, answer := range input.Answers {
		// Respect cancellation before launching work.
		select {
		case <-ctx.Done():
			return nil, retryable("ScoreAnswers", ctx.Err(), "context cancelled")
		default:
		}

		// Report progress in input order to keep progress logs predictable for tests.
		if a.progressReporter != nil {
			a.progressReporter(fmt.Sprintf("Scoring answer %d/%d (ID: %s)", i+1, len(input.Answers), answer.ID))
		}

		wg.Add(1)
		go func(idx int, ans domain.Answer) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			// If cancelled, exit early; caller will handle and return error without partial results.
			if ctx.Err() != nil {
				return
			}

			sc, err := a.scoreAnswer(ctx, input.Question, ans, input.Config, wfCtx, idx)
			if err != nil {
				// Create invalid score to maintain 1:1 mapping with input answers.
				scoreID := uuid.NewSHA1(
					uuid.NameSpaceURL, fmt.Appendf(nil, "score-%s-%d", wfCtx.WorkflowID, idx),
				).String()
				orderedScores[idx] = domain.Score{
					ID:              scoreID,
					AnswerID:        ans.ID,
					Value:           0,
					Confidence:      0,
					ScoreEvidence:   domain.ScoreEvidence{InlineReasoning: ""},
					ScoreProvenance: domain.ScoreProvenance{JudgeID: "unknown", Provider: "unknown", Model: "unknown", ScoredAt: time.Now()},
					ScoreUsage:      domain.ScoreUsage{},
					ScoreValidity:   domain.ScoreValidity{Valid: false, Error: err.Error()},
				}
				pkgactivity.SafeLogError(ctx, "Failed to score answer",
					"answer_id", ans.ID,
					"error", err)
				return
			}

			orderedScores[idx] = *sc
		}(i, answer)
	}

	// Wait for all workers to finish, then aggregate to avoid per-goroutine synchronization.
	wg.Wait()

	var (
		totalTokens, totalCalls int64
		totalCostCents          domain.Cents
	)
	for _, sc := range orderedScores {
		if sc.IsValid() {
			totalTokens += sc.TokensUsed
			totalCalls += sc.CallsUsed
			totalCostCents += sc.CostCents
		}
	}

	return &domain.ScoreAnswersOutput{
		Scores:     orderedScores,
		TokensUsed: totalTokens,
		CallsMade:  totalCalls,
		CostCents:  totalCostCents,
	}, nil
}

// scoreAnswer scores a single answer using the LLM client with strict validation.
// Implements blob-if-large policy for rationale storage and one-shot repair.
// Returns non-retryable errors for validation failures and retryable errors
// for transient LLM provider issues.
func (a *Activities) scoreAnswer(
	ctx context.Context,
	question string,
	answer domain.Answer,
	config domain.EvalConfig,
	wfCtx pkgactivity.WorkflowContext,
	index int,
) (*domain.Score, error) {
	// Create domain validator with schema version and blob threshold.
	validator := NewScoringValidator("v1.0", a.blobThreshold)

	// Configure one-shot repair policy per domain requirements.
	repairPolicy := &domain.RepairPolicy{
		MaxAttempts:           1,    // One-shot repair as per Story 1.5.
		AllowTransportRepairs: true, // Allow transport-level repairs.
	}
	scoreInput := domain.ScoreAnswersInput{
		Question:      question,
		Answers:       []domain.Answer{answer},
		Config:        config,
		SchemaVersion: "v1.0",
		Validator:     validator,
		RepairPolicy:  repairPolicy,
	}

	llmOutput, err := a.llmClient.Score(ctx, scoreInput)
	if err != nil {
		if wfErr := llmerrors.ClassifyLLMError(err); wfErr != nil && wfErr.ShouldRetry() {
			return nil, retryable("scoreAnswer", err, wfErr.Message)
		}
		return nil, nonRetryable("scoreAnswer", err, "LLM scoring failed")
	}

	if len(llmOutput.Scores) == 0 {
		return nil, nonRetryable("scoreAnswer", fmt.Errorf("no scores returned"), "empty response")
	}

	score := llmOutput.Scores[0] // Get the single score from batch response.

	if score.ID == "" {
		score.ID = fmt.Sprintf("score-%s-%d", wfCtx.WorkflowID, index)
	}
	if score.AnswerID == "" {
		score.AnswerID = answer.ID
	}

	// Apply blob-if-large policy for rationale storage optimization.
	if score.InlineReasoning != "" {
		if err := a.applyBlobIfLargePolicy(ctx, &score, score.InlineReasoning, wfCtx, index); err != nil {
			return nil, nonRetryable("scoreAnswer", err, "failed to store rationale")
		}
	}

	return &score, nil
}

// applyBlobIfLargePolicy conditionally stores rationale in blob storage or inline
// based on size threshold. Updates score with appropriate storage reference.
func (a *Activities) applyBlobIfLargePolicy(
	ctx context.Context,
	score *domain.Score,
	reasoning string,
	wfCtx pkgactivity.WorkflowContext,
	index int,
) error {
	reasoningLength := len(reasoning)

	if domain.ShouldBlobRationale(reasoningLength, a.blobThreshold) {
		// Store in blob storage to reduce memory footprint and payload size.
		artifactKey := a.generateRationaleKey(wfCtx.TenantID, wfCtx.WorkflowID, score.ID, index)

		ref, err := a.artifactStore.Put(ctx, reasoning, domain.ArtifactJudgeRationale, artifactKey)
		if err != nil {
			return fmt.Errorf("failed to store rationale in blob: %w", err)
		}

		score.ReasonRef = ref
		score.InlineReasoning = "" // Clear inline reasoning to avoid duplication.

		pkgactivity.SafeLog(ctx, "Stored rationale in blob storage",
			"score_id", score.ID,
			"rationale_length", reasoningLength,
			"artifact_key", ref.Key,
			"threshold", a.blobThreshold)
	} else {
		// Store inline for small rationales to avoid blob storage overhead.
		score.InlineReasoning = reasoning
		score.ReasonRef = domain.ArtifactRef{}

		pkgactivity.SafeLog(ctx, "Stored rationale inline",
			"score_id", score.ID,
			"rationale_length", reasoningLength,
			"threshold", a.blobThreshold)
	}

	return nil
}

// generateRationaleKey creates a deterministic artifact key for rationale storage.
// Follows consistent naming pattern for predictable blob organization.
func (a *Activities) generateRationaleKey(tenantID, workflowID, scoreID string, index int) string {
	return fmt.Sprintf("rationales/%s/wf-%s/score-%s/idx-%02d.txt",
		tenantID, workflowID, scoreID, index)
}

// countValidScores counts valid scores for metrics and logging.
func countValidScores(scores []domain.Score) int {
	count := 0
	for _, score := range scores {
		if score.IsValid() {
			count++
		}
	}
	return count
}

// emitScoringEvents emits domain events for observability and cost tracking.
// Emits individual AnswerScored events and aggregated usage metrics.
// Event failures are logged but don't fail the activity.
func (a *Activities) emitScoringEvents(
	ctx context.Context,
	output *domain.ScoreAnswersOutput,
	wfCtx pkgactivity.WorkflowContext,
) {
	// Generate deterministic client idempotency key for event deduplication.
	clientIdemKey := fmt.Sprintf("scoring-%s-%d", wfCtx.WorkflowID, len(output.Scores))
	for i, score := range output.Scores {
		a.events.EmitAnswerScored(ctx, score, wfCtx, clientIdemKey, i)
	}

	// Extract metadata for aggregated usage tracking.
	judgeModels := extractJudgeModels(output.Scores)
	provider := extractProvider(output.Scores)
	providerRequestIDs := extractProviderRequestIDs(output.Scores)

	// Ensure usage events are emitted even for all-failure cases by providing defaults
	if provider == "" {
		provider = "unknown" // Satisfy validation requirement for failed operations
	}
	if len(judgeModels) == 0 {
		judgeModels = []string{"unknown"} // Satisfy validation requirement for failed operations
	}

	// Emit aggregated usage metrics for cost tracking and optimization.
	a.events.EmitScoringUsage(ctx, output, wfCtx, clientIdemKey, judgeModels, provider, providerRequestIDs)
}

// extractJudgeModels extracts and deduplicates all unique models from scores for usage tracking.
// Returns all unique models used across the batch operation.
func extractJudgeModels(scores []domain.Score) []string {
	if len(scores) == 0 {
		return nil
	}

	modelSet := make(map[string]bool)
	for _, score := range scores {
		if score.Model != "" && score.Model != "unknown" {
			modelSet[score.Model] = true
		}
	}

	if len(modelSet) == 0 {
		return nil
	}

	models := make([]string, 0, len(modelSet))
	for model := range modelSet {
		models = append(models, model)
	}
	return models
}

// extractProvider extracts provider information from scores for usage tracking.
// Derives provider by deduping providers recorded on each Score.
// Returns the first unique provider found, or empty string if none.
func extractProvider(scores []domain.Score) string {
	if len(scores) == 0 {
		return ""
	}

	for _, score := range scores {
		if score.Provider != "" && score.Provider != "unknown" {
			return score.Provider
		}
	}

	return ""
}

// extractProviderRequestIDs extracts provider request IDs from scores for correlation.
// Collects all unique provider request IDs from the scores.
func extractProviderRequestIDs(scores []domain.Score) []string {
	if len(scores) == 0 {
		return nil
	}

	requestIDSet := make(map[string]bool)
	for _, score := range scores {
		for _, requestID := range score.ProviderRequestIDs {
			if requestID != "" {
				requestIDSet[requestID] = true
			}
		}
	}

	if len(requestIDSet) == 0 {
		return nil
	}

	requestIDs := make([]string, 0, len(requestIDSet))
	for requestID := range requestIDSet {
		requestIDs = append(requestIDs, requestID)
	}
	return requestIDs
}

// retryable wraps errors as retryable Temporal application errors for transient failures.
func retryable(tag string, cause error, msg string) error {
	return temporal.NewApplicationError(msg, tag, cause)
}
