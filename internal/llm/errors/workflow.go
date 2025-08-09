package errors

import (
	"fmt"
)

// WorkflowError provides comprehensive error context for workflow operations.
// Includes error classification for retry decisions, human-readable messages,
// provider-specific error codes, and structured details for observability.
type WorkflowError struct {
	Type      ErrorType      `json:"type"`      // Error classification
	Message   string         `json:"message"`   // Human-readable message
	Code      string         `json:"code"`      // Provider-specific error code
	Retryable bool           `json:"retryable"` // Whether to retry
	Details   map[string]any `json:"details"`   // Additional context
	Cause     error          `json:"-"`         // Underlying error
}

// Error returns formatted error string with type and code context.
// Provides structured error representation for logging and debugging.
func (e *WorkflowError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("[%s:%s] %s", e.Type, e.Code, e.Message)
	}
	return fmt.Sprintf("[%s] %s", e.Type, e.Message)
}

// Unwrap returns the underlying error for errors.Is/As compatibility.
// Enables error chain traversal and type-based error handling.
func (e *WorkflowError) Unwrap() error {
	return e.Cause
}

// ShouldRetry returns the explicit retry recommendation.
// Uses the Retryable field which may override default type-based retry logic.
func (e *WorkflowError) ShouldRetry() bool {
	return e.Retryable
}

// IsRetryable determines retry eligibility based on error type classification.
// Returns true for transient errors (timeouts, rate limits, network issues)
// and false for permanent errors (auth failures, quota exceeded).
func (e *WorkflowError) IsRetryable() bool {
	switch e.Type {
	case ErrorTypeTimeout, ErrorTypeRateLimit, ErrorTypeNetwork, ErrorTypeProvider, ErrorTypeCircuitBreaker:
		return true
	default:
		return false
	}
}
