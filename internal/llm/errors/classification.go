package errors

import (
	"errors"
	"strings"
)

// ClassifyLLMError transforms LLM operation errors into WorkflowError with retry guidance.
// Examines error types, HTTP status codes, and message patterns to determine
// appropriate error classification, retry behavior, and structured context.
func ClassifyLLMError(err error) *WorkflowError {
	if err == nil {
		return nil
	}

	// Check for strongly-typed errors first.
	if workflowErr := classifyTypedErrors(err); workflowErr != nil {
		return workflowErr
	}

	// Check for sentinel errors using errors.Is.
	if workflowErr := classifySentinelErrors(err); workflowErr != nil {
		return workflowErr
	}

	// Fallback to string pattern matching for untyped errors.
	return classifyStringPatternErrors(err)
}

// classifyTypedErrors handles strongly-typed error classification.
// Processes ProviderError, RateLimitError, CircuitBreakerError, ValidationError,
// and PricingError types with appropriate retry guidance and context details.
func classifyTypedErrors(err error) *WorkflowError {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return &WorkflowError{
			Type:      providerErr.Type,
			Message:   providerErr.Message,
			Code:      providerErr.Code,
			Retryable: providerErr.IsRetryable(),
			Details: map[string]any{
				"provider":    providerErr.Provider,
				"status_code": providerErr.StatusCode,
			},
			Cause: err,
		}
	}

	var rateLimitErr *RateLimitError
	if errors.As(err, &rateLimitErr) {
		return &WorkflowError{
			Type:      ErrorTypeRateLimit,
			Message:   rateLimitErr.Error(),
			Code:      "RATE_LIMIT",
			Retryable: true,
			Details: map[string]any{
				"provider":    rateLimitErr.Provider,
				"retry_after": rateLimitErr.RetryAfter,
			},
			Cause: err,
		}
	}

	var cbErr *CircuitBreakerError
	if errors.As(err, &cbErr) {
		return &WorkflowError{
			Type:      ErrorTypeCircuitBreaker,
			Message:   cbErr.Error(),
			Code:      "CIRCUIT_BREAKER",
			Retryable: true,
			Details: map[string]any{
				"provider": cbErr.Provider,
				"model":    cbErr.Model,
				"state":    cbErr.State,
			},
			Cause: err,
		}
	}

	var valErr *ValidationError
	if errors.As(err, &valErr) {
		return &WorkflowError{
			Type:      ErrorTypeValidation,
			Message:   valErr.Error(),
			Code:      "VALIDATION",
			Retryable: false,
			Details: map[string]any{
				"field": valErr.Field,
				"value": valErr.Value,
			},
			Cause: err,
		}
	}

	var pricingErr *PricingError
	if errors.As(err, &pricingErr) {
		return &WorkflowError{
			Type:      pricingErr.Type,
			Message:   pricingErr.Error(),
			Code:      "PRICING",
			Retryable: false,
			Details: map[string]any{
				"provider": pricingErr.Provider,
				"model":    pricingErr.Model,
				"region":   pricingErr.Region,
			},
			Cause: err,
		}
	}

	return nil
}

// classifySentinelErrors handles sentinel error classification.
// Processes sentinel errors using errors.Is for rate limits, circuit breakers,
// provider availability, pricing, and retry limits with appropriate retry guidance.
func classifySentinelErrors(err error) *WorkflowError {
	switch {
	case errors.Is(err, ErrRateLimitExceeded):
		return &WorkflowError{
			Type:      ErrorTypeRateLimit,
			Message:   err.Error(),
			Code:      "RATE_LIMIT",
			Retryable: true,
			Cause:     err,
		}
	case errors.Is(err, ErrCircuitBreakerOpen):
		return &WorkflowError{
			Type:      ErrorTypeCircuitBreaker,
			Message:   err.Error(),
			Code:      "CIRCUIT_BREAKER",
			Retryable: true,
			Cause:     err,
		}
	case errors.Is(err, ErrProviderUnavailable):
		return &WorkflowError{
			Type:      ErrorTypeProvider,
			Message:   err.Error(),
			Code:      "PROVIDER_UNAVAILABLE",
			Retryable: true,
			Cause:     err,
		}
	case errors.Is(err, ErrPricingUnavailable):
		return &WorkflowError{
			Type:      ErrorTypePricingUnavailable,
			Message:   err.Error(),
			Code:      "PRICING_UNAVAILABLE",
			Retryable: false,
			Cause:     err,
		}
	case errors.Is(err, ErrMaxRetriesExceeded):
		return &WorkflowError{
			Type:      ErrorTypeProvider,
			Message:   err.Error(),
			Code:      "MAX_RETRIES",
			Retryable: false,
			Details:   map[string]any{"original_error": err.Error()},
			Cause:     err,
		}
	}

	return nil
}

// classifyStringPatternErrors handles untyped error classification.
// Performs string pattern matching on error messages to classify
// rate limits, timeouts, authentication, permission, quota, and network errors.
func classifyStringPatternErrors(err error) *WorkflowError {
	errMsg := strings.ToLower(err.Error())

	switch {
	case strings.Contains(errMsg, "rate limit"):
		return &WorkflowError{
			Type:      ErrorTypeRateLimit,
			Message:   "Rate limit exceeded",
			Code:      "RATE_LIMIT",
			Retryable: true,
			Details:   map[string]any{"original_error": err.Error()},
			Cause:     err,
		}
	case strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "deadline"):
		return &WorkflowError{
			Type:      ErrorTypeTimeout,
			Message:   "Request timeout",
			Code:      "TIMEOUT",
			Retryable: true,
			Details:   map[string]any{"original_error": err.Error()},
			Cause:     err,
		}
	case strings.Contains(errMsg, "unauthorized") || strings.Contains(errMsg, "authentication"):
		return &WorkflowError{
			Type:      ErrorTypeAuth,
			Message:   "Authentication failed",
			Code:      "AUTH_FAILED",
			Retryable: false,
			Details:   map[string]any{"original_error": err.Error()},
			Cause:     err,
		}
	case strings.Contains(errMsg, "forbidden") || strings.Contains(errMsg, "permission"):
		return &WorkflowError{
			Type:      ErrorTypePermission,
			Message:   "Permission denied",
			Code:      "PERMISSION_DENIED",
			Retryable: false,
			Details:   map[string]any{"original_error": err.Error()},
			Cause:     err,
		}
	case strings.Contains(errMsg, "quota"):
		return &WorkflowError{
			Type:      ErrorTypeQuota,
			Message:   "Quota exceeded",
			Code:      "QUOTA_EXCEEDED",
			Retryable: false,
			Details:   map[string]any{"original_error": err.Error()},
			Cause:     err,
		}
	case strings.Contains(errMsg, "network") || strings.Contains(errMsg, "connection"):
		return &WorkflowError{
			Type:      ErrorTypeNetwork,
			Message:   "Network error",
			Code:      "NETWORK_ERROR",
			Retryable: true,
			Details:   map[string]any{"original_error": err.Error()},
			Cause:     err,
		}
	default:
		return &WorkflowError{
			Type:      ErrorTypeUnknown,
			Message:   "Unknown error",
			Code:      "UNKNOWN",
			Retryable: false,
			Details:   map[string]any{"original_error": err.Error()},
			Cause:     err,
		}
	}
}
