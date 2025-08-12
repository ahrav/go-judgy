// Package generation provides LLM answer generation activities for evaluation workflows.
// It defines structured error types for robust error handling across generation attempts,
// with built-in support for retry classification and error categorization.
package generation

import (
	"errors"
	"fmt"
)

// Standard errors returned by generation activities.
// Use these for well-known failure conditions that don't require structured metadata.
var (
	// ErrNotImplemented signals that a requested feature exists in the API but lacks implementation.
	// This is a permanent error that should not be retried.
	ErrNotImplemented = errors.New("not implemented")

	// ErrProviderUnavailable indicates temporary LLM provider unavailability.
	// Activities returning this error should be retried with exponential backoff.
	ErrProviderUnavailable = errors.New("provider unavailable")
)

// ErrorType classifies errors to guide retry logic and escalation decisions.
// Each type implies specific handling behavior for workflow orchestration.
type ErrorType string

const (
	// ErrorValidation signals malformed input that cannot be processed.
	// These errors are non-retryable and require caller correction.
	ErrorValidation ErrorType = "validation"

	// ErrorProvider indicates LLM provider failures like rate limits or service outages.
	// These errors are typically retryable with appropriate backoff.
	ErrorProvider ErrorType = "provider"

	// ErrorBudget signals token or cost limit violations.
	// Retrying requires budget replenishment or limit adjustment.
	ErrorBudget ErrorType = "budget"

	// ErrorInternal represents unexpected failures in generation logic.
	// Retry decisions depend on the specific cause.
	ErrorInternal ErrorType = "internal"
)

// Error provides structured error information for generation failures.
// It supports error wrapping, retry classification, and detailed diagnostics.
// Workflow activities use this type to communicate failure context to orchestrators.
type Error struct {
	// Type classifies the error for routing and retry decisions.
	Type ErrorType
	// Message provides human-readable error context.
	Message string
	// Cause wraps the underlying error for error chain traversal.
	Cause error
	// Retryable indicates whether the operation might succeed if retried.
	Retryable bool
}

// Error formats the error as "<type> error: <message> (<retry-status>)[: <cause>]".
// The format is stable and can be parsed by monitoring systems.
func (e *Error) Error() string {
	retryStr := "non-retryable"
	if e.Retryable {
		retryStr = "retryable"
	}

	if e.Cause != nil {
		return fmt.Sprintf("%s error: %s (%s): %v", e.Type, e.Message, retryStr, e.Cause)
	}
	return fmt.Sprintf("%s error: %s (%s)", e.Type, e.Message, retryStr)
}

// Unwrap supports error chain traversal with errors.Is and errors.As.
// Returns nil if no underlying cause exists.
func (e *Error) Unwrap() error {
	return e.Cause
}
