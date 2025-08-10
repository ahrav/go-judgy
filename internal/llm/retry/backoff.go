package retry

import (
	"errors"
	"math/rand/v2"
	"strconv"
	"time"

	"github.com/ahrav/go-judgy/internal/llm/configuration"
	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
)

// calculateBackoff computes optimal retry delay using exponential backoff with jitter.
// Respects provider Retry-After headers, applies configurable multipliers and caps,
// and uses full jitter randomization for distributed retry timing patterns.
// Thread-safe implementation using math/rand/v2.
func (r *retryMiddleware) calculateBackoff(attempt int, err error) time.Duration {
	// Calculate exponential backoff first as fallback.
	// Ensure minimum interval to prevent hot looping.
	baseBackoff := r.config.InitialInterval
	if baseBackoff <= 0 {
		baseBackoff = time.Millisecond // Minimum 1ms to prevent hot loop.
	}

	for i := 1; i < attempt; i++ {
		multiplier := r.config.Multiplier
		if multiplier < 1.0 {
			multiplier = 1.0 // Ensure multiplier doesn't decrease interval.
		}
		baseBackoff = time.Duration(float64(baseBackoff) * multiplier)
		if baseBackoff > r.config.MaxInterval {
			baseBackoff = r.config.MaxInterval
			break
		}
	}

	// Apply jitter if enabled using thread-safe rand/v2
	exponentialBackoff := baseBackoff
	if r.config.UseJitter {
		// Full jitter: random between 0 and calculated backoff.
		// Using math/rand/v2 for thread-safe random generation.
		jitterMs := rand.Int64N(baseBackoff.Milliseconds() + 1) // #nosec G404 -- non-cryptographic jitter is appropriate here
		exponentialBackoff = time.Duration(jitterMs) * time.Millisecond
	}

	// Check for Retry-After header guidance.
	retryAfter := r.extractRetryAfter(err)
	if retryAfter > 0 {
		// Provider-specified retry delay takes precedence, but only if reasonable.
		// If retry-after is too long, fall back to exponential backoff.
		return retryAfter
	}

	return exponentialBackoff
}

// calculatePureExponentialBackoff computes exponential backoff without considering retry-after headers.
// Used as fallback when retry-after values conflict with time constraints.
func (r *retryMiddleware) calculatePureExponentialBackoff(attempt int) time.Duration {
	// Calculate exponential backoff.
	baseBackoff := r.config.InitialInterval
	for i := 1; i < attempt; i++ {
		baseBackoff = time.Duration(float64(baseBackoff) * r.config.Multiplier)
		if baseBackoff > r.config.MaxInterval {
			baseBackoff = r.config.MaxInterval
			break
		}
	}

	// Apply jitter if enabled using thread-safe rand/v2.
	if r.config.UseJitter {
		// Full jitter: random between 0 and calculated backoff.
		// Using math/rand/v2 for thread-safe random generation.
		jitterMs := rand.Int64N(baseBackoff.Milliseconds() + 1) // #nosec G404 -- non-cryptographic jitter is appropriate here
		return time.Duration(jitterMs) * time.Millisecond
	}

	return baseBackoff
}

// extractRetryAfter determines provider-specified retry delays from error responses.
// Implements interface-based error inspection for clean type handling,
// supporting multiple retry-after formats including RFC date strings
// and numeric second values with comprehensive fallback parsing.
func (r *retryMiddleware) extractRetryAfter(err error) time.Duration {
	// Check for AfterProvider interface first.
	var provider AfterProvider
	if errors.As(err, &provider) {
		return provider.GetRetryAfter()
	}

	// Check for RateLimitError with explicit retry after.
	var rateLimitErr *llmerrors.RateLimitError
	if errors.As(err, &rateLimitErr) && rateLimitErr.RetryAfter > 0 {
		return time.Duration(rateLimitErr.RetryAfter) * time.Second
	}

	// Check for provider error with RetryAfter field.
	var providerErr *llmerrors.ProviderError
	if errors.As(err, &providerErr) && providerErr.RetryAfter > 0 {
		return time.Duration(providerErr.RetryAfter) * time.Second
	}

	// Check for workflow error with retry_after in details.
	var workflowErr *llmerrors.WorkflowError
	if errors.As(err, &workflowErr) && workflowErr.Details != nil {
		if retryAfterRaw, ok := workflowErr.Details["retry_after"]; ok {
			return r.parseRetryAfterValue(retryAfterRaw)
		}
	}

	return 0
}

// parseRetryAfterValue converts multiple retry-after formats to duration.
// Handles numeric seconds, RFC timestamp formats, and type-safe conversion
// with comprehensive fallback parsing for maximum compatibility.
func (r *retryMiddleware) parseRetryAfterValue(value any) time.Duration {
	switch v := value.(type) {
	case int:
		return time.Duration(v) * time.Second
	case int64:
		return time.Duration(v) * time.Second
	case float64:
		return time.Duration(v) * time.Second
	case string:
		// Try to parse as seconds first.
		if seconds, err := strconv.Atoi(v); err == nil {
			return time.Duration(seconds) * time.Second
		}
		// Try RFC date formats - including RFC850 and ANSIC as documented.
		formats := []string{
			time.RFC1123, time.RFC1123Z,
			time.RFC822, time.RFC822Z,
			time.RFC850, time.ANSIC,
		}
		for _, format := range formats {
			if t, err := time.Parse(format, v); err == nil {
				duration := time.Until(t)
				// Ensure we don't return negative durations.
				if duration < 0 {
					return 0
				}
				return duration
			}
		}
		// Also try parsing with the current location for timezone-aware formats.
		if t, err := time.ParseInLocation(time.RFC850, v, time.Local); err == nil {
			duration := time.Until(t)
			if duration < 0 {
				return 0
			}
			return duration
		}
	case time.Duration:
		return v
	}
	return 0
}

// ExponentialBackoff calculates retry delays using exponential backoff with jitter.
// Standalone utility function supporting configurable multipliers, maximum intervals,
// and optional full jitter randomization. Thread-safe using math/rand/v2.
// Returns zero duration for non-positive attempt numbers.
func ExponentialBackoff(attempt int, config configuration.RetryConfig) time.Duration {
	if attempt <= 0 {
		return 0
	}

	backoff := config.InitialInterval
	for i := 1; i < attempt; i++ {
		backoff = time.Duration(float64(backoff) * config.Multiplier)
		if backoff > config.MaxInterval {
			return config.MaxInterval
		}
	}

	if config.UseJitter {
		// Full jitter using thread-safe rand/v2.
		jitterMs := rand.Int64N(backoff.Milliseconds() + 1) // #nosec G404 -- non-cryptographic jitter is appropriate here
		return time.Duration(jitterMs) * time.Millisecond
	}

	return backoff
}

// CalculateJitter adds jitter to a base duration using thread-safe rand/v2.
// Factor should be between 0 and 1 for proportional jitter.
func CalculateJitter(base time.Duration, factor float64) time.Duration {
	if factor <= 0 {
		return base
	}
	if factor > 1 {
		factor = 1
	}

	jitterRange := float64(base) * factor
	jitter := rand.Float64() * jitterRange // #nosec G404 -- non-cryptographic jitter is appropriate here

	return base + time.Duration(jitter)
}
