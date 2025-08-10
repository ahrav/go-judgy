package errors

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestErrorTypes(t *testing.T) {
	// Test that all error types are defined correctly
	expectedTypes := []ErrorType{
		ErrorTypeTimeout,
		ErrorTypeRateLimit,
		ErrorTypeNetwork,
		ErrorTypeProvider,
		ErrorTypeCircuitBreaker,
		ErrorTypeBudget,
		ErrorTypeValidation,
		ErrorTypeContent,
		ErrorTypeAuth,
		ErrorTypePermission,
		ErrorTypeQuota,
		ErrorTypePricingUnavailable,
		ErrorTypeUnknown,
	}

	for _, expectedType := range expectedTypes {
		assert.NotEmpty(t, string(expectedType), "Error type should not be empty")
	}

	// Test specific values
	assert.Equal(t, "timeout", string(ErrorTypeTimeout))
	assert.Equal(t, "rate_limit", string(ErrorTypeRateLimit))
	assert.Equal(t, "network", string(ErrorTypeNetwork))
	assert.Equal(t, "provider_unavailable", string(ErrorTypeProvider))
	assert.Equal(t, "circuit_breaker", string(ErrorTypeCircuitBreaker))
	assert.Equal(t, "budget_exceeded", string(ErrorTypeBudget))
	assert.Equal(t, "validation_failed", string(ErrorTypeValidation))
	assert.Equal(t, "content_filtered", string(ErrorTypeContent))
	assert.Equal(t, "authentication", string(ErrorTypeAuth))
	assert.Equal(t, "permission_denied", string(ErrorTypePermission))
	assert.Equal(t, "quota_exceeded", string(ErrorTypeQuota))
	assert.Equal(t, "pricing_unavailable", string(ErrorTypePricingUnavailable))
	assert.Equal(t, "unknown", string(ErrorTypeUnknown))
}

func TestSentinelErrors(t *testing.T) {
	// Test that all sentinel errors are defined
	sentinelErrors := []error{
		ErrProviderUnavailable,
		ErrRateLimitExceeded,
		ErrCircuitBreakerOpen,
		ErrCacheMiss,
		ErrPricingUnavailable,
		ErrUnknownProvider,
		ErrUnknownModel,
		ErrInvalidResponse,
		ErrJSONValidation,
		ErrMaxRetriesExceeded,
		ErrUnsupportedUsageType,
		ErrUnsupportedProvider,
		ErrUsageNil,
		ErrNegativePromptTokens,
		ErrNegativeCompletionTokens,
		ErrNegativeTotalTokens,
		ErrInconsistentTokenCounts,
		ErrSuspiciouslyHighTokenCount,
	}

	for _, err := range sentinelErrors {
		assert.NotNil(t, err)
		assert.NotEmpty(t, err.Error())
	}

	// Test specific error messages
	assert.Contains(t, ErrProviderUnavailable.Error(), "provider service unavailable")
	assert.Contains(t, ErrRateLimitExceeded.Error(), "rate limit exceeded")
	assert.Contains(t, ErrCircuitBreakerOpen.Error(), "circuit breaker open")
	assert.Contains(t, ErrCacheMiss.Error(), "cache miss")
	assert.Contains(t, ErrPricingUnavailable.Error(), "pricing data unavailable")
}

func TestProviderError(t *testing.T) {
	t.Run("error_message_formatting", func(t *testing.T) {
		providerErr := &ProviderError{
			Provider:   "openai",
			StatusCode: 429,
			Message:    "Rate limit exceeded",
			Code:       "rate_limit_exceeded",
			Type:       ErrorTypeRateLimit,
			RetryAfter: 60,
		}

		expected := "openai error (status 429): Rate limit exceeded"
		assert.Equal(t, expected, providerErr.Error())
	})

	t.Run("is_retryable_true_cases", func(t *testing.T) {
		retryableTypes := []ErrorType{
			ErrorTypeTimeout,
			ErrorTypeRateLimit,
			ErrorTypeNetwork,
			ErrorTypeProvider,
		}

		for _, errorType := range retryableTypes {
			providerErr := &ProviderError{Type: errorType}
			assert.True(t, providerErr.IsRetryable(), "Error type %s should be retryable", errorType)
		}
	})

	t.Run("is_retryable_false_cases", func(t *testing.T) {
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
			providerErr := &ProviderError{Type: errorType}
			assert.False(t, providerErr.IsRetryable(), "Error type %s should not be retryable", errorType)
		}
	})

	t.Run("get_retry_after_with_value", func(t *testing.T) {
		providerErr := &ProviderError{
			RetryAfter: 120,
		}

		duration := providerErr.GetRetryAfter()
		assert.Equal(t, 2*time.Minute, duration)
	})

	t.Run("get_retry_after_zero_value", func(t *testing.T) {
		providerErr := &ProviderError{
			RetryAfter: 0,
		}

		duration := providerErr.GetRetryAfter()
		assert.Equal(t, time.Duration(0), duration)
	})

	t.Run("get_retry_after_negative_value", func(t *testing.T) {
		providerErr := &ProviderError{
			RetryAfter: -10,
		}

		duration := providerErr.GetRetryAfter()
		// Negative values should be treated as zero
		assert.Equal(t, time.Duration(0), duration)
	})
}

func TestRateLimitError(t *testing.T) {
	t.Run("error_message_with_retry_after", func(t *testing.T) {
		rateLimitErr := &RateLimitError{
			Provider:   "anthropic",
			RetryAfter: 60,
			ResetAt:    1234567890,
			Limit:      1000,
			Remaining:  0,
			LocalLimit: false,
		}

		expected := "rate limit exceeded for anthropic, retry after 60 seconds"
		assert.Equal(t, expected, rateLimitErr.Error())
	})

	t.Run("error_message_without_retry_after", func(t *testing.T) {
		rateLimitErr := &RateLimitError{
			Provider:   "openai",
			RetryAfter: 0,
			Limit:      500,
			Remaining:  0,
			LocalLimit: true,
		}

		expected := "rate limit exceeded for openai"
		assert.Equal(t, expected, rateLimitErr.Error())
	})

	t.Run("get_retry_after_with_value", func(t *testing.T) {
		rateLimitErr := &RateLimitError{
			RetryAfter: 30,
		}

		duration := rateLimitErr.GetRetryAfter()
		assert.Equal(t, 30*time.Second, duration)
	})

	t.Run("get_retry_after_zero_value", func(t *testing.T) {
		rateLimitErr := &RateLimitError{
			RetryAfter: 0,
		}

		duration := rateLimitErr.GetRetryAfter()
		assert.Equal(t, time.Duration(0), duration)
	})

	t.Run("local_vs_remote_limit", func(t *testing.T) {
		localLimit := &RateLimitError{LocalLimit: true}
		remoteLimit := &RateLimitError{LocalLimit: false}

		assert.True(t, localLimit.LocalLimit)
		assert.False(t, remoteLimit.LocalLimit)
	})
}

func TestCircuitBreakerError(t *testing.T) {
	t.Run("error_message_formatting", func(t *testing.T) {
		cbErr := &CircuitBreakerError{
			Provider: "google",
			Model:    "gemini-pro",
			State:    "open",
			ResetAt:  1234567890,
		}

		expected := "circuit breaker open for google/gemini-pro"
		assert.Equal(t, expected, cbErr.Error())
	})

	t.Run("different_states", func(t *testing.T) {
		states := []string{"open", "half-open", "closed"}

		for _, state := range states {
			cbErr := &CircuitBreakerError{
				Provider: "provider",
				Model:    "model",
				State:    state,
			}

			expected := fmt.Sprintf("circuit breaker %s for provider/model", state)
			assert.Equal(t, expected, cbErr.Error())
		}
	})

	t.Run("empty_model", func(t *testing.T) {
		cbErr := &CircuitBreakerError{
			Provider: "openai",
			Model:    "",
			State:    "open",
		}

		expected := "circuit breaker open for openai/"
		assert.Equal(t, expected, cbErr.Error())
	})
}

func TestValidationError(t *testing.T) {
	t.Run("error_message_with_field", func(t *testing.T) {
		valErr := &ValidationError{
			Field:   "temperature",
			Value:   2.5,
			Message: "Temperature must be between 0 and 2",
			Schema:  map[string]interface{}{"min": 0, "max": 2},
		}

		expected := "validation failed for field temperature: Temperature must be between 0 and 2"
		assert.Equal(t, expected, valErr.Error())
	})

	t.Run("error_message_without_field", func(t *testing.T) {
		valErr := &ValidationError{
			Field:   "",
			Value:   nil,
			Message: "Invalid request format",
		}

		expected := "validation failed: Invalid request format"
		assert.Equal(t, expected, valErr.Error())
	})

	t.Run("different_value_types", func(t *testing.T) {
		testCases := []struct {
			name  string
			value interface{}
		}{
			{"string_value", "invalid"},
			{"numeric_value", 42},
			{"bool_value", true},
			{"nil_value", nil},
			{"struct_value", map[string]string{"key": "value"}},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				valErr := &ValidationError{
					Field:   "test_field",
					Value:   tc.value,
					Message: "Test validation error",
				}

				assert.Contains(t, valErr.Error(), "validation failed for field test_field")
			})
		}
	})
}

func TestPricingError(t *testing.T) {
	t.Run("error_message_with_region", func(t *testing.T) {
		pricingErr := &PricingError{
			Provider: "openai",
			Model:    "gpt-4",
			Region:   "us-east-1",
			Reason:   "Pricing data not available",
			Type:     ErrorTypePricingUnavailable,
		}

		expected := "pricing error for openai/gpt-4/us-east-1: Pricing data not available"
		assert.Equal(t, expected, pricingErr.Error())
	})

	t.Run("error_message_without_region", func(t *testing.T) {
		pricingErr := &PricingError{
			Provider: "anthropic",
			Model:    "claude-3",
			Region:   "",
			Reason:   "Model not found in pricing database",
			Type:     ErrorTypePricingUnavailable,
		}

		expected := "pricing error for anthropic/claude-3: Model not found in pricing database"
		assert.Equal(t, expected, pricingErr.Error())
	})

	t.Run("different_pricing_error_types", func(t *testing.T) {
		errorTypes := []ErrorType{
			ErrorTypePricingUnavailable,
			ErrorTypeBudget,
			ErrorTypeValidation,
		}

		for _, errorType := range errorTypes {
			pricingErr := &PricingError{
				Provider: "provider",
				Model:    "model",
				Reason:   "test reason",
				Type:     errorType,
			}

			assert.Equal(t, errorType, pricingErr.Type)
		}
	})
}

func TestIsRetryableError(t *testing.T) {
	t.Run("nil_error", func(t *testing.T) {
		assert.False(t, IsRetryableError(nil))
	})

	t.Run("workflow_error_retryable", func(t *testing.T) {
		wfErr := &WorkflowError{
			Type:      ErrorTypeRateLimit,
			Retryable: true,
		}

		assert.True(t, IsRetryableError(wfErr))
	})

	t.Run("workflow_error_non_retryable", func(t *testing.T) {
		wfErr := &WorkflowError{
			Type:      ErrorTypeAuth,
			Retryable: false,
		}

		assert.False(t, IsRetryableError(wfErr))
	})

	t.Run("provider_error_retryable", func(t *testing.T) {
		provErr := &ProviderError{
			Type: ErrorTypeRateLimit,
		}

		assert.True(t, IsRetryableError(provErr))
	})

	t.Run("provider_error_non_retryable", func(t *testing.T) {
		provErr := &ProviderError{
			Type: ErrorTypeAuth,
		}

		assert.False(t, IsRetryableError(provErr))
	})

	t.Run("sentinel_errors_retryable", func(t *testing.T) {
		retryableSentinelErrors := []error{
			ErrRateLimitExceeded,
			ErrCircuitBreakerOpen,
			ErrProviderUnavailable,
		}

		for _, err := range retryableSentinelErrors {
			assert.True(t, IsRetryableError(err), "Error %v should be retryable", err)
		}
	})

	t.Run("sentinel_errors_non_retryable", func(t *testing.T) {
		nonRetryableSentinelErrors := []error{
			ErrPricingUnavailable,
			ErrUnknownProvider,
			ErrJSONValidation,
			ErrMaxRetriesExceeded,
		}

		for _, err := range nonRetryableSentinelErrors {
			assert.False(t, IsRetryableError(err), "Error %v should not be retryable", err)
		}
	})

	t.Run("wrapped_sentinel_errors", func(t *testing.T) {
		wrappedRetryable := fmt.Errorf("operation failed: %w", ErrRateLimitExceeded)
		assert.True(t, IsRetryableError(wrappedRetryable))

		wrappedNonRetryable := fmt.Errorf("validation failed: %w", ErrJSONValidation)
		assert.False(t, IsRetryableError(wrappedNonRetryable))
	})

	t.Run("http_status_codes_retryable", func(t *testing.T) {
		retryableStatuses := []int{
			http.StatusTooManyRequests,
			http.StatusRequestTimeout,
			http.StatusGatewayTimeout,
			http.StatusInternalServerError,
			http.StatusBadGateway,
			http.StatusServiceUnavailable,
		}

		for _, status := range retryableStatuses {
			err := &mockStatusCodeError{statusCode: status}
			assert.True(t, IsRetryableError(err), "Status code %d should be retryable", status)
		}
	})

	t.Run("http_status_codes_non_retryable", func(t *testing.T) {
		nonRetryableStatuses := []int{
			http.StatusBadRequest,
			http.StatusUnauthorized,
			http.StatusForbidden,
			http.StatusNotFound,
			http.StatusMethodNotAllowed,
			http.StatusConflict,
			http.StatusUnprocessableEntity,
		}

		for _, status := range nonRetryableStatuses {
			err := &mockStatusCodeError{statusCode: status}
			assert.False(t, IsRetryableError(err), "Status code %d should not be retryable", status)
		}
	})

	t.Run("generic_error", func(t *testing.T) {
		err := errors.New("generic error")
		assert.False(t, IsRetryableError(err))
	})
}

func TestIsRateLimitError(t *testing.T) {
	t.Run("nil_error", func(t *testing.T) {
		assert.False(t, IsRateLimitError(nil))
	})

	t.Run("rate_limit_error_direct", func(t *testing.T) {
		rateLimitErr := &RateLimitError{
			Provider: "openai",
		}

		assert.True(t, IsRateLimitError(rateLimitErr))
	})

	t.Run("workflow_error_rate_limit", func(t *testing.T) {
		wfErr := &WorkflowError{
			Type: ErrorTypeRateLimit,
		}

		assert.True(t, IsRateLimitError(wfErr))
	})

	t.Run("workflow_error_non_rate_limit", func(t *testing.T) {
		wfErr := &WorkflowError{
			Type: ErrorTypeTimeout,
		}

		assert.False(t, IsRateLimitError(wfErr))
	})

	t.Run("provider_error_rate_limit", func(t *testing.T) {
		provErr := &ProviderError{
			Type: ErrorTypeRateLimit,
		}

		assert.True(t, IsRateLimitError(provErr))
	})

	t.Run("provider_error_non_rate_limit", func(t *testing.T) {
		provErr := &ProviderError{
			Type: ErrorTypeAuth,
		}

		assert.False(t, IsRateLimitError(provErr))
	})

	t.Run("sentinel_rate_limit_error", func(t *testing.T) {
		assert.True(t, IsRateLimitError(ErrRateLimitExceeded))
	})

	t.Run("wrapped_sentinel_rate_limit_error", func(t *testing.T) {
		wrappedErr := fmt.Errorf("API call failed: %w", ErrRateLimitExceeded)
		assert.True(t, IsRateLimitError(wrappedErr))
	})

	t.Run("non_rate_limit_sentinel_error", func(t *testing.T) {
		assert.False(t, IsRateLimitError(ErrProviderUnavailable))
	})

	t.Run("generic_error", func(t *testing.T) {
		err := errors.New("generic error")
		assert.False(t, IsRateLimitError(err))
	})
}

func TestGetRetryAfter(t *testing.T) {
	t.Run("nil_error", func(t *testing.T) {
		assert.Equal(t, 0, GetRetryAfter(nil))
	})

	t.Run("rate_limit_error", func(t *testing.T) {
		rateLimitErr := &RateLimitError{
			RetryAfter: 120,
		}

		assert.Equal(t, 120, GetRetryAfter(rateLimitErr))
	})

	t.Run("provider_error", func(t *testing.T) {
		provErr := &ProviderError{
			RetryAfter: 60,
		}

		assert.Equal(t, 60, GetRetryAfter(provErr))
	})

	t.Run("wrapped_rate_limit_error", func(t *testing.T) {
		rateLimitErr := &RateLimitError{
			RetryAfter: 90,
		}
		wrappedErr := fmt.Errorf("API failed: %w", rateLimitErr)

		assert.Equal(t, 90, GetRetryAfter(wrappedErr))
	})

	t.Run("wrapped_provider_error", func(t *testing.T) {
		provErr := &ProviderError{
			RetryAfter: 45,
		}
		wrappedErr := fmt.Errorf("request failed: %w", provErr)

		assert.Equal(t, 45, GetRetryAfter(wrappedErr))
	})

	t.Run("generic_error", func(t *testing.T) {
		err := errors.New("generic error")
		assert.Equal(t, 0, GetRetryAfter(err))
	})

	t.Run("zero_retry_after", func(t *testing.T) {
		rateLimitErr := &RateLimitError{
			RetryAfter: 0,
		}

		assert.Equal(t, 0, GetRetryAfter(rateLimitErr))
	})

	t.Run("multiple_error_types_in_chain", func(t *testing.T) {
		// Rate limit error should be found first in the chain
		rateLimitErr := &RateLimitError{RetryAfter: 30}

		// Wrap rate limit error
		wrappedErr := fmt.Errorf("rate limit: %w", rateLimitErr)

		assert.Equal(t, 30, GetRetryAfter(wrappedErr))
	})
}

// Mock error type for testing HTTP status code interface
type mockStatusCodeError struct {
	statusCode int
}

func (e *mockStatusCodeError) Error() string {
	return fmt.Sprintf("HTTP error: %d", e.statusCode)
}

func (e *mockStatusCodeError) StatusCode() int {
	return e.statusCode
}

func TestErrorChaining(t *testing.T) {
	t.Run("provider_error_chain", func(t *testing.T) {
		provErr := &ProviderError{
			Provider:   "openai",
			StatusCode: 503,
			Message:    "Service unavailable",
			Type:       ErrorTypeProvider,
		}

		// Test that provider error can be identified in chain
		wrappedErr := fmt.Errorf("API call failed: %w", provErr)

		var extractedProvErr *ProviderError
		assert.True(t, errors.As(wrappedErr, &extractedProvErr))
		assert.Equal(t, "openai", extractedProvErr.Provider)
		assert.Equal(t, 503, extractedProvErr.StatusCode)
	})

	t.Run("multiple_error_types_chain", func(t *testing.T) {
		// Create a chain with rate limit error
		rateLimitErr := &RateLimitError{Provider: "anthropic", RetryAfter: 60}

		// Wrap the error
		err1 := fmt.Errorf("rate limit hit: %w", rateLimitErr)

		// Should be detectable
		var extractedRateLimit *RateLimitError

		assert.True(t, errors.As(err1, &extractedRateLimit))
		assert.Equal(t, "anthropic", extractedRateLimit.Provider)
		assert.Equal(t, 60, extractedRateLimit.RetryAfter)
	})
}

func TestErrorTypeConstants(t *testing.T) {
	// Ensure error type constants haven't been accidentally changed
	expectedConstants := map[ErrorType]string{
		ErrorTypeTimeout:            "timeout",
		ErrorTypeRateLimit:          "rate_limit",
		ErrorTypeNetwork:            "network",
		ErrorTypeProvider:           "provider_unavailable",
		ErrorTypeCircuitBreaker:     "circuit_breaker",
		ErrorTypeBudget:             "budget_exceeded",
		ErrorTypeValidation:         "validation_failed",
		ErrorTypeContent:            "content_filtered",
		ErrorTypeAuth:               "authentication",
		ErrorTypePermission:         "permission_denied",
		ErrorTypeQuota:              "quota_exceeded",
		ErrorTypePricingUnavailable: "pricing_unavailable",
		ErrorTypeUnknown:            "unknown",
	}

	for errorType, expectedValue := range expectedConstants {
		assert.Equal(t, expectedValue, string(errorType),
			"Error type %v should have value %s", errorType, expectedValue)
	}
}
