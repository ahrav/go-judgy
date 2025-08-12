package generation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.temporal.io/sdk/temporal"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm"
	"github.com/ahrav/go-judgy/internal/llm/business"
	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/transport"
	"github.com/ahrav/go-judgy/pkg/activity"
)

const (
	// HashKeyLength is the length of the hash used in artifact keys.
	hashKeyLength = 12
	// FallbackHashLength is the length of the hash used in fallback keys.
	fallbackHashLength = 8
)

// AnswerArtifactKey generates a stable, idempotent artifact storage key.
// Uses workflowID + short(clientIdemKey) + candidate index for retry-safe keys.
// This ensures artifact storage is idempotent across retries and discoverable
// by tenant/workflow.
func AnswerArtifactKey(tenantID, workflowID, clientIdemKey string, idx int) string {
	// Keep it small and deterministic; add tenant & wf for partitioning/browsing.
	short := shortHash(clientIdemKey, hashKeyLength)
	return fmt.Sprintf("answers/%s/wf-%s/idem-%s/cand-%02d.txt",
		tenantID, slug(workflowID), short, idx)
}

// shortHash creates a short hash from the input string.
// Uses SHA-256 and returns the first n characters of the hex encoding.
func shortHash(input string, n int) string {
	hash := sha256.Sum256([]byte(input))
	hexStr := hex.EncodeToString(hash[:])
	if len(hexStr) > n {
		return hexStr[:n]
	}
	return hexStr
}

// slug creates a URL-safe version of a string for use in paths.
// Replaces non-alphanumeric characters with hyphens and converts to lowercase.
func slug(s string) string {
	s = strings.ToLower(s)

	// Replace non-alphanumeric characters with hyphens.
	var result strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			result.WriteRune(r)
		} else if result.Len() > 0 && result.String()[result.Len()-1] != '-' {
			result.WriteRune('-')
		}
	}

	return strings.Trim(result.String(), "-")
}

// Activities handles generation-specific Temporal activities.
// It encapsulates all dependencies needed for answer generation including
// LLM client interaction, artifact storage, and event emission.
type Activities struct {
	activity.BaseActivities
	llmClient     llm.Client
	artifactStore business.ArtifactStore
	events        *EventEmitter
}

// NewActivities creates generation activities with the provided dependencies.
// The base activities provide common infrastructure like event emission and logging,
// while the LLM client and artifact store handle generation and storage operations.
func NewActivities(
	base activity.BaseActivities,
	client llm.Client,
	store business.ArtifactStore,
) *Activities {
	return &Activities{
		BaseActivities: base,
		llmClient:      client,
		artifactStore:  store,
		events:         NewEventEmitter(base),
	}
}

// GenerateAnswers produces candidate answers using configured LLM providers.
// This activity implements idempotent answer generation with comprehensive
// resilience patterns including retry logic, circuit breaking, rate limiting,
// and cost tracking. All answer content is stored in external artifact storage
// following the always-blob policy for efficient data management.
//
// The operation:
// 1. Validates input parameters
// 2. Generates answers via the LLM client
// 3. Stores answer content in artifact storage (concurrent with bounded parallelism)
// 4. Emits events for observability and projections
// 5. Returns structured output with artifact references
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

	wfCtx := a.GetWorkflowContext(ctx)
	activity.SafeLog(ctx, "Starting GenerateAnswers activity",
		"workflow_id", wfCtx.WorkflowID,
		"activity_id", wfCtx.ActivityID)

	startTime := time.Now()
	output, err := a.llmClient.Generate(ctx, input)
	if err != nil {
		if wfErr := llmerrors.ClassifyLLMError(err); wfErr != nil && wfErr.ShouldRetry() {
			return nil, retryable("GenerateAnswers", err, wfErr.Message)
		}
		return nil, nonRetryable("GenerateAnswers", err, "generation failed")
	}

	// Derive clientIdemKey if missing - use the same canonical payload logic as the LLM client.
	clientIdemKey := output.ClientIdemKey
	if clientIdemKey == "" {
		// Build the exact CanonicalPayload used by the LLM client and hash it.
		activity.SafeLog(ctx, "WARNING: No client idempotency key, generating fallback")

		// Extract provider and model from first answer (if available)
		provider := "unknown"
		model := "unknown"
		if len(output.Answers) > 0 {
			provider = output.Answers[0].Provider
			model = output.Answers[0].Model
		}

		// Build canonical payload from generation input
		// Note: EvalConfig doesn't have SystemPrompt, so we use empty string
		canonicalPayload, err := transport.BuildCanonicalPayloadFromGenerate(
			wfCtx.TenantID,
			input.Question,
			provider,
			model,
			"", // SystemPrompt not available in EvalConfig
			int(input.Config.MaxAnswerTokens),
			input.Config.Temperature,
		)
		if err != nil {
			// Ultimate fallback - use a simple deterministic key
			clientIdemKey = fmt.Sprintf("fallback-%s-%s-%d", wfCtx.WorkflowID, shortHash(input.Question, fallbackHashLength), input.NumAnswers)
		} else {
			clientIdemKey = transport.HashCanonicalPayload(canonicalPayload)
		}
	}

	// Store artifacts concurrently to optimize I/O latency for multiple answers.
	// Use clientIdemKey for idempotent storage keys.
	processedAnswers, err := a.storeAnswerArtifacts(ctx, output.Answers, wfCtx.WorkflowID, wfCtx.TenantID, clientIdemKey)
	if err != nil {
		return nil, nonRetryable("GenerateAnswers", err, "failed to store artifacts")
	}

	result := &domain.GenerateAnswersOutput{
		Answers:       processedAnswers,
		TokensUsed:    output.TokensUsed,
		CallsMade:     output.CallsMade,
		CostCents:     output.CostCents,
		ClientIdemKey: clientIdemKey, // Use the derived key (could be fallback)
		Error:         output.Error,
	}

	// Emit events for observability without failing the activity if events fail.
	a.emitGenerationEvents(ctx, result, wfCtx)

	latencyMs := time.Since(startTime).Milliseconds()
	activity.SafeLog(ctx, "GenerateAnswers completed",
		"answers", len(result.Answers),
		"latency_ms", latencyMs)

	return result, nil
}

// storeAnswerArtifacts concurrently stores answer content in artifact storage.
// Implements the always-blob policy with bounded parallelism for efficiency.
// Returns answers with updated artifact references or error if storage fails.
func (a *Activities) storeAnswerArtifacts(
	ctx context.Context,
	answers []domain.Answer,
	workflowID string,
	tenantID string,
	clientIdemKey string,
) ([]domain.Answer, error) {
	// Limit concurrent storage operations to prevent overwhelming the artifact store.
	const maxConcurrency = 10
	sem := make(chan struct{}, maxConcurrency)

	type answerResult struct {
		index  int
		answer domain.Answer
		ref    domain.ArtifactRef
		err    error
	}

	results := make(chan answerResult, len(answers))
	var wg sync.WaitGroup

	for i, answer := range answers {
		wg.Add(1)
		go func(idx int, ans domain.Answer) {
			defer wg.Done()

			// Acquire semaphore to limit concurrent artifact storage operations.
			sem <- struct{}{}
			defer func() { <-sem }()

			select {
			case <-ctx.Done():
				results <- answerResult{index: idx, err: ctx.Err()}
				return
			default:
			}

			// Record heartbeat to prevent Temporal timeout during long storage operations.
			activity.RecordHeartbeat(ctx, fmt.Sprintf("Storing answer %d/%d", idx+1, len(answers)))

			ref, err := a.StoreAnswerContent(ctx, ans, workflowID, tenantID, clientIdemKey, idx)
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

	processedAnswers := make([]domain.Answer, len(answers))
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
		// Remove content from metadata to enforce always-blob policy for large data.
		if processedAnswer.Metadata != nil {
			delete(processedAnswer.Metadata, "content")
		}
		processedAnswers[res.index] = processedAnswer
	}

	// Handle any errors with cleanup.
	if firstError != nil {
		a.cleanupArtifacts(ctx, processedAnswers)
		return nil, firstError
	}

	return processedAnswers, nil
}

// StoreAnswerContent stores answer content in the artifact store.
// Creates idempotent artifact keys based on workflow ID, client idempotency key, and index.
// If content reference already exists and is valid, returns existing reference.
func (a *Activities) StoreAnswerContent(
	ctx context.Context,
	answer domain.Answer,
	workflowID string,
	tenantID string,
	clientIdemKey string,
	index int,
) (domain.ArtifactRef, error) {
	if answer.ContentRef.Key != "" {
		exists, err := a.artifactStore.Exists(ctx, answer.ContentRef)
		if err != nil {
			return domain.ArtifactRef{}, fmt.Errorf("failed to verify existing content: %w", err)
		}
		if exists {
			return answer.ContentRef, nil
		}
	}

	var content string
	if contentStr, ok := answer.Metadata["content"].(string); ok && contentStr != "" {
		content = contentStr
	} else {
		// Fallback content indicates upstream data loss or corruption.
		content = fmt.Sprintf("ERROR: Content not found for answer ID %s. This indicates a transport layer issue.", answer.ID)
	}

	// Create idempotent artifact key using stable inputs for retry-safety.
	artifactKey := AnswerArtifactKey(tenantID, workflowID, clientIdemKey, index)

	ref, err := a.artifactStore.Put(ctx, content, domain.ArtifactAnswer, artifactKey)
	if err != nil {
		return domain.ArtifactRef{}, fmt.Errorf("failed to store answer content: %w", err)
	}

	activity.SafeLog(ctx, "Answer content stored",
		"answer_id", answer.ID,
		"artifact_key", ref.Key,
		"content_size", ref.Size,
		"workflow_id", workflowID)

	return ref, nil
}

// cleanupArtifacts removes artifacts from storage when an error occurs.
// Provides best-effort cleanup without failing the activity if cleanup fails.
func (a *Activities) cleanupArtifacts(ctx context.Context, answers []domain.Answer) {
	for _, answer := range answers {
		if answer.ContentRef.Key != "" {
			if exists, err := a.artifactStore.Exists(ctx, answer.ContentRef); err == nil && exists {
				if err := a.artifactStore.Delete(ctx, answer.ContentRef); err != nil {
					activity.SafeLogError(ctx, "Failed to cleanup artifact",
						"artifact_key", answer.ContentRef.Key,
						"answer_id", answer.ID,
						"error", err)
				} else {
					activity.SafeLog(ctx, "Successfully cleaned up artifact",
						"artifact_key", answer.ContentRef.Key,
						"answer_id", answer.ID)
				}
			}
		}
	}
}

// emitGenerationEvents emits domain events for the generation activity.
// Emits individual CandidateProduced events per answer and one aggregated
// LLMUsage event. Event failures are logged but don't fail the activity.
func (a *Activities) emitGenerationEvents(
	ctx context.Context,
	result *domain.GenerateAnswersOutput,
	wfCtx activity.WorkflowContext,
) {
	if result.ClientIdemKey == "" {
		activity.SafeLog(ctx, "WARNING: No client idempotency key, skipping events")
		return
	}

	// Emit individual answer events.
	for i, answer := range result.Answers {
		a.events.EmitCandidateProduced(ctx, answer, i, wfCtx, result.ClientIdemKey)
	}

	// Collect metadata for usage event.
	models := extractModelsFromAnswers(result.Answers)
	providerRequestIDs := extractProviderRequestIDs(result.Answers)
	provider := extractPrimaryProvider(result.Answers)
	artifactRefs := extractArtifactRefs(result.Answers)

	// Emit usage event.
	a.events.EmitLLMUsage(ctx, result, wfCtx, models, providerRequestIDs, provider, artifactRefs)
}

// extractModelsFromAnswers extracts unique model names from the answer set.
// Returns a deduplicated slice of model strings for usage tracking.
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

// extractProviderRequestIDs aggregates all provider request IDs from answers.
// Returns a flattened slice of request IDs for audit trail correlation.
func extractProviderRequestIDs(answers []domain.Answer) []string {
	var requestIDs []string
	for _, answer := range answers {
		requestIDs = append(requestIDs, answer.ProviderRequestIDs...)
	}
	return requestIDs
}

// extractPrimaryProvider determines the most frequently used provider.
// Returns the provider name with the highest answer count, or empty string if no answers.
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

// extractArtifactRefs collects all artifact reference keys from answers.
// Returns a slice of artifact keys for storage tracking and cleanup.
func extractArtifactRefs(answers []domain.Answer) []string {
	var refs []string
	for _, answer := range answers {
		if answer.ContentRef.Key != "" {
			refs = append(refs, answer.ContentRef.Key)
		}
	}
	return refs
}

// nonRetryable wraps an error as a Temporal non-retryable application error.
// Used for validation failures and permanent errors that should not be retried.
func nonRetryable(tag string, cause error, msg string) error {
	return temporal.NewNonRetryableApplicationError(msg, tag, cause)
}

// retryable wraps an error as a Temporal retryable application error.
// Used for transient failures that may succeed on retry with backoff.
func retryable(tag string, cause error, msg string) error {
	return temporal.NewApplicationError(msg, tag, cause)
}
