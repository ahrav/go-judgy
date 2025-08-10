package errors

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWorkflowError(t *testing.T) {
	t.Run("error_message_with_code", func(t *testing.T) {
		wfErr := &WorkflowError{
			Type:      ErrorTypeRateLimit,
			Message:   "Rate limit exceeded",
			Code:      "RATE_LIMIT",
			Retryable: true,
			Details:   map[string]any{"provider": "openai"},
			Cause:     errors.New("underlying error"),
		}

		expected := "[rate_limit:RATE_LIMIT] Rate limit exceeded"
		assert.Equal(t, expected, wfErr.Error())
	})

	t.Run("error_message_without_code", func(t *testing.T) {
		wfErr := &WorkflowError{
			Type:      ErrorTypeTimeout,
			Message:   "Request timeout",
			Code:      "",
			Retryable: true,
			Details:   map[string]any{"duration": "30s"},
			Cause:     errors.New("context deadline exceeded"),
		}

		expected := "[timeout] Request timeout"
		assert.Equal(t, expected, wfErr.Error())
	})

	t.Run("empty_message", func(t *testing.T) {
		wfErr := &WorkflowError{
			Type:    ErrorTypeUnknown,
			Message: "",
			Code:    "UNKNOWN",
		}

		expected := "[unknown:UNKNOWN] "
		assert.Equal(t, expected, wfErr.Error())
	})

	t.Run("empty_type", func(t *testing.T) {
		wfErr := &WorkflowError{
			Type:    "",
			Message: "Test message",
			Code:    "TEST",
		}

		expected := "[:TEST] Test message"
		assert.Equal(t, expected, wfErr.Error())
	})
}

func TestWorkflowErrorUnwrap(t *testing.T) {
	t.Run("unwrap_with_cause", func(t *testing.T) {
		underlyingErr := errors.New("original error")
		wfErr := &WorkflowError{
			Type:    ErrorTypeNetwork,
			Message: "Network error",
			Code:    "NETWORK",
			Cause:   underlyingErr,
		}

		unwrapped := wfErr.Unwrap()
		assert.Equal(t, underlyingErr, unwrapped)
	})

	t.Run("unwrap_without_cause", func(t *testing.T) {
		wfErr := &WorkflowError{
			Type:    ErrorTypeValidation,
			Message: "Validation failed",
			Code:    "VALIDATION",
			Cause:   nil,
		}

		unwrapped := wfErr.Unwrap()
		assert.Nil(t, unwrapped)
	})

	t.Run("error_chain_compatibility", func(t *testing.T) {
		originalErr := errors.New("root cause")
		wfErr := &WorkflowError{
			Type:    ErrorTypeProvider,
			Message: "Provider failed",
			Code:    "PROVIDER",
			Cause:   originalErr,
		}

		// Test errors.Is compatibility
		assert.True(t, errors.Is(wfErr, originalErr))
		assert.False(t, errors.Is(wfErr, errors.New("different error")))

		// Test that we can find the original error in the chain
		assert.True(t, errors.Is(wfErr, originalErr))
	})

	t.Run("nested_workflow_errors", func(t *testing.T) {
		rootErr := errors.New("root cause")
		innerWfErr := &WorkflowError{
			Type:    ErrorTypeTimeout,
			Message: "Inner timeout",
			Code:    "INNER_TIMEOUT",
			Cause:   rootErr,
		}
		outerWfErr := &WorkflowError{
			Type:    ErrorTypeProvider,
			Message: "Provider wrapper",
			Code:    "PROVIDER_WRAPPER",
			Cause:   innerWfErr,
		}

		// Should be able to unwrap to inner workflow error
		assert.Equal(t, innerWfErr, outerWfErr.Unwrap())

		// Should be able to find root cause through chain
		assert.True(t, errors.Is(outerWfErr, rootErr))

		// Should be able to find the outer workflow error (first match)
		var foundErr *WorkflowError
		assert.True(t, errors.As(outerWfErr, &foundErr))
		assert.Equal(t, outerWfErr, foundErr)
	})
}

func TestWorkflowErrorShouldRetry(t *testing.T) {
	t.Run("explicit_retryable_true", func(t *testing.T) {
		wfErr := &WorkflowError{
			Type:      ErrorTypeAuth, // Normally not retryable
			Retryable: true,          // But explicitly set to true
		}

		assert.True(t, wfErr.ShouldRetry())
	})

	t.Run("explicit_retryable_false", func(t *testing.T) {
		wfErr := &WorkflowError{
			Type:      ErrorTypeRateLimit, // Normally retryable
			Retryable: false,              // But explicitly set to false
		}

		assert.False(t, wfErr.ShouldRetry())
	})

	t.Run("retryable_field_precedence", func(t *testing.T) {
		// Test that the explicit Retryable field takes precedence over type-based logic
		testCases := []struct {
			name              string
			errorType         ErrorType
			explicitRetryable bool
			expected          bool
		}{
			{"auth_override_true", ErrorTypeAuth, true, true},
			{"auth_override_false", ErrorTypeAuth, false, false},
			{"rate_limit_override_false", ErrorTypeRateLimit, false, false},
			{"rate_limit_override_true", ErrorTypeRateLimit, true, true},
			{"timeout_override_false", ErrorTypeTimeout, false, false},
			{"validation_override_true", ErrorTypeValidation, true, true},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				wfErr := &WorkflowError{
					Type:      tc.errorType,
					Retryable: tc.explicitRetryable,
				}

				assert.Equal(t, tc.expected, wfErr.ShouldRetry(),
					"Error type %s with explicit retryable=%t should return %t",
					tc.errorType, tc.explicitRetryable, tc.expected)
			})
		}
	})
}

func TestWorkflowErrorIsRetryable(t *testing.T) {
	t.Run("retryable_error_types", func(t *testing.T) {
		retryableTypes := []ErrorType{
			ErrorTypeTimeout,
			ErrorTypeRateLimit,
			ErrorTypeNetwork,
			ErrorTypeProvider,
			ErrorTypeCircuitBreaker,
		}

		for _, errorType := range retryableTypes {
			wfErr := &WorkflowError{
				Type:      errorType,
				Retryable: false, // Set explicit field to opposite to test type-based logic
			}

			assert.True(t, wfErr.IsRetryable(), "Error type %s should be retryable", errorType)
		}
	})

	t.Run("non_retryable_error_types", func(t *testing.T) {
		nonRetryableTypes := []ErrorType{
			ErrorTypeAuth,
			ErrorTypePermission,
			ErrorTypeQuota,
			ErrorTypeValidation,
			ErrorTypeContent,
			ErrorTypeBudget,
			ErrorTypePricingUnavailable,
			ErrorTypeUnknown,
		}

		for _, errorType := range nonRetryableTypes {
			wfErr := &WorkflowError{
				Type:      errorType,
				Retryable: true, // Set explicit field to opposite to test type-based logic
			}

			assert.False(t, wfErr.IsRetryable(), "Error type %s should not be retryable", errorType)
		}
	})

	t.Run("difference_between_should_retry_and_is_retryable", func(t *testing.T) {
		// ShouldRetry uses explicit Retryable field
		// IsRetryable uses type-based classification

		wfErr := &WorkflowError{
			Type:      ErrorTypeAuth, // Not retryable by type
			Retryable: true,          // But explicitly marked retryable
		}

		assert.True(t, wfErr.ShouldRetry(), "ShouldRetry should use explicit Retryable field")
		assert.False(t, wfErr.IsRetryable(), "IsRetryable should use type-based classification")
	})

	t.Run("empty_error_type", func(t *testing.T) {
		wfErr := &WorkflowError{
			Type: "",
		}

		assert.False(t, wfErr.IsRetryable(), "Empty error type should not be retryable")
	})

	t.Run("unknown_error_type", func(t *testing.T) {
		wfErr := &WorkflowError{
			Type: ErrorType("custom_unknown_type"),
		}

		assert.False(t, wfErr.IsRetryable(), "Unknown error type should not be retryable")
	})
}

func TestWorkflowErrorDetails(t *testing.T) {
	t.Run("nil_details", func(t *testing.T) {
		wfErr := &WorkflowError{
			Type:    ErrorTypeTimeout,
			Message: "Test",
			Details: nil,
		}

		assert.Nil(t, wfErr.Details)
	})

	t.Run("empty_details", func(t *testing.T) {
		wfErr := &WorkflowError{
			Type:    ErrorTypeTimeout,
			Message: "Test",
			Details: map[string]any{},
		}

		assert.NotNil(t, wfErr.Details)
		assert.Len(t, wfErr.Details, 0)
	})

	t.Run("populated_details", func(t *testing.T) {
		details := map[string]any{
			"provider":     "openai",
			"status_code":  429,
			"retry_after":  60,
			"user_id":      "user123",
			"request_id":   "req-456",
			"complex_data": map[string]string{"nested": "value"},
		}

		wfErr := &WorkflowError{
			Type:    ErrorTypeRateLimit,
			Message: "Rate limit exceeded",
			Details: details,
		}

		assert.Equal(t, "openai", wfErr.Details["provider"])
		assert.Equal(t, 429, wfErr.Details["status_code"])
		assert.Equal(t, 60, wfErr.Details["retry_after"])
		assert.Equal(t, "user123", wfErr.Details["user_id"])
		assert.Equal(t, "req-456", wfErr.Details["request_id"])

		if nestedData, ok := wfErr.Details["complex_data"].(map[string]string); ok {
			assert.Equal(t, "value", nestedData["nested"])
		}
	})

	t.Run("details_modification", func(t *testing.T) {
		wfErr := &WorkflowError{
			Type:    ErrorTypeValidation,
			Message: "Validation failed",
			Details: map[string]any{"field": "temperature"},
		}

		// Modify details after creation
		wfErr.Details["additional_info"] = "added later"
		wfErr.Details["field"] = "updated_field"

		assert.Equal(t, "added later", wfErr.Details["additional_info"])
		assert.Equal(t, "updated_field", wfErr.Details["field"])
	})
}

func TestWorkflowErrorConstruction(t *testing.T) {
	t.Run("minimal_construction", func(t *testing.T) {
		wfErr := &WorkflowError{
			Type:    ErrorTypeTimeout,
			Message: "Timeout occurred",
		}

		assert.Equal(t, ErrorTypeTimeout, wfErr.Type)
		assert.Equal(t, "Timeout occurred", wfErr.Message)
		assert.Equal(t, "", wfErr.Code)
		assert.False(t, wfErr.Retryable) // Zero value
		assert.Nil(t, wfErr.Details)
		assert.Nil(t, wfErr.Cause)
	})

	t.Run("full_construction", func(t *testing.T) {
		cause := errors.New("underlying cause")
		details := map[string]any{"key": "value"}

		wfErr := &WorkflowError{
			Type:      ErrorTypeRateLimit,
			Message:   "Rate limit exceeded",
			Code:      "RATE_LIMIT",
			Retryable: true,
			Details:   details,
			Cause:     cause,
		}

		assert.Equal(t, ErrorTypeRateLimit, wfErr.Type)
		assert.Equal(t, "Rate limit exceeded", wfErr.Message)
		assert.Equal(t, "RATE_LIMIT", wfErr.Code)
		assert.True(t, wfErr.Retryable)
		assert.Equal(t, details, wfErr.Details)
		assert.Equal(t, cause, wfErr.Cause)
	})

	t.Run("zero_values", func(t *testing.T) {
		wfErr := &WorkflowError{}

		assert.Equal(t, ErrorType(""), wfErr.Type)
		assert.Equal(t, "", wfErr.Message)
		assert.Equal(t, "", wfErr.Code)
		assert.False(t, wfErr.Retryable)
		assert.Nil(t, wfErr.Details)
		assert.Nil(t, wfErr.Cause)
	})
}

func TestWorkflowErrorIntegration(t *testing.T) {
	t.Run("error_wrapping_patterns", func(t *testing.T) {
		originalErr := errors.New("network connection failed")

		wfErr := &WorkflowError{
			Type:      ErrorTypeNetwork,
			Message:   "Network error occurred",
			Code:      "NETWORK_FAILURE",
			Retryable: true,
			Details:   map[string]any{"attempt": 1},
			Cause:     originalErr,
		}

		// Test that it can be wrapped further
		wrappedErr := fmt.Errorf("operation failed: %w", wfErr)

		// Original error should be findable
		assert.True(t, errors.Is(wrappedErr, originalErr))

		// Workflow error should be findable
		var foundWfErr *WorkflowError
		assert.True(t, errors.As(wrappedErr, &foundWfErr))
		assert.Equal(t, wfErr, foundWfErr)

		// Properties should be preserved
		assert.Equal(t, ErrorTypeNetwork, foundWfErr.Type)
		assert.Equal(t, "NETWORK_FAILURE", foundWfErr.Code)
		assert.True(t, foundWfErr.Retryable)
		assert.Equal(t, 1, foundWfErr.Details["attempt"])
	})

	t.Run("classification_integration", func(t *testing.T) {
		// Simulate what ClassifyLLMError might produce
		originalErr := errors.New("rate limit exceeded")

		wfErr := &WorkflowError{
			Type:      ErrorTypeRateLimit,
			Message:   "Rate limit exceeded",
			Code:      "RATE_LIMIT",
			Retryable: true,
			Details:   map[string]any{"original_error": originalErr.Error()},
			Cause:     originalErr,
		}

		// Test retry logic integration
		assert.True(t, wfErr.ShouldRetry())
		assert.True(t, wfErr.IsRetryable())

		// Test error message formatting
		expectedMsg := "[rate_limit:RATE_LIMIT] Rate limit exceeded"
		assert.Equal(t, expectedMsg, wfErr.Error())

		// Test error chain
		assert.True(t, errors.Is(wfErr, originalErr))
	})

	t.Run("json_serialization_considerations", func(t *testing.T) {
		// Note: The Cause field has `json:"-"` tag, so it won't be serialized
		wfErr := &WorkflowError{
			Type:      ErrorTypeValidation,
			Message:   "Invalid input",
			Code:      "VALIDATION_FAILED",
			Retryable: false,
			Details:   map[string]any{"field": "email", "reason": "invalid format"},
			Cause:     errors.New("underlying validation error"),
		}

		// Test that all json-serializable fields are accessible
		assert.Equal(t, ErrorTypeValidation, wfErr.Type)
		assert.Equal(t, "Invalid input", wfErr.Message)
		assert.Equal(t, "VALIDATION_FAILED", wfErr.Code)
		assert.False(t, wfErr.Retryable)
		assert.NotNil(t, wfErr.Details)
		assert.NotNil(t, wfErr.Cause) // Still accessible even though not serialized
	})
}

func TestWorkflowErrorEdgeCases(t *testing.T) {
	t.Run("very_long_error_message", func(t *testing.T) {
		longMessage := "This is a very long error message that contains a lot of details about what went wrong in the system and might be used to test how the error formatting handles extremely verbose error descriptions that could potentially cause issues with logging or display systems"

		wfErr := &WorkflowError{
			Type:    ErrorTypeProvider,
			Message: longMessage,
			Code:    "LONG_ERROR",
		}

		errorStr := wfErr.Error()
		assert.Contains(t, errorStr, "[provider_unavailable:LONG_ERROR]")
		assert.Contains(t, errorStr, longMessage)
	})

	t.Run("special_characters_in_fields", func(t *testing.T) {
		wfErr := &WorkflowError{
			Type:    ErrorType("custom_type_with_underscores"),
			Message: "Error with special chars: !@#$%^&*()[]{}|\\:;\"'<>,.?/~`",
			Code:    "CODE_WITH_UNDERSCORES_AND_NUMBERS_123",
		}

		errorStr := wfErr.Error()
		assert.Contains(t, errorStr, "custom_type_with_underscores")
		assert.Contains(t, errorStr, "CODE_WITH_UNDERSCORES_AND_NUMBERS_123")
		assert.Contains(t, errorStr, "Error with special chars:")
	})

	t.Run("unicode_characters", func(t *testing.T) {
		wfErr := &WorkflowError{
			Type:    ErrorTypeValidation,
			Message: "Validation échouée - 验证失败 - التحقق فشل",
			Code:    "UNICODE_ERROR",
		}

		errorStr := wfErr.Error()
		assert.Contains(t, errorStr, "Validation échouée - 验证失败 - التحقق فشل")
	})

	t.Run("nil_details_map_operations", func(t *testing.T) {
		wfErr := &WorkflowError{
			Type:    ErrorTypeTimeout,
			Message: "Test",
			Details: nil,
		}

		// These operations should not panic
		assert.Nil(t, wfErr.Details)

		// Initialize details map
		wfErr.Details = make(map[string]any)
		wfErr.Details["test"] = "value"

		assert.Equal(t, "value", wfErr.Details["test"])
	})
}
