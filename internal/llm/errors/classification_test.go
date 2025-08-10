package errors

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyLLMError(t *testing.T) {
	t.Run("nil_error", func(t *testing.T) {
		result := ClassifyLLMError(nil)
		assert.Nil(t, result)
	})

	t.Run("provider_error_classification", func(t *testing.T) {
		providerErr := &ProviderError{
			Provider:   "openai",
			StatusCode: http.StatusTooManyRequests,
			Message:    "Rate limit exceeded",
			Code:       "rate_limit_exceeded",
			Type:       ErrorTypeRateLimit,
			RetryAfter: 60,
		}

		result := ClassifyLLMError(providerErr)
		require.NotNil(t, result)
		assert.Equal(t, ErrorTypeRateLimit, result.Type)
		assert.Equal(t, "Rate limit exceeded", result.Message)
		assert.Equal(t, "rate_limit_exceeded", result.Code)
		assert.True(t, result.Retryable)
		assert.Equal(t, "openai", result.Details["provider"])
		assert.Equal(t, http.StatusTooManyRequests, result.Details["status_code"])
		assert.Equal(t, providerErr, result.Cause)
	})

	t.Run("rate_limit_error_classification", func(t *testing.T) {
		rateLimitErr := &RateLimitError{
			Provider:   "anthropic",
			RetryAfter: 120,
			ResetAt:    1234567890,
			Limit:      1000,
			Remaining:  0,
			LocalLimit: false,
		}

		result := ClassifyLLMError(rateLimitErr)
		require.NotNil(t, result)
		assert.Equal(t, ErrorTypeRateLimit, result.Type)
		assert.Equal(t, "RATE_LIMIT", result.Code)
		assert.True(t, result.Retryable)
		assert.Equal(t, "anthropic", result.Details["provider"])
		assert.Equal(t, 120, result.Details["retry_after"])
		assert.Equal(t, rateLimitErr, result.Cause)
	})

	t.Run("circuit_breaker_error_classification", func(t *testing.T) {
		cbErr := &CircuitBreakerError{
			Provider: "google",
			Model:    "gemini-pro",
			State:    "open",
			ResetAt:  1234567890,
		}

		result := ClassifyLLMError(cbErr)
		require.NotNil(t, result)
		assert.Equal(t, ErrorTypeCircuitBreaker, result.Type)
		assert.Equal(t, "CIRCUIT_BREAKER", result.Code)
		assert.True(t, result.Retryable)
		assert.Equal(t, "google", result.Details["provider"])
		assert.Equal(t, "gemini-pro", result.Details["model"])
		assert.Equal(t, "open", result.Details["state"])
		assert.Equal(t, cbErr, result.Cause)
	})

	t.Run("validation_error_classification", func(t *testing.T) {
		valErr := &ValidationError{
			Field:   "temperature",
			Value:   2.5,
			Message: "Temperature must be between 0 and 2",
			Schema:  map[string]interface{}{"min": 0, "max": 2},
		}

		result := ClassifyLLMError(valErr)
		require.NotNil(t, result)
		assert.Equal(t, ErrorTypeValidation, result.Type)
		assert.Equal(t, "VALIDATION", result.Code)
		assert.False(t, result.Retryable)
		assert.Equal(t, "temperature", result.Details["field"])
		assert.Equal(t, 2.5, result.Details["value"])
		assert.Equal(t, valErr, result.Cause)
	})

	t.Run("pricing_error_classification", func(t *testing.T) {
		pricingErr := &PricingError{
			Provider: "openai",
			Model:    "gpt-4",
			Region:   "us-east-1",
			Reason:   "Pricing data not available",
			Type:     ErrorTypePricingUnavailable,
		}

		result := ClassifyLLMError(pricingErr)
		require.NotNil(t, result)
		assert.Equal(t, ErrorTypePricingUnavailable, result.Type)
		assert.Equal(t, "PRICING", result.Code)
		assert.False(t, result.Retryable)
		assert.Equal(t, "openai", result.Details["provider"])
		assert.Equal(t, "gpt-4", result.Details["model"])
		assert.Equal(t, "us-east-1", result.Details["region"])
		assert.Equal(t, pricingErr, result.Cause)
	})
}

func TestClassifyTypedErrors(t *testing.T) {
	t.Run("non_matching_error", func(t *testing.T) {
		err := errors.New("generic error")
		result := classifyTypedErrors(err)
		assert.Nil(t, result)
	})

	t.Run("provider_error_non_retryable", func(t *testing.T) {
		providerErr := &ProviderError{
			Provider:   "openai",
			StatusCode: http.StatusUnauthorized,
			Message:    "Invalid API key",
			Code:       "invalid_api_key",
			Type:       ErrorTypeAuth,
			RetryAfter: 0,
		}

		result := classifyTypedErrors(providerErr)
		require.NotNil(t, result)
		assert.Equal(t, ErrorTypeAuth, result.Type)
		assert.False(t, result.Retryable)
		assert.Equal(t, "openai", result.Details["provider"])
		assert.Equal(t, http.StatusUnauthorized, result.Details["status_code"])
	})

	t.Run("rate_limit_error_with_local_limit", func(t *testing.T) {
		rateLimitErr := &RateLimitError{
			Provider:   "anthropic",
			RetryAfter: 60,
			LocalLimit: true,
		}

		result := classifyTypedErrors(rateLimitErr)
		require.NotNil(t, result)
		assert.Equal(t, ErrorTypeRateLimit, result.Type)
		assert.True(t, result.Retryable)
		assert.Equal(t, "anthropic", result.Details["provider"])
		assert.Equal(t, 60, result.Details["retry_after"])
	})

	t.Run("circuit_breaker_half_open", func(t *testing.T) {
		cbErr := &CircuitBreakerError{
			Provider: "google",
			Model:    "gemini-pro",
			State:    "half-open",
			ResetAt:  1234567890,
		}

		result := classifyTypedErrors(cbErr)
		require.NotNil(t, result)
		assert.Equal(t, ErrorTypeCircuitBreaker, result.Type)
		assert.True(t, result.Retryable)
		assert.Equal(t, "half-open", result.Details["state"])
	})

	t.Run("validation_error_no_field", func(t *testing.T) {
		valErr := &ValidationError{
			Field:   "",
			Value:   nil,
			Message: "Invalid request format",
		}

		result := classifyTypedErrors(valErr)
		require.NotNil(t, result)
		assert.Equal(t, ErrorTypeValidation, result.Type)
		assert.False(t, result.Retryable)
		assert.Equal(t, "", result.Details["field"])
		assert.Nil(t, result.Details["value"])
	})

	t.Run("pricing_error_no_region", func(t *testing.T) {
		pricingErr := &PricingError{
			Provider: "anthropic",
			Model:    "claude-3",
			Region:   "",
			Reason:   "Model not found",
			Type:     ErrorTypePricingUnavailable,
		}

		result := classifyTypedErrors(pricingErr)
		require.NotNil(t, result)
		assert.Equal(t, ErrorTypePricingUnavailable, result.Type)
		assert.Equal(t, "", result.Details["region"])
	})
}

func TestClassifySentinelErrors(t *testing.T) {
	t.Run("rate_limit_exceeded_sentinel", func(t *testing.T) {
		err := fmt.Errorf("operation failed: %w", ErrRateLimitExceeded)
		result := classifySentinelErrors(err)
		require.NotNil(t, result)
		assert.Equal(t, ErrorTypeRateLimit, result.Type)
		assert.Equal(t, "RATE_LIMIT", result.Code)
		assert.True(t, result.Retryable)
		assert.Equal(t, err, result.Cause)
	})

	t.Run("circuit_breaker_open_sentinel", func(t *testing.T) {
		err := fmt.Errorf("request blocked: %w", ErrCircuitBreakerOpen)
		result := classifySentinelErrors(err)
		require.NotNil(t, result)
		assert.Equal(t, ErrorTypeCircuitBreaker, result.Type)
		assert.Equal(t, "CIRCUIT_BREAKER", result.Code)
		assert.True(t, result.Retryable)
		assert.Equal(t, err, result.Cause)
	})

	t.Run("provider_unavailable_sentinel", func(t *testing.T) {
		err := fmt.Errorf("service down: %w", ErrProviderUnavailable)
		result := classifySentinelErrors(err)
		require.NotNil(t, result)
		assert.Equal(t, ErrorTypeProvider, result.Type)
		assert.Equal(t, "PROVIDER_UNAVAILABLE", result.Code)
		assert.True(t, result.Retryable)
		assert.Equal(t, err, result.Cause)
	})

	t.Run("pricing_unavailable_sentinel", func(t *testing.T) {
		err := fmt.Errorf("cost calculation failed: %w", ErrPricingUnavailable)
		result := classifySentinelErrors(err)
		require.NotNil(t, result)
		assert.Equal(t, ErrorTypePricingUnavailable, result.Type)
		assert.Equal(t, "PRICING_UNAVAILABLE", result.Code)
		assert.False(t, result.Retryable)
		assert.Equal(t, err, result.Cause)
	})

	t.Run("max_retries_exceeded_sentinel", func(t *testing.T) {
		err := fmt.Errorf("giving up: %w", ErrMaxRetriesExceeded)
		result := classifySentinelErrors(err)
		require.NotNil(t, result)
		assert.Equal(t, ErrorTypeProvider, result.Type)
		assert.Equal(t, "MAX_RETRIES", result.Code)
		assert.False(t, result.Retryable)
		assert.Contains(t, result.Details["original_error"], "giving up")
		assert.Equal(t, err, result.Cause)
	})

	t.Run("non_sentinel_error", func(t *testing.T) {
		err := errors.New("random error")
		result := classifySentinelErrors(err)
		assert.Nil(t, result)
	})

	t.Run("wrapped_non_sentinel_error", func(t *testing.T) {
		baseErr := errors.New("base error")
		err := fmt.Errorf("wrapper: %w", baseErr)
		result := classifySentinelErrors(err)
		assert.Nil(t, result)
	})
}

func TestClassifyStringPatternErrors(t *testing.T) {
	t.Run("rate_limit_pattern", func(t *testing.T) {
		testCases := []string{
			"Rate limit exceeded",
			"RATE LIMIT HIT",
			"API rate limit reached",
			"Too many requests - rate limit",
		}

		for _, errMsg := range testCases {
			err := errors.New(errMsg)
			result := classifyStringPatternErrors(err)
			require.NotNil(t, result, "Failed for: %s", errMsg)
			assert.Equal(t, ErrorTypeRateLimit, result.Type)
			assert.Equal(t, "RATE_LIMIT", result.Code)
			assert.True(t, result.Retryable)
			assert.Equal(t, errMsg, result.Details["original_error"])
		}
	})

	t.Run("timeout_pattern", func(t *testing.T) {
		testCases := []string{
			"Request timeout",
			"Connection timeout occurred",
			"Context deadline exceeded",
			"Operation timeout",
		}

		for _, errMsg := range testCases {
			err := errors.New(errMsg)
			result := classifyStringPatternErrors(err)
			assert.NotNil(t, result, "Failed for: %s", errMsg)
			assert.Equal(t, ErrorTypeTimeout, result.Type, "Failed for: %s", errMsg)
			assert.Equal(t, "TIMEOUT", result.Code, "Failed for: %s", errMsg)
			assert.True(t, result.Retryable, "Failed for: %s", errMsg)
			assert.Equal(t, errMsg, result.Details["original_error"], "Failed for: %s", errMsg)
		}
	})

	t.Run("auth_pattern", func(t *testing.T) {
		testCases := []string{
			"Unauthorized access",
			"Authentication failed",
			"Invalid credentials - unauthorized",
			"API key authentication error",
		}

		for _, errMsg := range testCases {
			err := errors.New(errMsg)
			result := classifyStringPatternErrors(err)
			require.NotNil(t, result, "Failed for: %s", errMsg)
			assert.Equal(t, ErrorTypeAuth, result.Type)
			assert.Equal(t, "AUTH_FAILED", result.Code)
			assert.False(t, result.Retryable)
			assert.Equal(t, errMsg, result.Details["original_error"])
		}
	})

	t.Run("permission_pattern", func(t *testing.T) {
		testCases := []string{
			"Forbidden operation",
			"Permission denied",
			"Access forbidden - insufficient permissions",
			"User lacks required permission",
		}

		for _, errMsg := range testCases {
			err := errors.New(errMsg)
			result := classifyStringPatternErrors(err)
			require.NotNil(t, result, "Failed for: %s", errMsg)
			assert.Equal(t, ErrorTypePermission, result.Type)
			assert.Equal(t, "PERMISSION_DENIED", result.Code)
			assert.False(t, result.Retryable)
			assert.Equal(t, errMsg, result.Details["original_error"])
		}
	})

	t.Run("quota_pattern", func(t *testing.T) {
		testCases := []string{
			"Quota exceeded",
			"API quota limit reached",
			"Monthly quota exhausted",
			"Usage quota has been exceeded",
		}

		for _, errMsg := range testCases {
			err := errors.New(errMsg)
			result := classifyStringPatternErrors(err)
			require.NotNil(t, result, "Failed for: %s", errMsg)
			assert.Equal(t, ErrorTypeQuota, result.Type)
			assert.Equal(t, "QUOTA_EXCEEDED", result.Code)
			assert.False(t, result.Retryable)
			assert.Equal(t, errMsg, result.Details["original_error"])
		}
	})

	t.Run("network_pattern", func(t *testing.T) {
		testCases := []string{
			"Network error occurred",
			"Connection refused",
			"DNS resolution failed - network issue",
			"Network connectivity problem",
		}

		for _, errMsg := range testCases {
			err := errors.New(errMsg)
			result := classifyStringPatternErrors(err)
			require.NotNil(t, result, "Failed for: %s", errMsg)
			assert.Equal(t, ErrorTypeNetwork, result.Type)
			assert.Equal(t, "NETWORK_ERROR", result.Code)
			assert.True(t, result.Retryable)
			assert.Equal(t, errMsg, result.Details["original_error"])
		}
	})

	t.Run("unknown_error_pattern", func(t *testing.T) {
		testCases := []string{
			"Something went wrong",
			"Unexpected error occurred",
			"Internal server error",
			"Generic failure",
		}

		for _, errMsg := range testCases {
			err := errors.New(errMsg)
			result := classifyStringPatternErrors(err)
			require.NotNil(t, result, "Failed for: %s", errMsg)
			assert.Equal(t, ErrorTypeUnknown, result.Type)
			assert.Equal(t, "UNKNOWN", result.Code)
			assert.False(t, result.Retryable)
			assert.Equal(t, errMsg, result.Details["original_error"])
		}
	})

	t.Run("case_insensitive_matching", func(t *testing.T) {
		testCases := []struct {
			message      string
			expectedType ErrorType
		}{
			{"RATE LIMIT EXCEEDED", ErrorTypeRateLimit},
			{"Request TIMEOUT", ErrorTypeTimeout},
			{"UNAUTHORIZED Access", ErrorTypeAuth},
			{"FORBIDDEN Operation", ErrorTypePermission},
			{"QUOTA Exceeded", ErrorTypeQuota},
			{"NETWORK Error", ErrorTypeNetwork},
		}

		for _, tc := range testCases {
			err := errors.New(tc.message)
			result := classifyStringPatternErrors(err)
			require.NotNil(t, result, "Failed for: %s", tc.message)
			assert.Equal(t, tc.expectedType, result.Type, "Failed for: %s", tc.message)
		}
	})

	t.Run("multiple_pattern_matching_priority", func(t *testing.T) {
		// Test that the first matching pattern takes precedence
		err := errors.New("Rate limit exceeded due to network timeout")
		result := classifyStringPatternErrors(err)
		require.NotNil(t, result)
		assert.Equal(t, ErrorTypeRateLimit, result.Type) // Rate limit should match first
	})
}

func TestClassifyLLMErrorIntegration(t *testing.T) {
	t.Run("typed_error_takes_precedence", func(t *testing.T) {
		// Create an error that could match string patterns but is strongly typed
		providerErr := &ProviderError{
			Provider:   "openai",
			StatusCode: 429,
			Message:    "rate limit exceeded", // This would match string pattern
			Code:       "rate_limit",
			Type:       ErrorTypeRateLimit,
		}

		result := ClassifyLLMError(providerErr)
		require.NotNil(t, result)
		assert.Equal(t, ErrorTypeRateLimit, result.Type)
		assert.Equal(t, "rate_limit", result.Code) // Should use provider error code, not string pattern code
		assert.Equal(t, "openai", result.Details["provider"])
	})

	t.Run("sentinel_error_takes_precedence_over_string", func(t *testing.T) {
		// Wrap a sentinel error with a message that could match string patterns
		err := fmt.Errorf("rate limit encountered: %w", ErrRateLimitExceeded)

		result := ClassifyLLMError(err)
		require.NotNil(t, result)
		assert.Equal(t, ErrorTypeRateLimit, result.Type)
		assert.Equal(t, "RATE_LIMIT", result.Code) // Should use sentinel error code
	})

	t.Run("string_pattern_fallback", func(t *testing.T) {
		// Create an error that only matches string patterns
		err := errors.New("Unexpected timeout occurred")

		result := ClassifyLLMError(err)
		require.NotNil(t, result)
		assert.Equal(t, ErrorTypeTimeout, result.Type)
		assert.Equal(t, "TIMEOUT", result.Code)
		assert.Equal(t, "Unexpected timeout occurred", result.Details["original_error"])
	})

	t.Run("error_chain_classification", func(t *testing.T) {
		// Test error chain handling
		baseErr := &RateLimitError{
			Provider:   "anthropic",
			RetryAfter: 30,
		}
		wrappedErr := fmt.Errorf("API call failed: %w", baseErr)

		result := ClassifyLLMError(wrappedErr)
		require.NotNil(t, result)
		assert.Equal(t, ErrorTypeRateLimit, result.Type)
		assert.Equal(t, "anthropic", result.Details["provider"])
		assert.Equal(t, 30, result.Details["retry_after"])
	})
}

func TestClassificationEdgeCases(t *testing.T) {
	t.Run("empty_error_message", func(t *testing.T) {
		err := errors.New("")
		result := ClassifyLLMError(err)
		require.NotNil(t, result)
		assert.Equal(t, ErrorTypeUnknown, result.Type)
		assert.Equal(t, "", result.Details["original_error"])
	})

	t.Run("whitespace_only_error_message", func(t *testing.T) {
		err := errors.New("   \t\n   ")
		result := ClassifyLLMError(err)
		require.NotNil(t, result)
		assert.Equal(t, ErrorTypeUnknown, result.Type) // Should not match any pattern
	})

	t.Run("very_long_error_message", func(t *testing.T) {
		longMsg := "This is a very long error message that contains rate limit information but is buried in lots of other text that might make pattern matching more challenging to ensure our algorithm works properly"
		err := errors.New(longMsg)
		result := ClassifyLLMError(err)
		require.NotNil(t, result)
		assert.Equal(t, ErrorTypeRateLimit, result.Type) // Should still find the pattern
	})

	t.Run("special_characters_in_message", func(t *testing.T) {
		err := errors.New("API returned 429: rate limit exceeded! @#$%^&*()")
		result := ClassifyLLMError(err)
		require.NotNil(t, result)
		assert.Equal(t, ErrorTypeRateLimit, result.Type)
	})

	t.Run("unicode_characters", func(t *testing.T) {
		err := errors.New("Taux limite dépassé - rate limit exceeded 🚫")
		result := ClassifyLLMError(err)
		require.NotNil(t, result)
		assert.Equal(t, ErrorTypeRateLimit, result.Type)
	})
}
