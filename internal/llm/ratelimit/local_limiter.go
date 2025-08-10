package ratelimit

import (
	"math"
	"sync/atomic"

	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
	"golang.org/x/time/rate"
)

// timedLimiter wraps a rate limiter with an atomic timestamp.
// This design enables TTL-based cleanup of stale limiters without requiring locks,
// preventing memory leaks in long-running services.
type timedLimiter struct {
	limiter *rate.Limiter
	// lastUsed stores the last access time as a Unix nanosecond timestamp.
	// It is updated atomically to ensure thread-safe, lock-free reads.
	lastUsed atomic.Int64
}

// checkLocalLimit enforces the local token-bucket rate limit.
//
// This method is the fast-path check, using per-key token buckets to allow
// controlled bursts while maintaining an average rate. If the limit is exceeded,
// it calculates an optimal retry delay without consuming a token, preventing
// bucket capacity leaks.
func checkLocalLimit(r *rateLimitMiddleware, key string) error {
	limiter := r.getOrCreateLimiter(key)

	if !limiter.Allow() {
		// Calculate retry delay without consuming tokens to avoid capacity leaks.
		reservation := limiter.Reserve()
		delay := reservation.Delay()
		reservation.Cancel() // Critical: prevent token consumption for failed requests

		// Apply minimum 1-second retry to prevent tight client retry loops.
		retryAfter := int(math.Ceil(delay.Seconds()))
		if retryAfter < 1 {
			retryAfter = 1
		}

		return &llmerrors.RateLimitError{
			Provider:   "local",
			Limit:      int(r.localConfig.TokensPerSecond),
			RetryAfter: retryAfter,
		}
	}

	return nil // Token bucket has capacity, allow request
}
