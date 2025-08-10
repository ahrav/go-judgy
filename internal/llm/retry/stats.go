package retry

import (
	"sync/atomic"
	"time"
)

// retryStats provides thread-safe retry metrics using atomic operations.
// Tracks attempts, success/failure rates, backoff durations, and budget usage
// for comprehensive observability without mutex overhead.
type retryStats struct {
	totalAttempts           atomic.Int64 // Total retry attempts across all requests
	successfulRetries       atomic.Int64 // Requests that succeeded after retry
	failedRetries           atomic.Int64 // Requests that failed after all retries
	successfulFirstAttempts atomic.Int64 // Requests that succeeded on first attempt
	maxBackoff              atomic.Int64 // Maximum backoff duration in nanoseconds
	totalCostCents          atomic.Int64 // Cumulative cost in cents from retries
	totalTokens             atomic.Int64 // Cumulative tokens used in retries
	budgetExceeded          atomic.Int64 // Count of requests exceeding budget limits
}

// RetryStats holds aggregated metrics for retry middleware activity.
// It provides a snapshot of retry behavior for monitoring and observability.
type RetryStats struct {
	// TotalAttempts is the total number of requests, including initial attempts and all retries.
	TotalAttempts int64 `json:"total_attempts"`
	// SuccessfulRetries is the count of requests that succeeded only after one or more retries.
	SuccessfulRetries int64 `json:"successful_retries"`
	// FailedRetries is the count of requests that failed after exhausting all retry attempts.
	FailedRetries int64 `json:"failed_retries"`
	// AverageAttempts is the average number of attempts per request.
	AverageAttempts float64 `json:"average_attempts"`
	// MaxBackoff is the longest backoff duration applied during retries.
	MaxBackoff time.Duration `json:"max_backoff"`
	// TotalCostCents is the cumulative cost in cents from all retried requests.
	TotalCostCents int64 `json:"total_cost_cents"`
	// TotalTokens is the cumulative number of tokens used in all retried requests.
	TotalTokens int64 `json:"total_tokens"`
	// BudgetExceeded is the number of requests that were terminated because they exceeded their configured budget.
	BudgetExceeded int64 `json:"budget_exceeded"`
}

// recordBackoffMetrics records backoff duration for monitoring.
func (r *retryMiddleware) recordBackoffMetrics(backoff time.Duration) {
	backoffNanos := backoff.Nanoseconds()
	// Update max backoff atomically to avoid race conditions.
	for {
		current := r.stats.maxBackoff.Load()
		if backoffNanos <= current {
			break
		}
		if r.stats.maxBackoff.CompareAndSwap(current, backoffNanos) {
			break
		}
	}
}

// GetRetryStats returns a snapshot of the current retry statistics for this
// middleware instance.
func (r *retryMiddleware) GetRetryStats() *RetryStats {
	totalAttempts := r.stats.totalAttempts.Load()
	successfulRetries := r.stats.successfulRetries.Load()
	failedRetries := r.stats.failedRetries.Load()
	successfulFirstAttempts := r.stats.successfulFirstAttempts.Load()
	maxBackoffNanos := r.stats.maxBackoff.Load()
	totalCostCents := r.stats.totalCostCents.Load()
	totalTokens := r.stats.totalTokens.Load()
	budgetExceeded := r.stats.budgetExceeded.Load()

	var averageAttempts float64 = 1.0
	// Include all requests: first-attempt successes, retry successes, and failures.
	if totalRequests := successfulFirstAttempts + successfulRetries + failedRetries; totalRequests > 0 {
		averageAttempts = float64(totalAttempts) / float64(totalRequests)
	}

	return &RetryStats{
		TotalAttempts:     totalAttempts,
		SuccessfulRetries: successfulRetries,
		FailedRetries:     failedRetries,
		AverageAttempts:   averageAttempts,
		MaxBackoff:        time.Duration(maxBackoffNanos),
		TotalCostCents:    totalCostCents,
		TotalTokens:       totalTokens,
		BudgetExceeded:    budgetExceeded,
	}
}

// GetGlobalRetryStats returns global retry statistics across all middleware instances.
// This would typically interface with a metrics system like Prometheus.
func GetGlobalRetryStats() *RetryStats {
	// TODO: Implement global stats aggregation from metrics system
	return &RetryStats{
		AverageAttempts: 1.0,
	}
}
