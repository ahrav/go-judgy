// Package retry provides components for building robust and fault-tolerant LLM clients.
// It includes middleware for retries, rate limiting, circuit breaking, and caching
// to handle transient failures and improve system stability.
package retry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/ahrav/go-judgy/internal/llm/configuration"
	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

var (
	// Configuration validation errors.
	errMaxAttemptsInvalid     = errors.New("maxAttempts must be greater than 0")
	errInitialIntervalInvalid = errors.New("initialInterval must be greater than 0")
	errMaxIntervalInvalid     = errors.New("maxInterval must be >= initialInterval")
	errMultiplierInvalid      = errors.New("multiplier must be >= 1.0")
	errMaxElapsedTimeInvalid  = errors.New("maxElapsedTime must be >= 0")

	// Runtime errors.
	errContextCancelledBeforeRetry = errors.New("context cancelled before retry")
	errContextCancelledDuringRetry = errors.New("context cancelled during retry")
	errAllRetriesExhausted         = errors.New("all retries exhausted")
	errUnexpectedRetryExhaustion   = errors.New("unexpected retry exhaustion")
)

// retryMiddleware implements intelligent retry logic with exponential backoff.
// Handles transient failures with configurable retry policies and respects
// provider-specific retry guidance like Retry-After headers.
type retryMiddleware struct {
	config configuration.RetryConfig
	logger *slog.Logger
	stats  *retryStats
}

// RetryAfterProvider defines an interface for error types that can provide
// a specific duration to wait before retrying.
// This allows servers to communicate backpressure and rate limits, which the
// client can respect.
type RetryAfterProvider interface {
	// GetRetryAfter returns the recommended duration to wait before the next attempt.
	// If no specific duration is available, it should return zero.
	GetRetryAfter() time.Duration
}

// NewRetryMiddlewareWithConfig creates retry middleware with specified configuration.
// Implements exponential backoff with full jitter for optimal retry distribution
// and respects provider rate limit headers.
func NewRetryMiddlewareWithConfig(cfg configuration.RetryConfig) (transport.Middleware, error) {
	if cfg.MaxAttempts <= 0 {
		return nil, fmt.Errorf("%w, got %d", errMaxAttemptsInvalid, cfg.MaxAttempts)
	}
	if cfg.InitialInterval <= 0 {
		return nil, fmt.Errorf("%w, got %v", errInitialIntervalInvalid, cfg.InitialInterval)
	}
	if cfg.MaxInterval < cfg.InitialInterval {
		return nil, fmt.Errorf("%w, MaxInterval: %v, InitialInterval: %v", errMaxIntervalInvalid, cfg.MaxInterval, cfg.InitialInterval)
	}
	if cfg.Multiplier < 1.0 {
		return nil, fmt.Errorf("%w, got %f", errMultiplierInvalid, cfg.Multiplier)
	}
	if cfg.MaxElapsedTime < 0 {
		return nil, fmt.Errorf("%w, got %v", errMaxElapsedTimeInvalid, cfg.MaxElapsedTime)
	}

	return newRetryMiddleware(cfg), nil
}

// newRetryMiddleware constructs retry middleware with optimized error detection.
// Utilizes thread-safe random number generation and preprocessed network error
// patterns for efficient runtime matching without string allocation overhead.
func newRetryMiddleware(cfg configuration.RetryConfig) transport.Middleware {
	rm := &retryMiddleware{
		config: cfg,
		logger: slog.Default().With("component", "retry"),
		stats:  &retryStats{},
	}

	return rm.middleware()
}

// middleware returns the retry middleware function.
func (r *retryMiddleware) middleware() transport.Middleware {
	return func(next transport.Handler) transport.Handler {
		return transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
			var lastErr error
			var lastResp *transport.Response
			startTime := time.Now()

			// Fail fast if context is already cancelled to avoid wasted retry attempts.
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("%w: %w", errContextCancelledBeforeRetry, ctx.Err())
			default:
			}

			// Initialize request budget tracking to prevent runaway costs.
			budget := requestBudget{
				maxCost:   r.config.MaxCostCents,
				maxTokens: r.config.MaxTokens,
				enabled:   r.config.EnableBudget,
			}

			// Circuit breaker probes use single attempts to test service health.
			maxAttempts := r.config.MaxAttempts
			if ctx.Value("circuit_breaker_half_open_probe") != nil {
				maxAttempts = 1
			}

			for attempt := 1; attempt <= maxAttempts; attempt++ {
				// Respect overall timeout to prevent indefinite retry loops.
				if r.config.MaxElapsedTime > 0 && time.Since(startTime) > r.config.MaxElapsedTime {
					r.logger.Warn("max elapsed time exceeded",
						"elapsed", time.Since(startTime),
						"attempts", attempt-1,
						"last_error", lastErr)
					break
				}

				// Enforce spending limits to prevent cost overruns.
				if budget.enabled && r.exceedsBudget(&budget) {
					r.stats.budgetExceeded.Add(1)
					return nil, &llmerrors.WorkflowError{
						Type:      llmerrors.ErrorTypeBudget,
						Message:   "retry budget exceeded",
						Code:      "RETRY_BUDGET_EXCEEDED",
						Retryable: false,
						Details: map[string]any{
							"cost_cents": budget.costCents,
							"tokens":     budget.tokens,
							"max_cost":   budget.maxCost,
							"max_tokens": budget.maxTokens,
						},
					}
				}

				// Execute request and record metrics for observability.
				resp, err := next.Handle(ctx, req)
				r.stats.totalAttempts.Add(1)

				// Track accumulated costs and tokens for budget enforcement.
				if budget.enabled && resp != nil {
					r.updateBudgetFromResponse(&budget, resp)
				}

				// Preserve partial responses for error context and debugging.
				if resp != nil {
					lastResp = resp
				}

				// Success - return immediately to minimize latency.
				if err == nil {
					if attempt > 1 {
						r.stats.successfulRetries.Add(1)
						r.logger.Info("request succeeded after retry",
							"attempt", attempt,
							"provider", req.Provider,
							"model", req.Model,
							"total_cost_cents", budget.costCents,
							"total_tokens", budget.tokens)
					} else {
						r.stats.successfulFirstAttempts.Add(1)
					}
					return resp, nil
				}

				// Avoid retrying errors that will always fail.
				if !r.isRetryable(err) {
					r.logger.Debug("non-retryable error",
						"error", err,
						"attempt", attempt,
						"provider", req.Provider)
					// Return partial response with error for debugging context.
					return lastResp, err
				}

				lastErr = err

				// Prevent unnecessary backoff calculation on final attempt.
				if attempt == maxAttempts {
					break
				}

				// Calculate backoff duration respecting provider guidance.
				backoff := r.calculateBackoff(attempt, err)
				r.recordBackoffMetrics(backoff)

				// Ensure backoff doesn't push us past the overall timeout.
				if r.config.MaxElapsedTime > 0 {
					elapsed := time.Since(startTime)
					if elapsed+backoff > r.config.MaxElapsedTime {
						// Provider retry-after may exceed our time budget - use exponential as fallback.
						retryAfter := r.extractRetryAfter(err)
						if retryAfter > 0 {
							// Calculate exponential backoff ignoring provider guidance.
							exponentialBackoff := r.calculatePureExponentialBackoff(attempt)
							if elapsed+exponentialBackoff <= r.config.MaxElapsedTime {
								backoff = exponentialBackoff
							} else {
								r.logger.Warn("max elapsed time exceeded",
									"elapsed", elapsed,
									"attempts", attempt,
									"last_error", err)
								break
							}
						} else {
							r.logger.Warn("max elapsed time exceeded",
								"elapsed", elapsed,
								"attempts", attempt,
								"last_error", err)
							break
						}
					}
				}

				r.logger.Debug("retrying after backoff",
					"attempt", attempt,
					"backoff", backoff,
					"error", err,
					"provider", req.Provider)

				// Wait with context cancellation to enable graceful shutdown.
				select {
				case <-time.After(backoff):
					// Continue to next attempt.
				case <-ctx.Done():
					return nil, fmt.Errorf("%w: %w", errContextCancelledDuringRetry, ctx.Err())
				}
			}

			// All retries exhausted.
			if lastErr != nil {
				r.stats.failedRetries.Add(1)
				// Return partial response with wrapped error for debugging context.
				return lastResp, fmt.Errorf("%w after %d attempts: %w",
					errAllRetriesExhausted, maxAttempts, lastErr)
			}

			return nil, errUnexpectedRetryExhaustion
		})
	}
}

// isRetryable evaluates error types to determine retry eligibility.
// Implements comprehensive error classification covering provider errors,
// rate limits, network failures, and timeouts while avoiding retries
// for non-recoverable errors like authentication or validation failures.
func (r *retryMiddleware) isRetryable(err error) bool {
	if err == nil {
		return false
	}

	// Check specific error types BEFORE checking RetryAfterProvider interface
	// to ensure proper error classification takes precedence.

	var circuitBreakerErr *llmerrors.CircuitBreakerError
	if errors.As(err, &circuitBreakerErr) {
		return false // Circuit breaker errors are non-retryable
	}

	var rateLimitErr *llmerrors.RateLimitError
	if errors.As(err, &rateLimitErr) {
		return true // Always retry rate limits.
	}

	var providerErr *llmerrors.ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.IsRetryable()
	}

	var workflowErr *llmerrors.WorkflowError
	if errors.As(err, &workflowErr) {
		return workflowErr.Retryable
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	if isNetworkError(err) {
		return true
	}

	// Check for RetryAfterProvider interface last to handle custom error types.
	var provider RetryAfterProvider
	if errors.As(err, &provider) {
		return true
	}

	// Default: don't retry unknown errors.
	return false
}

// isNetworkError checks if an error is a network-related error using proper type assertions.
// This provides robust network error detection without fragile string matching.
func isNetworkError(err error) bool {
	// Check specific types first before interfaces to avoid false negatives.

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		var netErr net.Error
		if errors.As(urlErr.Err, &netErr) {
			return netErr.Timeout()
		}
		return isNetworkErrorByString(urlErr.Err.Error())
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}

	return isNetworkErrorByString(err.Error())
}

// isNetworkErrorByString checks for network errors using string patterns.
func isNetworkErrorByString(errStr string) bool {
	lowered := strings.ToLower(errStr)
	for _, indicator := range getNetworkErrorIndicators() {
		if strings.Contains(lowered, indicator) {
			return true
		}
	}
	return false
}

// getNetworkErrorIndicators returns pre-lowercased network error indicators.
func getNetworkErrorIndicators() []string {
	return []string{
		"connection refused",
		"connection reset",
		"broken pipe",
		"no such host",
		"network is unreachable",
		"i/o timeout",
		"eof",
	}
}
