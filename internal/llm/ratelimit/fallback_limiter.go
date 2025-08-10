package ratelimit

import (
	"fmt"
	"math"
	"time"

	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
	"golang.org/x/time/rate"
)

// DefaultRateLimit provides a fallback rate when a configuration is missing.
// It is set to a conservative 10 requests per second.
const DefaultRateLimit = 10

// checkFallbackLimit enforces a default rate limit when both Redis and local limiting fail.
//
// This method provides a security fallback to prevent fail-open behavior when
// Redis is unavailable and local limiting is disabled. It uses the DefaultRateLimit
// constant to create a temporary local limiter that prevents unlimited throughput
// while maintaining service availability during degraded operation.
//
// The fallback limiter uses the same token-bucket algorithm as normal local
// limiting but with conservative defaults designed for emergency operation.
func checkFallbackLimit(r *rateLimitMiddleware, key string) error {
	fallbackKey := fmt.Sprintf("fallback:%s", key)

	r.localMu.RLock()
	if tl, ok := r.localLimiters[fallbackKey]; ok {
		tl.lastUsed.Store(time.Now().UnixNano())
		lim := tl.limiter
		r.localMu.RUnlock()

		if !lim.Allow() {
			reservation := lim.Reserve()
			delay := reservation.Delay()
			reservation.Cancel()

			retryAfter := int(math.Ceil(delay.Seconds()))
			if retryAfter < 1 {
				retryAfter = 1
			}

			return &llmerrors.RateLimitError{
				Provider:   "fallback",
				Limit:      DefaultRateLimit,
				RetryAfter: retryAfter,
			}
		}
		return nil
	}
	r.localMu.RUnlock()

	// Create fallback limiter with DefaultRateLimit and minimal burst.
	r.localMu.Lock()
	if tl, ok := r.localLimiters[fallbackKey]; ok {
		tl.lastUsed.Store(time.Now().UnixNano())
		lim := tl.limiter
		r.localMu.Unlock()

		if !lim.Allow() {
			reservation := lim.Reserve()
			delay := reservation.Delay()
			reservation.Cancel()

			retryAfter := int(math.Ceil(delay.Seconds()))
			if retryAfter < 1 {
				retryAfter = 1
			}

			return &llmerrors.RateLimitError{
				Provider:   "fallback",
				Limit:      DefaultRateLimit,
				RetryAfter: retryAfter,
			}
		}
		r.localMu.Unlock()
		return nil
	}

	// Create new fallback limiter with conservative settings.
	fallbackLimiter := rate.NewLimiter(rate.Limit(DefaultRateLimit), DefaultRateLimit)
	tl := &timedLimiter{limiter: fallbackLimiter}
	tl.lastUsed.Store(time.Now().UnixNano())
	r.localLimiters[fallbackKey] = tl
	r.localMu.Unlock()

	// Check the newly created fallback limiter
	if !fallbackLimiter.Allow() {
		reservation := fallbackLimiter.Reserve()
		delay := reservation.Delay()
		reservation.Cancel()

		retryAfter := int(math.Ceil(delay.Seconds()))
		if retryAfter < 1 {
			retryAfter = 1
		}

		return &llmerrors.RateLimitError{
			Provider:   "fallback",
			Limit:      DefaultRateLimit,
			RetryAfter: retryAfter,
		}
	}

	return nil
}
