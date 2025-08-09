package errors

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ErrorType categorizes LLM operation failures for retry classification.
// Types determine whether operations should be retried and with what backoff strategy.
// Enabling resilient handling of transient vs. permanent failures.
//
//nolint:godot // linter incorrectly flags properly capitalized comment
type ErrorType string

const (
	// ErrorTypeTimeout indicates request timeout or deadline exceeded (retryable).
	ErrorTypeTimeout ErrorType = "timeout"

	// ErrorTypeRateLimit indicates rate limit exceeded, retry with backoff (retryable).
	ErrorTypeRateLimit ErrorType = "rate_limit"

	// ErrorTypeNetwork indicates network connectivity issues (retryable).
	ErrorTypeNetwork ErrorType = "network"

	// ErrorTypeProvider indicates provider service unavailable (retryable).
	ErrorTypeProvider ErrorType = "provider_unavailable"

	// ErrorTypeCircuitBreaker indicates circuit breaker protection activated (retryable).
	ErrorTypeCircuitBreaker ErrorType = "circuit_breaker"

	// ErrorTypeBudget indicates cost budget exceeded (business error).
	ErrorTypeBudget ErrorType = "budget_exceeded"

	// ErrorTypeValidation indicates input validation failed (business error).
	ErrorTypeValidation ErrorType = "validation_failed"

	// ErrorTypeContent indicates content blocked by safety filters (business error).
	ErrorTypeContent ErrorType = "content_filtered"

	// ErrorTypeAuth indicates authentication failed (non-retryable).
	ErrorTypeAuth ErrorType = "authentication"

	// ErrorTypePermission indicates insufficient permissions (non-retryable).
	ErrorTypePermission ErrorType = "permission_denied"

	// ErrorTypeQuota indicates account quota exceeded (non-retryable).
	ErrorTypeQuota ErrorType = "quota_exceeded"

	// ErrorTypePricingUnavailable indicates pricing data unavailable or stale.
	ErrorTypePricingUnavailable ErrorType = "pricing_unavailable"

	// ErrorTypeUnknown indicates an unclassified error.
	ErrorTypeUnknown ErrorType = "unknown"
)

// Common LLM operation errors for consistent error handling.
var (
	// ErrProviderUnavailable indicates the provider service is down or unreachable.
	ErrProviderUnavailable = errors.New("provider service unavailable")

	// ErrRateLimitExceeded indicates rate limit has been exceeded.
	ErrRateLimitExceeded = errors.New("rate limit exceeded")

	// ErrCircuitBreakerOpen indicates the circuit breaker is open.
	ErrCircuitBreakerOpen = errors.New("circuit breaker open")

	// ErrCacheMiss indicates the requested item was not found in cache.
	ErrCacheMiss = errors.New("cache miss")

	// ErrPricingUnavailable indicates pricing data is unavailable or stale.
	ErrPricingUnavailable = errors.New("pricing data unavailable")

	// ErrUnknownProvider indicates an unknown or unsupported provider.
	ErrUnknownProvider = errors.New("unknown provider")

	// ErrUnknownModel indicates an unknown or unsupported model.
	ErrUnknownModel = errors.New("unknown model")

	// ErrInvalidResponse indicates the provider returned an invalid response.
	ErrInvalidResponse = errors.New("invalid provider response")

	// ErrJSONValidation indicates JSON validation failed.
	ErrJSONValidation = errors.New("JSON validation failed")

	// ErrMaxRetriesExceeded indicates maximum retry attempts exceeded.
	ErrMaxRetriesExceeded = errors.New("maximum retries exceeded")

	// Usage mapping errors.
	ErrUnsupportedUsageType       = errors.New("unsupported usage type")
	ErrUnsupportedProvider        = errors.New("unsupported provider for usage mapping")
	ErrUsageNil                   = errors.New("usage is nil")
	ErrNegativePromptTokens       = errors.New("negative prompt tokens")
	ErrNegativeCompletionTokens   = errors.New("negative completion tokens")
	ErrNegativeTotalTokens        = errors.New("negative total tokens")
	ErrInconsistentTokenCounts    = errors.New("inconsistent token counts")
	ErrSuspiciouslyHighTokenCount = errors.New("suspiciously high token count")
)

// ProviderError captures structured error responses from LLM providers.
// Includes HTTP status codes, provider-specific error codes, and retry timing
// to enable appropriate retry behavior and error diagnosis.
type ProviderError struct {
	Provider   string    `json:"provider"`    // Provider name
	StatusCode int       `json:"status_code"` // HTTP status code
	Message    string    `json:"message"`     // Error message
	Code       string    `json:"code"`        // Provider error code
	Type       ErrorType `json:"type"`        // Classified error type
	RetryAfter int       `json:"retry_after"` // Retry-After header value in seconds
}

// Error returns formatted provider error with status code context.
// Provides structured error representation for provider-specific failures.
func (e *ProviderError) Error() string {
	return fmt.Sprintf("%s error (status %d): %s", e.Provider, e.StatusCode, e.Message)
}

// IsRetryable determines if the provider error warrants a retry attempt.
// Examines error type to classify transient vs. permanent failures.
//
//nolint:godot // linter incorrectly flags properly capitalized comment
func (e *ProviderError) IsRetryable() bool {
	switch e.Type {
	case ErrorTypeTimeout, ErrorTypeRateLimit, ErrorTypeNetwork, ErrorTypeProvider:
		return true
	default:
		return false
	}
}

// GetRetryAfter implements RetryAfterProvider interface.
func (e *ProviderError) GetRetryAfter() time.Duration {
	if e.RetryAfter > 0 {
		return time.Duration(e.RetryAfter) * time.Second
	}
	return 0
}

// RateLimitError provides detailed rate limit context for backoff calculation.
// Includes retry timing, limit details, and local vs. remote limit distinction
// to enable optimal backoff strategies and quota management.
//
//nolint:godot // linter incorrectly flags properly capitalized comment
type RateLimitError struct {
	Provider   string `json:"provider"`
	RetryAfter int    `json:"retry_after"` // Seconds to wait before retry
	ResetAt    int64  `json:"reset_at"`    // Unix timestamp when limit resets
	Limit      int    `json:"limit"`       // Rate limit
	Remaining  int    `json:"remaining"`   // Remaining requests
	LocalLimit bool   `json:"local_limit"` // Whether this is a local limit
}

// Error returns formatted rate limit error with retry guidance.
// Includes backoff timing for optimal retry behavior.
func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("rate limit exceeded for %s, retry after %d seconds", e.Provider, e.RetryAfter)
	}
	return fmt.Sprintf("rate limit exceeded for %s", e.Provider)
}

// GetRetryAfter implements RetryAfterProvider interface.
func (e *RateLimitError) GetRetryAfter() time.Duration {
	if e.RetryAfter > 0 {
		return time.Duration(e.RetryAfter) * time.Second
	}
	return 0
}

// CircuitBreakerError indicates circuit breaker activation for provider protection.
// Provides breaker state and reset timing to enable proper fallback behavior
// and prevent cascading failures during provider outages.
type CircuitBreakerError struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	State    string `json:"state"`    // "open" or "half-open"
	ResetAt  int64  `json:"reset_at"` // Unix timestamp when breaker might close
}

// Error returns formatted circuit breaker error with state context.
// Indicates breaker activation and provides reset timing guidance.
func (e *CircuitBreakerError) Error() string {
	return fmt.Sprintf("circuit breaker %s for %s/%s", e.State, e.Provider, e.Model)
}

// ValidationError captures input validation failures with structured context.
// Includes field-specific details and expected schemas to enable proper
// error handling and user feedback for malformed requests.
type ValidationError struct {
	Field   string `json:"field"`   // Field that failed validation
	Value   any    `json:"value"`   // Invalid value
	Message string `json:"message"` // Validation message
	Schema  any    `json:"schema"`  // Expected schema
}

// Error returns formatted validation error with field-specific context.
// Provides detailed validation failure information for debugging.
func (e *ValidationError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("validation failed for field %s: %s", e.Field, e.Message)
	}
	return fmt.Sprintf("validation failed: %s", e.Message)
}

// PricingError indicates cost calculation or budget enforcement failures.
// Provides provider and model context to enable fallback pricing strategies
// and prevent unbounded cost exposure in production environments.
type PricingError struct {
	Provider string    `json:"provider"`
	Model    string    `json:"model"`
	Region   string    `json:"region,omitempty"`
	Reason   string    `json:"reason"`
	Type     ErrorType `json:"type"`
}

// Error returns formatted pricing error with provider and model context.
// Indicates cost calculation failures with specific resource identification.
func (e *PricingError) Error() string {
	key := fmt.Sprintf("%s/%s", e.Provider, e.Model)
	if e.Region != "" {
		key = fmt.Sprintf("%s/%s", key, e.Region)
	}
	return fmt.Sprintf("pricing error for %s: %s", key, e.Reason)
}

// IsRetryableError determines if an error warrants retry attempt.
// Examines error types, HTTP status codes, and specific error conditions
// to provide consistent retry decisions across all LLM operations.
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check structured workflow errors first.
	var wfErr *WorkflowError
	if errors.As(err, &wfErr) {
		return wfErr.ShouldRetry()
	}

	// Check ProviderError
	var provErr *ProviderError
	if errors.As(err, &provErr) {
		return provErr.IsRetryable()
	}

	// Check sentinel errors known to be retryable.
	if errors.Is(err, ErrRateLimitExceeded) ||
		errors.Is(err, ErrCircuitBreakerOpen) ||
		errors.Is(err, ErrProviderUnavailable) {
		return true
	}

	// Examine HTTP status codes for retry classification.
	type statusCoder interface {
		StatusCode() int
	}
	if sc, ok := err.(statusCoder); ok {
		code := sc.StatusCode()
		return code == http.StatusTooManyRequests ||
			code == http.StatusRequestTimeout ||
			code == http.StatusGatewayTimeout ||
			code >= 500
	}

	// Conservative default - avoid retry loops for unknown errors.
	return false
}

// IsRateLimitError identifies rate limiting errors for backoff handling.
// Examines multiple error types and patterns to detect rate limit conditions
// requiring specialized retry behavior with appropriate backoff timing.
func IsRateLimitError(err error) bool {
	if err == nil {
		return false
	}

	var rateLimitErr *RateLimitError
	if errors.As(err, &rateLimitErr) {
		return true
	}

	var wfErr *WorkflowError
	if errors.As(err, &wfErr) {
		return wfErr.Type == ErrorTypeRateLimit
	}

	var provErr *ProviderError
	if errors.As(err, &provErr) {
		return provErr.Type == ErrorTypeRateLimit
	}

	return errors.Is(err, ErrRateLimitExceeded)
}

// GetRetryAfter extracts retry-after duration from rate limit errors.
// Returns backoff duration in seconds for optimal retry timing,
// or 0 if no specific retry guidance is available.
func GetRetryAfter(err error) int {
	if err == nil {
		return 0
	}

	var rateLimitErr *RateLimitError
	if errors.As(err, &rateLimitErr) {
		return rateLimitErr.RetryAfter
	}

	var provErr *ProviderError
	if errors.As(err, &provErr) {
		return provErr.RetryAfter
	}

	return 0
}
