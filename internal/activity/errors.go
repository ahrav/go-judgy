package activity

import (
	"errors"
	"fmt"

	"go.temporal.io/sdk/temporal"
)

// Activity-specific errors for operational and business failure classification.
var (
	// ErrNotImplemented indicates the activity has not been implemented yet.
	ErrNotImplemented = errors.New("activity not implemented")

	// ErrActivityValidation is returned when activity input validation fails
	// due to missing required fields or constraint violations.
	// This is a non-retryable error indicating programming errors.
	ErrActivityValidation = errors.New("activity input validation failed")

	// ErrProviderUnavailable is returned when LLM or external service providers
	// are temporarily unavailable. This is a retryable error with exponential backoff.
	ErrProviderUnavailable = errors.New("external provider unavailable")

	// ErrProviderRateLimit is returned when API rate limits are exceeded.
	// This is a retryable error requiring backoff strategies.
	ErrProviderRateLimit = errors.New("provider rate limit exceeded")

	// ErrSchemaValidation is returned when response schemas cannot be validated
	// or repaired after maximum attempts. This may be retryable depending on context.
	ErrSchemaValidation = errors.New("schema validation failed after repair attempts")

	// ErrInsufficientBudget is returned when remaining budget is insufficient
	// for requested operations. This is a non-retryable application error.
	ErrInsufficientBudget = errors.New("insufficient budget for operation")

	// ErrActivityTimeout is returned when activity execution exceeds configured
	// timeout limits without proper heartbeat reporting.
	ErrActivityTimeout = errors.New("activity execution timeout")
)

// Error represents an activity execution error with detailed context
// for retry decision making and operational debugging.
type Error struct {
	// Type indicates the error category for retry and escalation decisions.
	Type ErrorType

	// Message provides human-readable error description with actionable details.
	Message string

	// Cause contains the underlying error that triggered this activity error.
	Cause error

	// Retryable indicates whether this error should trigger automatic retries.
	Retryable bool

	// Context provides additional metadata for error analysis and debugging.
	Context map[string]any
}

// Error implements the error interface with comprehensive error information.
func (e *Error) Error() string {
	retryInfo := "non-retryable"
	if e.Retryable {
		retryInfo = "retryable"
	}

	if e.Cause != nil {
		return fmt.Sprintf("activity error [%s, %s]: %s: %v", e.Type, retryInfo, e.Message, e.Cause)
	}
	return fmt.Sprintf("activity error [%s, %s]: %s", e.Type, retryInfo, e.Message)
}

// Unwrap returns the underlying cause error for error chain inspection.
func (e *Error) Unwrap() error {
	return e.Cause
}

// ErrorType categorizes activity errors for appropriate handling strategies.
type ErrorType string

// Activity error types for comprehensive error classification and handling.
const (
	// ErrorValidation indicates input validation or configuration
	// errors that are non-retryable and require immediate correction.
	ErrorValidation ErrorType = "validation"

	// ErrorProvider indicates external provider failures that
	// may be retryable with appropriate backoff strategies.
	ErrorProvider ErrorType = "provider"

	// ErrorSchema indicates response parsing or validation
	// failures that may be retryable after repair attempts.
	ErrorSchema ErrorType = "schema"

	// ErrorBudget indicates resource limit violations that
	// are non-retryable application errors requiring workflow decisions.
	ErrorBudget ErrorType = "budget"

	// ErrorTimeout indicates execution time limit exceeded,
	// which may be retryable depending on the underlying cause.
	ErrorTimeout ErrorType = "timeout"

	// ErrorBusiness indicates business logic failures that
	// should be handled at the workflow level rather than retried.
	ErrorBusiness ErrorType = "business"
)

// nonRetryable wraps an error as a Temporal non-retryable application error.
// This helper standardizes error creation for programming errors, validation
// failures, and other conditions that should not trigger automatic retries.
// The tag parameter categorizes the error type for monitoring and debugging.
func nonRetryable(tag string, cause error, msg string) error {
	return temporal.NewNonRetryableApplicationError(msg, tag, cause)
}
