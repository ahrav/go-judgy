package activity

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm"
	"github.com/ahrav/go-judgy/internal/llm/business"
	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
)

// Activities provides activity functions with proper dependency injection.
// Holds LLM client, artifact store, event sink, and other dependencies needed for Temporal activities.
type Activities struct {
	llmClient     llm.Client
	artifactStore business.ArtifactStore
	eventSink     domain.EventSink
}

// NewActivities creates a new Activities instance with the provided dependencies.
// Used for both production (with real client) and testing (with mock dependencies).
func NewActivities(client llm.Client, artifactStore business.ArtifactStore, eventSink domain.EventSink) *Activities {
	return &Activities{
		llmClient:     client,
		artifactStore: artifactStore,
		eventSink:     eventSink,
	}
}

// GenerateAnswers produces candidate answers using configured LLM providers.
// Implements idempotent answer generation with budget control, artifact storage,
// and comprehensive observability for production reliability.
//
// The operation processes generation requests through the LLM client with
// resilience patterns including idempotency, caching, rate limiting, circuit
// breaking, retry logic, and cost tracking. All answer content is stored
// in external artifact storage following the always-blob policy.
//
// Returns non-retryable errors for validation failures and retryable errors
// for transient provider issues. Activity heartbeats track progress for
// long-running operations.
func (a *Activities) GenerateAnswers(
	ctx context.Context,
	input domain.GenerateAnswersInput,
) (*domain.GenerateAnswersOutput, error) {
	if err := input.Validate(); err != nil {
		return nil, nonRetryable("GenerateAnswers", err, "invalid input")
	}

	var workflowID string
	var isActivityContext bool

	func() {
		defer func() {
			if r := recover(); r != nil {
				workflowID = "test-" + uuid.New().String()
				isActivityContext = false
			}
		}()

		info := activity.GetInfo(ctx)
		workflowID = info.WorkflowExecution.ID
		isActivityContext = true
		safeLog(ctx, "Starting GenerateAnswers activity",
			"workflow_id", workflowID,
			"activity_id", info.ActivityID)
	}()

	startTime := time.Now()

	output, err := a.llmClient.Generate(ctx, input)
	if err != nil {
		if wfErr := llmerrors.ClassifyLLMError(err); wfErr != nil && wfErr.ShouldRetry() {
			return nil, retryable("GenerateAnswers", err, wfErr.Message)
		}
		return nil, nonRetryable("GenerateAnswers", err, "generation failed")
	}

	latencyMs := time.Since(startTime).Milliseconds()

	safeLog(ctx, "LLM generation complete",
		"latency_ms", latencyMs,
		"answers_generated", len(output.Answers),
		"tokens_used", output.TokensUsed,
		"cost_cents", output.CostCents,
		"client_idem_key", output.ClientIdemKey)

	// Implement always-blob policy: store all answer content in artifact storage.
	// Process answers concurrently with bounded parallelism.
	const maxConcurrency = 10
	sem := make(chan struct{}, maxConcurrency)

	type answerResult struct {
		index  int
		answer domain.Answer
		ref    domain.ArtifactRef
		err    error
	}

	results := make(chan answerResult, len(output.Answers))
	var wg sync.WaitGroup

	for i, answer := range output.Answers {
		wg.Add(1)
		go func(idx int, ans domain.Answer) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			select {
			case <-ctx.Done():
				results <- answerResult{index: idx, err: ctx.Err()}
				return
			default:
			}

			// Heartbeat for this specific answer (if in activity context).
			if isActivityContext {
				activity.RecordHeartbeat(ctx, fmt.Sprintf("Storing answer %d/%d", idx+1, len(output.Answers)))
			}

			ref, err := a.storeAnswerContent(ctx, ans, workflowID, output)
			results <- answerResult{
				index:  idx,
				answer: ans,
				ref:    ref,
				err:    err,
			}
		}(i, answer)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	processedAnswers := make([]domain.Answer, len(output.Answers))
	var firstError error

	for res := range results {
		if res.err != nil {
			if firstError == nil {
				firstError = fmt.Errorf("answer %d: %w", res.index, res.err)
			}
			continue
		}

		processedAnswer := res.answer
		processedAnswer.ContentRef = res.ref
		// Clear metadata content to enforce always-blob policy.
		if processedAnswer.Metadata != nil {
			delete(processedAnswer.Metadata, "content")
		}
		processedAnswers[res.index] = processedAnswer
	}

	// Handle any errors with cleanup (fail-fast behavior).
	if firstError != nil {
		a.cleanupArtifacts(ctx, processedAnswers)
		return nil, nonRetryable("GenerateAnswers", firstError, "failed to store answers")
	}

	result := &domain.GenerateAnswersOutput{
		Answers:       processedAnswers,
		TokensUsed:    output.TokensUsed,
		CallsMade:     output.CallsMade,
		CostCents:     output.CostCents,
		ClientIdemKey: output.ClientIdemKey, // Preserve the client's canonical key.
		Error:         output.Error,
	}

	if err := result.Validate(); err != nil {
		a.cleanupArtifacts(ctx, processedAnswers)
		return nil, nonRetryable("GenerateAnswers", err, "invalid output")
	}

	// Emit events for projections (best-effort, non-fatal).
	// Use the client's canonical idempotency key for event deduplication.
	if result.ClientIdemKey != "" {
		a.emitEvents(ctx, result, workflowID, result.ClientIdemKey, input)
	} else {
		safeLog(ctx, "WARNING: Client did not provide idempotency key, skipping event emission",
			"workflow_id", workflowID)
	}

	if isActivityContext {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("Completed: %d answers, %d tokens, %d cents, %dms",
			len(result.Answers), result.TokensUsed, int64(result.CostCents), latencyMs))
	}

	safeLog(ctx, "GenerateAnswers activity completed successfully",
		"answers_processed", len(result.Answers),
		"total_latency_ms", latencyMs,
		"workflow_id", workflowID)

	return result, nil
}

// storeAnswerContent stores answer content in the artifact store and returns ArtifactRef.
// Implements always-blob policy by storing all answer content externally.
//
// Creates unique artifact keys based on workflow ID and answer ID for traceability.
// If content reference already exists and is valid, returns existing reference.
// Content is extracted from answer metadata and stored with a timestamped key.
func (a *Activities) storeAnswerContent(
	ctx context.Context, answer domain.Answer, workflowID string, _ *domain.GenerateAnswersOutput,
) (domain.ArtifactRef, error) {
	if answer.ContentRef.Key != "" {
		exists, err := a.artifactStore.Exists(ctx, answer.ContentRef)
		if err != nil {
			return domain.ArtifactRef{}, fmt.Errorf("failed to verify existing content: %w", err)
		}
		if exists {
			return answer.ContentRef, nil
		}
		// Content reference exists but actual content is missing - fall through to store.
	}

	var content string
	if contentStr, ok := answer.Metadata["content"].(string); ok && contentStr != "" {
		content = contentStr
	} else {
		// This indicates a bug in the transport layer - the content should be available.
		// For now, create a meaningful error content for debugging.
		content = fmt.Sprintf("ERROR: Content not found for answer ID %s. This indicates a transport layer issue where content was not properly passed through metadata.", answer.ID)
	}

	timestamp := time.Now().Format("2006/01/02/15/04/05")
	uniqueID := uuid.New().String()
	artifactKey := fmt.Sprintf("answers/%s/%s/%s-%s.txt", timestamp, workflowID, answer.ID, uniqueID)

	ref, err := a.artifactStore.Put(ctx, content, domain.ArtifactAnswer, artifactKey)
	if err != nil {
		return domain.ArtifactRef{}, fmt.Errorf("failed to store answer content: %w", err)
	}

	safeLog(ctx, "Answer content stored",
		"answer_id", answer.ID,
		"artifact_key", ref.Key,
		"content_size", ref.Size,
		"workflow_id", workflowID)

	return ref, nil
}

// cleanupArtifacts removes artifacts from storage when an error occurs during processing.
// Provides graceful cleanup to prevent orphaned artifacts on activity failure.
//
// Performs best-effort cleanup without failing the activity if cleanup operations fail.
// Logs cleanup success and failures for operational visibility.
func (a *Activities) cleanupArtifacts(ctx context.Context, answers []domain.Answer) {
	for _, answer := range answers {
		if answer.ContentRef.Key != "" {
			if exists, err := a.artifactStore.Exists(ctx, answer.ContentRef); err == nil && exists {
				if err := a.artifactStore.Delete(ctx, answer.ContentRef); err != nil {
					safeLogError(ctx, "Failed to cleanup artifact",
						"artifact_key", answer.ContentRef.Key,
						"answer_id", answer.ID,
						"error", err)
				} else {
					safeLog(ctx, "Successfully cleaned up artifact",
						"artifact_key", answer.ContentRef.Key,
						"answer_id", answer.ID)
				}
			}
		}
	}
}

// emitEvents handles event emission for projections with best-effort delivery.
// Emits CandidateProduced events (one per answer) and LLMUsage event (one per activity).
//
// Event failures are logged and metered but do not fail the activity.
// Uses client-provided idempotency keys for event deduplication across workflow retries.
func (a *Activities) emitEvents(
	ctx context.Context,
	result *domain.GenerateAnswersOutput,
	workflowID string,
	clientIdempotencyKey string,
	input domain.GenerateAnswersInput,
) {
	var runID string
	var tenantID uuid.UUID

	func() {
		defer func() {
			if recover() != nil {
				runID = "test-" + uuid.New().String()
			}
		}()

		info := activity.GetInfo(ctx)
		if info.WorkflowExecution.RunID != "" {
			runID = info.WorkflowExecution.RunID
		} else {
			runID = "test-" + uuid.New().String()
		}
	}()

	// For MVP, derive tenant from workflow ID or use defaults.
	// In production, these would come from workflow input or metadata.
	tenantID = uuid.New() // TODO: Extract from workflow context in future.

	var artifactRefs []string
	for _, answer := range result.Answers {
		if answer.ContentRef.Key != "" {
			artifactRefs = append(artifactRefs, answer.ContentRef.Key)
		}
	}

	for i, answer := range result.Answers {
		event, err := domain.NewCandidateProducedEvent(
			tenantID,
			workflowID,
			runID,
			answer,
			clientIdempotencyKey,
			i,
		)

		if err != nil {
			safeLogError(ctx, "Failed to create CandidateProduced event",
				"answer_id", answer.ID,
				"error", err)
			continue
		}

		if err := a.appendEventWithRetry(ctx, event); err != nil {
			safeLogError(ctx, "Failed to emit CandidateProduced event",
				"answer_id", answer.ID,
				"idempotency_key", event.IdempotencyKey,
				"error", err)
		} else {
			safeLog(ctx, "CandidateProduced event emitted",
				"answer_id", answer.ID,
				"idempotency_key", event.IdempotencyKey)
		}
	}

	models := extractModelsFromAnswers(result.Answers)
	providerRequestIDs := extractProviderRequestIDsFromAnswers(result.Answers)
	provider := extractPrimaryProvider(result.Answers)
	cacheHit := isLikelyCacheHit(result) // Heuristic based on very fast response times.

	usageEvent, err := domain.NewLLMUsageEvent(
		tenantID,
		workflowID,
		runID,
		result,
		provider,
		models,
		providerRequestIDs,
		cacheHit,
		clientIdempotencyKey,
		artifactRefs,
	)

	if err != nil {
		safeLogError(ctx, "Failed to create LLMUsage event",
			"workflow_id", workflowID,
			"error", err)
		return
	}

	if err := a.appendEventWithRetry(ctx, usageEvent); err != nil {
		safeLogError(ctx, "Failed to emit LLMUsage event",
			"workflow_id", workflowID,
			"idempotency_key", usageEvent.IdempotencyKey,
			"error", err)
	} else {
		safeLog(ctx, "LLMUsage event emitted",
			"workflow_id", workflowID,
			"idempotency_key", usageEvent.IdempotencyKey,
			"total_tokens", result.TokensUsed,
			"cost_cents", result.CostCents)
	}
}

// appendEventWithRetry implements best-effort event emission with a short retry.
// Performs bounded retry (200ms backoff, 2 attempts) then gives up.
//
// Returns error only after all retry attempts fail. Does not block activity
// completion on event emission failures.
func (a *Activities) appendEventWithRetry(ctx context.Context, event domain.EventEnvelope) error {
	const maxAttempts = 2
	const retryDelay = 200 * time.Millisecond

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(retryDelay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		if err := a.eventSink.Append(ctx, event); err != nil {
			lastErr = err
			continue
		}

		return nil
	}

	return fmt.Errorf("event append failed after %d attempts: %w", maxAttempts, lastErr)
}

// extractModelsFromAnswers extracts unique models used across all answers.
// Returns a slice of distinct model names for usage tracking and cost analysis.
func extractModelsFromAnswers(answers []domain.Answer) []string {
	modelSet := make(map[string]struct{})
	for _, answer := range answers {
		if answer.Model != "" {
			modelSet[answer.Model] = struct{}{}
		}
	}

	models := make([]string, 0, len(modelSet))
	for model := range modelSet {
		models = append(models, model)
	}
	return models
}

// extractProviderRequestIDsFromAnswers collects all provider request IDs for correlation.
// Returns a flattened slice of all provider request IDs from all answers.
func extractProviderRequestIDsFromAnswers(answers []domain.Answer) []string {
	var requestIDs []string
	for _, answer := range answers {
		requestIDs = append(requestIDs, answer.ProviderRequestIDs...)
	}
	return requestIDs
}

// extractPrimaryProvider returns the most common provider used across answers.
// Returns empty string if no answers have provider information.
func extractPrimaryProvider(answers []domain.Answer) string {
	if len(answers) == 0 {
		return ""
	}

	providerCounts := make(map[string]int)
	for _, answer := range answers {
		if answer.Provider != "" {
			providerCounts[answer.Provider]++
		}
	}

	var maxProvider string
	var maxCount int
	for provider, count := range providerCounts {
		if count > maxCount {
			maxCount = count
			maxProvider = provider
		}
	}

	return maxProvider
}

// isLikelyCacheHit provides a heuristic to determine if the request was cached.
// This is a simple heuristic based on response time; in production this should
// come from the LLM client's cache hit tracking.
//
// Returns true if any answer has latency below the cache hit threshold (100ms).
// This is a temporary implementation until proper cache hit tracking is available.
func isLikelyCacheHit(result *domain.GenerateAnswersOutput) bool {
	if len(result.Answers) == 0 {
		return false
	}

	const cacheHitThresholdMs = 100
	for _, answer := range result.Answers {
		if answer.LatencyMillis < cacheHitThresholdMs {
			return true
		}
	}

	return false
}

// safeLog performs context-safe logging that works both in activity and test contexts.
// In activity context, uses Temporal's activity logger. In test context, silently ignores.
//
// Uses panic recovery to detect activity context availability without explicit checks.
func safeLog(ctx context.Context, msg string, keyvals ...interface{}) {
	defer func() {
		if recover() != nil {
			// Not an activity context, ignore logging.
		}
	}()

	// This will panic if not in activity context, which is caught by recover above.
	activity.GetLogger(ctx).Info(msg, keyvals...)
}

// safeLogError performs context-safe error logging that works both in activity and test contexts.
// Uses panic recovery to detect activity context availability without explicit checks.
func safeLogError(ctx context.Context, msg string, keyvals ...interface{}) {
	defer func() {
		if recover() != nil {
			// Not an activity context, ignore logging.
		}
	}()

	// This will panic if not in activity context, which is caught by recover above.
	activity.GetLogger(ctx).Error(msg, keyvals...)
}
