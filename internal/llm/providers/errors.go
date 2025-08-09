package providers

import (
	"errors"
	"net/http"
	"strings"

	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
)

// Provider adapter errors.
var (
	ErrUnsupportedOperation = errors.New("unsupported operation")
)

// ServerErrorStatusThreshold defines the HTTP status code threshold for server errors.
const ServerErrorStatusThreshold = 500

// classifyErrorType determines ErrorType from HTTP status and provider error codes.
// It examines both provider-specific error codes and HTTP status codes to
// classify errors into retryable and non-retryable categories.
func classifyErrorType(statusCode int, errorCode string) llmerrors.ErrorType {
	// Check error code first for specific classifications.
	lowerCode := strings.ToLower(errorCode)
	if strings.Contains(lowerCode, "rate") || strings.Contains(lowerCode, "limit") {
		return llmerrors.ErrorTypeRateLimit
	}
	if strings.Contains(lowerCode, "timeout") {
		return llmerrors.ErrorTypeTimeout
	}
	if strings.Contains(lowerCode, "auth") || strings.Contains(lowerCode, "unauthorized") {
		return llmerrors.ErrorTypeAuth
	}
	if strings.Contains(lowerCode, "permission") || strings.Contains(lowerCode, "forbidden") {
		return llmerrors.ErrorTypePermission
	}
	if strings.Contains(lowerCode, "quota") {
		return llmerrors.ErrorTypeQuota
	}

	// Fall back to status code classification.
	switch statusCode {
	case http.StatusTooManyRequests:
		return llmerrors.ErrorTypeRateLimit
	case http.StatusUnauthorized:
		return llmerrors.ErrorTypeAuth
	case http.StatusForbidden:
		return llmerrors.ErrorTypePermission
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return llmerrors.ErrorTypeTimeout
	case http.StatusBadRequest:
		return llmerrors.ErrorTypeValidation
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable:
		return llmerrors.ErrorTypeProvider
	default:
		if statusCode >= ServerErrorStatusThreshold {
			return llmerrors.ErrorTypeProvider
		}
		return llmerrors.ErrorTypeUnknown
	}
}
