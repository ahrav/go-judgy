package ratelimit

import (
	"math"
	"sync/atomic"

	"golang.org/x/time/rate"

	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
)

// timedLimiter wraps a rate limiter with an atomic timestamp and exhaustion state.
// This design enables TTL-based cleanup of stale limiters without requiring locks,
// preventing memory leaks in long-running services while preserving rate limit state.
type timedLimiter struct {
	limiter *rate.Limiter
	// lastUsed stores the last access time as a Unix nanosecond timestamp.
	// It is updated atomically to ensure thread-safe, lock-free reads.
	lastUsed atomic.Int64
	// exhausted indicates if this limiter was cleaned while in an exhausted state.
	// This prevents rate limit bypass by ensuring cleaned limiters don't grant fresh tokens.
	exhausted atomic.Bool
}

// checkLocalLimit enforces the local token-bucket rate limit.
//
// This method is the fast-path check, using per-key token buckets to allow
// controlled bursts while maintaining an average rate. If the limit is exceeded,
// it calculates an optimal retry delay without consuming a token, preventing
// bucket capacity leaks. It also tracks when limiters become exhausted for
// proper cleanup behavior.
func checkLocalLimit(r *rateLimitMiddleware, key string) error {
	limiter := r.getOrCreateLimiter(key)

	if !limiter.Allow() {
		// Mark the limiter as having been rate-limited (exhausted).
		r.localMu.RLock()
		if tl, ok := r.localLimiters[key]; ok {
			tl.exhausted.Store(true)
		}
		r.localMu.RUnlock()

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
