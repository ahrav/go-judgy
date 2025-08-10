package providers

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
)

func TestClassifyErrorType(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		errorCode    string
		expectedType llmerrors.ErrorType
	}{
		// Test error code-based classification first
		{
			name:         "rate_limit_from_error_code",
			statusCode:   http.StatusOK, // Status code should be ignored when error code matches
			errorCode:    "rate_limit_exceeded",
			expectedType: llmerrors.ErrorTypeRateLimit,
		},
		{
			name:         "rate_limit_case_insensitive",
			statusCode:   http.StatusOK,
			errorCode:    "RATE_LIMIT_ERROR",
			expectedType: llmerrors.ErrorTypeRateLimit,
		},
		{
			name:         "limit_in_error_code",
			statusCode:   http.StatusOK,
			errorCode:    "requests_limit_exceeded",
			expectedType: llmerrors.ErrorTypeRateLimit,
		},
		{
			name:         "timeout_from_error_code",
			statusCode:   http.StatusOK,
			errorCode:    "request_timeout",
			expectedType: llmerrors.ErrorTypeTimeout,
		},
		{
			name:         "auth_from_error_code",
			statusCode:   http.StatusOK,
			errorCode:    "invalid_auth_token",
			expectedType: llmerrors.ErrorTypeAuth,
		},
		{
			name:         "unauthorized_from_error_code",
			statusCode:   http.StatusOK,
			errorCode:    "unauthorized_access",
			expectedType: llmerrors.ErrorTypeAuth,
		},
		{
			name:         "permission_from_error_code",
			statusCode:   http.StatusOK,
			errorCode:    "permission_denied",
			expectedType: llmerrors.ErrorTypePermission,
		},
		{
			name:         "forbidden_from_error_code",
			statusCode:   http.StatusOK,
			errorCode:    "access_forbidden",
			expectedType: llmerrors.ErrorTypePermission,
		},
		{
			name:         "quota_from_error_code",
			statusCode:   http.StatusOK,
			errorCode:    "quota_exceeded",
			expectedType: llmerrors.ErrorTypeQuota,
		},

		// Test status code-based classification (when error code doesn't match patterns)
		{
			name:         "rate_limit_from_status",
			statusCode:   http.StatusTooManyRequests,
			errorCode:    "generic_error",
			expectedType: llmerrors.ErrorTypeRateLimit,
		},
		{
			name:         "auth_from_status",
			statusCode:   http.StatusUnauthorized,
			errorCode:    "",
			expectedType: llmerrors.ErrorTypeAuth,
		},
		{
			name:         "permission_from_status",
			statusCode:   http.StatusForbidden,
			errorCode:    "",
			expectedType: llmerrors.ErrorTypePermission,
		},
		{
			name:         "timeout_from_request_timeout_status",
			statusCode:   http.StatusRequestTimeout,
			errorCode:    "",
			expectedType: llmerrors.ErrorTypeTimeout,
		},
		{
			name:         "timeout_from_gateway_timeout_status",
			statusCode:   http.StatusGatewayTimeout,
			errorCode:    "",
			expectedType: llmerrors.ErrorTypeTimeout,
		},
		{
			name:         "validation_from_bad_request",
			statusCode:   http.StatusBadRequest,
			errorCode:    "",
			expectedType: llmerrors.ErrorTypeValidation,
		},
		{
			name:         "provider_error_from_internal_server_error",
			statusCode:   http.StatusInternalServerError,
			errorCode:    "",
			expectedType: llmerrors.ErrorTypeProvider,
		},
		{
			name:         "provider_error_from_bad_gateway",
			statusCode:   http.StatusBadGateway,
			errorCode:    "",
			expectedType: llmerrors.ErrorTypeProvider,
		},
		{
			name:         "provider_error_from_service_unavailable",
			statusCode:   http.StatusServiceUnavailable,
			errorCode:    "",
			expectedType: llmerrors.ErrorTypeProvider,
		},

		// Test server error threshold (5xx errors)
		{
			name:         "server_error_502",
			statusCode:   502,
			errorCode:    "",
			expectedType: llmerrors.ErrorTypeProvider,
		},
		{
			name:         "server_error_503",
			statusCode:   503,
			errorCode:    "",
			expectedType: llmerrors.ErrorTypeProvider,
		},
		{
			name:         "server_error_custom_5xx",
			statusCode:   599,
			errorCode:    "",
			expectedType: llmerrors.ErrorTypeProvider,
		},

		// Test unknown errors (fallback cases)
		{
			name:         "unknown_client_error",
			statusCode:   http.StatusTeapot, // 418 - not handled specifically
			errorCode:    "",
			expectedType: llmerrors.ErrorTypeUnknown,
		},
		{
			name:         "unknown_success_status",
			statusCode:   http.StatusCreated, // 201 - success but not expected
			errorCode:    "unknown_error",
			expectedType: llmerrors.ErrorTypeUnknown,
		},
		{
			name:         "empty_inputs",
			statusCode:   0,
			errorCode:    "",
			expectedType: llmerrors.ErrorTypeUnknown,
		},

		// Test error code precedence over status code
		{
			name:         "error_code_overrides_status",
			statusCode:   http.StatusInternalServerError, // Would normally be provider error
			errorCode:    "rate_limit_exceeded",
			expectedType: llmerrors.ErrorTypeRateLimit, // Should be rate limit due to error code
		},

		// Test partial matches in error codes
		{
			name:         "rate_substring_match",
			statusCode:   http.StatusBadRequest,
			errorCode:    "user_rate_exceeded_daily",
			expectedType: llmerrors.ErrorTypeRateLimit,
		},
		{
			name:         "timeout_substring_match",
			statusCode:   http.StatusBadRequest,
			errorCode:    "connection_timeout_error",
			expectedType: llmerrors.ErrorTypeTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifyErrorType(tt.statusCode, tt.errorCode)
			assert.Equal(t, tt.expectedType, result,
				"Expected error type %v but got %v for statusCode=%d, errorCode=%s",
				tt.expectedType, result, tt.statusCode, tt.errorCode)
		})
	}
}

func TestServerErrorStatusThreshold(t *testing.T) {
	// Test that the threshold constant is set correctly
	assert.Equal(t, 500, ServerErrorStatusThreshold)

	// Test boundary cases around the threshold
	assert.Equal(t, llmerrors.ErrorTypeUnknown, classifyErrorType(499, ""))
	assert.Equal(t, llmerrors.ErrorTypeProvider, classifyErrorType(500, ""))
	assert.Equal(t, llmerrors.ErrorTypeProvider, classifyErrorType(501, ""))
}

func TestUnsupportedOperationError(t *testing.T) {
	// Test that the error constant exists and has the correct message
	assert.NotNil(t, ErrUnsupportedOperation)
	assert.Contains(t, ErrUnsupportedOperation.Error(), "unsupported operation")
}

func TestClassifyErrorType_CaseSensitivity(t *testing.T) {
	// Test various case combinations to ensure case-insensitive matching
	testCases := []string{
		"RATE_LIMIT",
		"rate_limit",
		"Rate_Limit",
		"rAtE_lImIt",
	}

	for _, errorCode := range testCases {
		t.Run("case_insensitive_"+errorCode, func(t *testing.T) {
			result := classifyErrorType(http.StatusOK, errorCode)
			assert.Equal(t, llmerrors.ErrorTypeRateLimit, result)
		})
	}
}

func TestClassifyErrorType_EdgeCases(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		errorCode    string
		expectedType llmerrors.ErrorType
	}{
		{
			name:         "negative_status_code",
			statusCode:   -1,
			errorCode:    "",
			expectedType: llmerrors.ErrorTypeUnknown,
		},
		{
			name:         "very_large_status_code",
			statusCode:   999,
			errorCode:    "",
			expectedType: llmerrors.ErrorTypeProvider, // >= 500
		},
		{
			name:         "whitespace_in_error_code",
			statusCode:   http.StatusBadRequest,
			errorCode:    " rate limit ",
			expectedType: llmerrors.ErrorTypeRateLimit, // Should still match
		},
		{
			name:         "multiple_keywords_in_error_code",
			statusCode:   http.StatusBadRequest,
			errorCode:    "rate_limit_timeout_auth_error", // First match should win
			expectedType: llmerrors.ErrorTypeRateLimit,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifyErrorType(tt.statusCode, tt.errorCode)
			assert.Equal(t, tt.expectedType, result)
		})
	}
}
