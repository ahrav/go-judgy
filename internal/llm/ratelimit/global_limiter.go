package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
)

// Redis configuration constants.
const (
	// RedisReadTimeoutSeconds defines the read timeout for Redis operations.
	// A 5-second timeout balances responsiveness with network latency tolerance.
	RedisReadTimeoutSeconds = 5

	// RedisWriteTimeoutSeconds defines the write timeout for Redis operations.
	// It matches the read timeout for consistent behavior.
	RedisWriteTimeoutSeconds = 5

	// RedisPoolSize sets the maximum number of connections in the Redis pool.
	// This value is sized for moderate concurrent load across service instances.
	RedisPoolSize = 10

	// MillisecondsPerSecond defines the number of milliseconds in a second.
	MillisecondsPerSecond       = 1000
	MinRetryAfterSeconds        = 1
	MaxRetryAfterSecondsPerHour = 3600 // Maximum 1-hour retry for safety

	// DefaultInitialInterval provides a fallback for retry calculations.
	DefaultInitialInterval = 1 * time.Second
)

// checkGlobalLimit enforces the distributed rate limit using a Redis fixed window.
//
// This method uses a 1-second fixed-window counting algorithm implemented in a
// Lua script for atomicity and performance. The script handles counter
// initialization, incrementing, and TTL management in a single Redis command,
// reducing network round-trips and preventing race conditions. If the limit is
// exceeded, it returns an error with retry timing based on the window's TTL.
func checkGlobalLimit(r *rateLimitMiddleware, ctx context.Context, key string) error {
	if r.globalClient == nil {
		return nil // No global client configured, skip global limiting
	}

	// Atomic Lua script for distributed rate limiting with fixed 1-second windows.
	// Handles all race conditions and TTL edge cases in a single Redis operation.
	script := redis.NewScript(`
		local key = KEYS[1]
		local window = tonumber(ARGV[1])    -- Window duration in milliseconds
		local limit = tonumber(ARGV[2])     -- Request limit per window

		-- Get current request count for this window
		local current = redis.call('GET', key)
		if current == false then
			-- First request in new window, initialize counter with TTL
			redis.call('SET', key, 1, 'PX', window)
			return {1, limit - 1}  -- Allow request, return remaining capacity
		end

		local count = tonumber(current)
		if count < limit then
			-- Within limit, increment counter and preserve TTL
			local newCount = redis.call('INCR', key)
			local ttl = redis.call('PTTL', key)
			if ttl == -1 then
				-- Key exists without TTL (edge case), restore window expiration
				redis.call('PEXPIRE', key, window)
			end
			return {1, limit - newCount}  -- Allow request, return remaining capacity
		else
			-- Rate limit exceeded, return retry timing
			local ttl = redis.call('PTTL', key)
			return {0, ttl}  -- Deny request, return milliseconds until window reset
		end
	`)

	globalKey := fmt.Sprintf("rl:global:%s", key)    // Prefix for Redis key namespacing
	windowMs := int64(MillisecondsPerSecond)         // 1-second window in milliseconds
	limit := int64(r.globalConfig.RequestsPerSecond) // Configured global rate limit

	// Defense-in-depth: Reject negative rate limits at runtime
	// This should never happen if configuration validation is working correctly,
	// but we check here to prevent security vulnerabilities
	if limit < 0 {
		r.logger.Error("negative global rate limit detected at runtime", "limit", limit)
		return fmt.Errorf("%w (got %d)", errNegativeRequestsPerSecond, limit)
	}

	// Skip global limiting when disabled (RequestsPerSecond == 0).
	if limit == 0 {
		return nil // Global rate limiting disabled, continue with local-only
	}

	// Execute atomic Lua script for distributed rate limiting.
	result, err := script.Run(ctx, r.globalClient, []string{globalKey},
		windowMs, limit).Result()
	if err != nil {
		return fmt.Errorf("global rate limit check failed: %w", err)
	}

	// Parse Redis response: [allowed, remaining_or_ttl].
	res, ok := result.([]any)
	if !ok || len(res) < 2 {
		r.logger.Warn("invalid Redis response format, switching to degraded mode", "response", result)
		r.globalConfig.DegradedMode.Store(true)
		return nil // Graceful degradation to local-only limiting
	}

	allowed, ok := res[0].(int64)
	if !ok {
		r.logger.Warn("invalid Redis allowed value format, switching to degraded mode", "allowed", res[0])
		r.globalConfig.DegradedMode.Store(true)
		return nil // Graceful degradation on parsing errors
	}

	if allowed == 0 {
		// Rate limit exceeded, extract retry timing from TTL.
		retryAfterMs, ok := res[1].(int64)
		if !ok || retryAfterMs <= 0 {
			// Fallback to default interval on invalid TTL.
			retryAfterMs = int64(DefaultInitialInterval / time.Millisecond)
		}

		retryAfterSecs := int(retryAfterMs / MillisecondsPerSecond)
		if retryAfterSecs < MinRetryAfterSeconds {
			retryAfterSecs = MinRetryAfterSeconds // Minimum 1-second retry to prevent tight loops
		}
		if retryAfterSecs > MaxRetryAfterSecondsPerHour {
			retryAfterSecs = MaxRetryAfterSecondsPerHour // Maximum 1-hour retry for safety
		}

		return &llmerrors.RateLimitError{
			Provider:   "global",
			Limit:      int(limit),
			RetryAfter: retryAfterSecs,
		}
	}

	return nil // Request allowed, continue processing
}
