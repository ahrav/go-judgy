package retry

import (
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// requestBudget enforces per-request spending limits across retry attempts.
// Prevents runaway costs by tracking accumulated usage and enforcing
// maximum cost and token thresholds during retry sequences.
type requestBudget struct {
	costCents int64
	tokens    int64
	maxCost   int64
	maxTokens int64
	enabled   bool
}

const (
	// DefaultMaxAttempts is the default number of retry attempts for non-idempotent operations.
	DefaultMaxAttempts = 3
	// MilliCentsToCentsConversion converts milli-cents to cents for budget tracking.
	MilliCentsToCentsConversion = 1000
)

// GetActivityOptions returns Temporal activity options configured for retry coordination.
// Prevents conflicts between HTTP-level retries and activity-level retries.
func GetActivityOptions(isIdempotent bool) map[string]any {
	if isIdempotent {
		// Let HTTP client handle retries for idempotent operations.
		return map[string]any{
			"RetryPolicy": map[string]any{
				"MaximumAttempts": 1,
			},
			"StartToCloseTimeout": "5m",
		}
	}
	// Non-idempotent: activity retries only, no HTTP retries.
	return map[string]any{
		"RetryPolicy": map[string]any{
			"MaximumAttempts":        DefaultMaxAttempts,
			"NonRetryableErrorTypes": []string{"ValidationError", "AuthError"},
		},
		"StartToCloseTimeout": "5m",
	}
}

// exceedsBudget checks if the current request would exceed budget limits.
func (r *retryMiddleware) exceedsBudget(budget *requestBudget) bool {
	if !budget.enabled {
		return false
	}

	if budget.maxCost > 0 && budget.costCents > budget.maxCost {
		r.logger.Warn("retry cost budget exceeded",
			"current_cost_cents", budget.costCents,
			"max_cost_cents", budget.maxCost)
		return true
	}

	if budget.maxTokens > 0 && budget.tokens > budget.maxTokens {
		r.logger.Warn("retry token budget exceeded",
			"current_tokens", budget.tokens,
			"max_tokens", budget.maxTokens)
		return true
	}

	return false
}

// updateBudgetFromResponse extracts cost and token information from response.
func (r *retryMiddleware) updateBudgetFromResponse(budget *requestBudget, resp *transport.Response) {
	// Add tokens from this attempt for budget tracking.
	if resp.Usage.TotalTokens > 0 {
		tokensUsed := resp.Usage.TotalTokens
		budget.tokens += tokensUsed
		r.stats.totalTokens.Add(tokensUsed)
	}

	// Add cost from this attempt for budget tracking.
	if resp.EstimatedCostMilliCents > 0 {
		costCents := resp.EstimatedCostMilliCents / MilliCentsToCentsConversion // Convert milli-cents to cents for budget tracking.
		budget.costCents += costCents
		r.stats.totalCostCents.Add(costCents)
	}
}
