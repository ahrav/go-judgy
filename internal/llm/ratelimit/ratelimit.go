// Package ratelimit Package resilience provides dual-layer rate limiting for LLM request processing.
//
// This package implements a middleware that combines a local token-bucket algorithm
// with an optional Redis-based distributed rate limiter.
// This approach provides comprehensive request throttling across multiple service instances.
// The system gracefully degrades to local-only limiting when Redis is unavailable,
// ensuring high availability and service continuity.
//
// The middleware supports tenant-specific rate limiting with provider and model granularity.
// It integrates seamlessly into the LLM request pipeline and includes features like
// automatic background cleanup of stale limiters to prevent memory leaks and
// comprehensive metrics for production monitoring.
package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/time/rate"

	"github.com/ahrav/go-judgy/internal/llm/configuration"
	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/transport"
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

// Rate limiting configuration constants.
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
	MillisecondsPerSecond = 1000

	// DefaultRateLimit provides a fallback rate when a configuration is missing.
	// It is set to a conservative 10 requests per second.
	DefaultRateLimit = 10

	// CleanupInterval determines the frequency of stale limiter cleanup.
	// A 1-hour interval balances memory usage with cleanup overhead.
	CleanupInterval = 1 * time.Hour

	// LimiterTTL defines the time-to-live for unused local limiters.
	// It matches the CleanupInterval to ensure deterministic cleanup behavior.
	LimiterTTL = 1 * time.Hour

	// DefaultInitialInterval provides a fallback for retry calculations.
	DefaultInitialInterval = 1 * time.Second
)

// rateLimitMiddleware implements dual-layer rate limiting for LLM requests.
//
// This middleware combines a local token-bucket algorithm with an optional
// Redis-based distributed rate limiter for comprehensive request throttling.
// It operates in two phases: a fast local check followed by a global check.
//
// The middleware is designed for high availability, gracefully degrading to
// local-only operation when Redis is unavailable. A background process cleans
// up unused limiters to prevent memory leaks. All operations are thread-safe.
type rateLimitMiddleware struct {
	// localMu protects access to the localLimiters map.
	localMu sync.RWMutex
	// localLimiters stores the per-key rate limiters with TTL tracking.
	localLimiters map[string]*timedLimiter
	// localConfig holds the configuration for the token bucket algorithm.
	localConfig configuration.LocalRateLimitConfig
	// limiterMinTTL is the minimum time-to-live for local limiters before cleanup.
	// Pre-calculated during initialization to avoid redundant calculations.
	limiterMinTTL time.Duration

	// globalClient is the Redis client for distributed rate limiting.
	globalClient *redis.Client
	// globalConfig holds the configuration for the fixed-window algorithm.
	globalConfig configuration.GlobalRateLimitConfig

	// cleanupMu protects Start/Stop operations to prevent race conditions.
	cleanupMu sync.Mutex
	// cleanupTicker triggers periodic cleanup of stale local limiters.
	cleanupTicker *time.Ticker
	// cleanupStop signals the cleanup goroutine to terminate.
	cleanupStop chan struct{}
	// cleanupDone synchronizes the completion of the cleanup goroutine.
	cleanupDone sync.WaitGroup

	// logger provides structured logging for observability.
	logger *slog.Logger
}

// validateRateLimitConfig performs comprehensive validation of rate limiting configuration.
//
// This function orchestrates validation of both local and global rate limiting
// settings to prevent security vulnerabilities and ensure correct operation.
func validateRateLimitConfig(cfg *configuration.RateLimitConfig) error {
	if err := validateLocalRateLimitConfig(cfg.Local); err != nil {
		return err
	}

	if err := validateGlobalRateLimitConfig(&cfg.Global); err != nil {
		return err
	}

	return nil
}

// validateLocalRateLimitConfig validates the local rate limit configuration.
//
// This function ensures that local rate limiting parameters are non-negative
// and enforce the business rule that BurstSize must be 0 when TokensPerSecond is 0.
func validateLocalRateLimitConfig(cfg configuration.LocalRateLimitConfig) error {
	if !cfg.Enabled {
		return nil // Skip validation when local limiting is disabled
	}

	if cfg.TokensPerSecond < 0 {
		return fmt.Errorf("invalid local rate limit: TokensPerSecond cannot be negative (got %f)", cfg.TokensPerSecond)
	}
	if cfg.BurstSize < 0 {
		return fmt.Errorf("invalid local rate limit: BurstSize cannot be negative (got %d)", cfg.BurstSize)
	}
	if cfg.TokensPerSecond == 0 && cfg.BurstSize > 0 {
		return fmt.Errorf("invalid local rate limit: BurstSize must be 0 when TokensPerSecond is 0")
	}

	return nil
}

// validateGlobalRateLimitConfig validates the global rate limit configuration.
//
// This function ensures that global rate limiting parameters are non-negative.
func validateGlobalRateLimitConfig(cfg *configuration.GlobalRateLimitConfig) error {
	if !cfg.Enabled {
		return nil // Skip validation when global limiting is disabled
	}

	if cfg.RequestsPerSecond < 0 {
		return fmt.Errorf("invalid global rate limit: RequestsPerSecond cannot be negative (got %d)", cfg.RequestsPerSecond)
	}

	return nil
}

// NewRateLimitMiddlewareWithRedis creates a transport.Middleware for rate limiting.
//
// It implements a dual-layer system with a local token-bucket limiter and an
// optional Redis-based global limiter. The middleware is designed for high
// availability, automatically falling back to local-only mode if Redis is
// unreachable.
//
// The local limiter uses per-key token buckets with configurable rates and burst
// sizes. The global limiter uses a fixed-window algorithm in Redis.
//
// If the provided Redis client is nil and global limiting is enabled, a new
// client is created based on the configuration. The function returns an error
// if the configuration is invalid. The returned middleware is thread-safe and
// includes background cleanup to prevent memory leaks.
//
// IMPORTANT: This function automatically starts a background cleanup goroutine
// that runs every hour (CleanupInterval) to remove stale local limiters. The
// cleanup process is essential for preventing memory leaks in long-running
// services. The goroutine will run for the lifetime of the application.
func NewRateLimitMiddlewareWithRedis(
	cfg *configuration.RateLimitConfig,
	client *redis.Client,
) (transport.Middleware, error) {
	if err := validateRateLimitConfig(cfg); err != nil {
		return nil, err
	}

	// Pre-calculate the minimum TTL for local limiters based on refill time.
	// This avoids redundant calculations in the cleanup routine.
	// Protect against division by zero
	var limiterMinTTL time.Duration
	if cfg.Local.TokensPerSecond > 0 {
		refillTime := time.Duration(float64(cfg.Local.BurstSize)/cfg.Local.TokensPerSecond) * time.Second
		limiterMinTTL = refillTime * 10
	}
	if limiterMinTTL < time.Hour {
		limiterMinTTL = time.Hour
	}

	rlm := &rateLimitMiddleware{
		localLimiters: make(map[string]*timedLimiter),
		localConfig:   cfg.Local,
		limiterMinTTL: limiterMinTTL,
		globalConfig: configuration.GlobalRateLimitConfig{
			Enabled:           cfg.Global.Enabled,
			RequestsPerSecond: cfg.Global.RequestsPerSecond,
			RedisAddr:         cfg.Global.RedisAddr,
			RedisPassword:     cfg.Global.RedisPassword,
			RedisDB:           cfg.Global.RedisDB,
			ConnectTimeout:    cfg.Global.ConnectTimeout,
			DegradedMode:      atomic.Bool{}, // Initialize with new atomic.Bool
		},
		logger: slog.Default().With("component", "ratelimit"),
	}

	if cfg.Global.Enabled {
		if client == nil {
			client = redis.NewClient(&redis.Options{
				Addr:         cfg.Global.RedisAddr,
				Password:     cfg.Global.RedisPassword,
				DB:           cfg.Global.RedisDB,
				DialTimeout:  cfg.Global.ConnectTimeout,
				ReadTimeout:  RedisReadTimeoutSeconds * time.Second,
				WriteTimeout: RedisWriteTimeoutSeconds * time.Second,
				PoolSize:     RedisPoolSize,
			})

			ctx, cancel := context.WithTimeout(context.Background(), cfg.Global.ConnectTimeout)
			defer cancel()

			if err := client.Ping(ctx).Err(); err != nil {
				rlm.logger.Warn("Redis connection failed, using local-only rate limiting", "error", err)
				rlm.globalConfig.DegradedMode.Store(true) // Enable graceful degradation
			}
		}
		rlm.globalClient = client
	}

	// Start the background cleanup process to prevent memory leaks.
	rlm.Start()

	return rlm.middleware(), nil
}

// middleware returns the configured rate limiting middleware function.
//
// The returned middleware performs dual-layer rate limiting by first checking
// the local token-bucket limit and then, if applicable, the global Redis limit.
// It builds a hierarchical key from the request metadata to apply the correct
// limits. If a Redis error occurs, it enters a degraded mode and relies solely
// on the local limiter. Rate limit errors include retry-after timing to guide
// client backoff behavior.
func (r *rateLimitMiddleware) middleware() transport.Middleware {
	return func(next transport.Handler) transport.Handler {
		return transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
			key := r.buildKey(req) // Generate tenant:provider:model:operation key

			// Phase 1: Local token bucket limiting (fast path).
			if r.localConfig.Enabled {
				if err := r.checkLocalLimit(key); err != nil {
					return nil, err // Return immediately on local rate limit
				}
			}

			// Phase 2: Global Redis-based limiting with graceful degradation.
			if r.globalConfig.Enabled && !r.globalConfig.DegradedMode.Load() {
				if err := r.checkGlobalLimit(ctx, key); err != nil {
					// Handle Redis connectivity issues by switching to degraded mode.
					if r.isRedisError(err) {
						r.logger.Warn("Redis error, switching to degraded mode", "error", err)
						r.globalConfig.DegradedMode.Store(true)
						// If local limiting is disabled, enforce fallback rate limiting
						// to prevent fail-open vulnerability when Redis is unavailable.
						if !r.localConfig.Enabled {
							if err := r.checkFallbackLimit(key); err != nil {
								return nil, err
							}
						}
					} else {
						return nil, err
					}
				}
			}

			// Phase 3: Fallback limiting when in degraded mode with local disabled.
			// This ensures we never fail open when both Redis and local limiting are unavailable.
			if r.globalConfig.Enabled && r.globalConfig.DegradedMode.Load() && !r.localConfig.Enabled {
				if err := r.checkFallbackLimit(key); err != nil {
					return nil, err
				}
			}

			// All rate limits passed, execute downstream handler.
			return next.Handle(ctx, req)
		})
	}
}

// buildKey constructs a hierarchical rate limiting key from request metadata.
//
// The key format "tenant:provider:model:operation" enables granular rate
// limiting strategies, such as per-tenant quotas or provider-specific limits.
func (r *rateLimitMiddleware) buildKey(req *transport.Request) string {
	return fmt.Sprintf("%s:%s:%s:%s", req.TenantID, req.Provider, req.Model, req.Operation)
}

// checkLocalLimit enforces the local token-bucket rate limit.
//
// This method is the fast-path check, using per-key token buckets to allow
// controlled bursts while maintaining an average rate. If the limit is exceeded,
// it calculates an optimal retry delay without consuming a token, preventing
// bucket capacity leaks.
func (r *rateLimitMiddleware) checkLocalLimit(key string) error {
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

// getOrCreateLimiter retrieves an existing token-bucket limiter or creates a new one.
//
// This method provides thread-safe limiter management using a double-checked
// locking pattern to minimize contention. It uses a read lock for the fast path
// and a write lock for creation. Timestamps are updated atomically to enable
// lock-free TTL tracking for cleanup.
func (r *rateLimitMiddleware) getOrCreateLimiter(key string) *rate.Limiter {
	now := time.Now().UnixNano()

	r.localMu.RLock()
	if tl, ok := r.localLimiters[key]; ok {
		// Touch while holding RLock so CleanupStale (writer) can’t delete before we update.
		tl.lastUsed.Store(now)
		lim := tl.limiter
		r.localMu.RUnlock()
		return lim
	}
	r.localMu.RUnlock()

	r.localMu.Lock()
	if tl, ok := r.localLimiters[key]; ok {
		tl.lastUsed.Store(now)
		lim := tl.limiter
		r.localMu.Unlock()
		return lim
	}

	lim := rate.NewLimiter(rate.Limit(r.localConfig.TokensPerSecond), r.localConfig.BurstSize)
	tl := &timedLimiter{limiter: lim}
	tl.lastUsed.Store(now)
	r.localLimiters[key] = tl
	r.localMu.Unlock()
	return lim
}

// checkGlobalLimit enforces the distributed rate limit using a Redis fixed window.
//
// This method uses a 1-second fixed-window counting algorithm implemented in a
// Lua script for atomicity and performance. The script handles counter
// initialization, incrementing, and TTL management in a single Redis command,
// reducing network round-trips and preventing race conditions. If the limit is
// exceeded, it returns an error with retry timing based on the window's TTL.
func (r *rateLimitMiddleware) checkGlobalLimit(ctx context.Context, key string) error {
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
		return fmt.Errorf("invalid global rate limit: RequestsPerSecond cannot be negative (got %d)", limit)
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
		if retryAfterSecs < 1 {
			retryAfterSecs = 1 // Minimum 1-second retry to prevent tight loops
		}
		if retryAfterSecs > 3600 {
			retryAfterSecs = 3600 // Maximum 1-hour retry for safety
		}

		return &llmerrors.RateLimitError{
			Provider:   "global",
			Limit:      int(limit),
			RetryAfter: retryAfterSecs,
		}
	}

	return nil // Request allowed, continue processing
}

// checkFallbackLimit enforces a default rate limit when both Redis and local limiting fail.
//
// This method provides a security fallback to prevent fail-open behavior when
// Redis is unavailable and local limiting is disabled. It uses the DefaultRateLimit
// constant to create a temporary local limiter that prevents unlimited throughput
// while maintaining service availability during degraded operation.
//
// The fallback limiter uses the same token-bucket algorithm as normal local
// limiting but with conservative defaults designed for emergency operation.
func (r *rateLimitMiddleware) checkFallbackLimit(key string) error {
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

// isRedisError determines if an error indicates a Redis connectivity issue.
//
// This helper function distinguishes between Redis infrastructure problems (like
// network failures or timeouts) and other application errors. It checks for
// Redis-specific errors, context cancellations, and network I/O errors to
// decide whether to enter degraded mode.
func (r *rateLimitMiddleware) isRedisError(err error) bool {
	if err == nil {
		return false
	}

	var redisErr redis.Error
	if errors.As(err, &redisErr) {
		return true
	}

	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	return false // Not a Redis infrastructure error
}

// CleanupStale removes unused local rate limiters to prevent memory leaks.
//
// This method iterates through all local limiters and removes those that have
// not been accessed since the provided `before` timestamp. It acquires a write
// lock to ensure thread-safe modification of the limiter map. This function is
// called periodically by the background cleanup goroutine.
func (r *rateLimitMiddleware) CleanupStale(before time.Time) {
	r.localMu.Lock()
	defer r.localMu.Unlock()

	cutoff := before.Add(-r.limiterMinTTL).UnixNano()

	for key, tl := range r.localLimiters {
		if tl.lastUsed.Load() < cutoff {
			delete(r.localLimiters, key)
		}
	}
}

// Stats provides comprehensive metrics for rate limiting performance.
//
// These statistics enable production monitoring and capacity planning by exposing
// the state of both the local limiter and the Redis connection pool. The metrics
// can be used to alert on degraded mode, connection pool exhaustion, or high
// memory usage from an accumulation of local limiters.
type Stats struct {
	// LocalLimiters is the number of active per-key token-bucket limiters.
	LocalLimiters int
	// GlobalEnabled indicates whether global Redis-based rate limiting is configured.
	GlobalEnabled bool
	// DegradedMode indicates whether the system has fallen back to local-only limiting.
	DegradedMode bool

	// PoolHits is the number of connections reused from the Redis pool.
	PoolHits uint32
	// PoolMisses is the number of new connections created by the Redis pool.
	PoolMisses uint32
	// PoolTimeouts is the number of connection acquisition timeouts.
	PoolTimeouts uint32
	// PoolTotalConns is the total number of connections managed by the Redis pool.
	PoolTotalConns uint32
	// PoolIdleConns is the number of idle connections available for reuse.
	PoolIdleConns uint32
	// PoolStaleConns is the number of connections marked as stale and pending cleanup.
	PoolStaleConns uint32
}

// GetStats returns a snapshot of the rate limiting performance statistics.
//
// This method provides real-time metrics for production monitoring, including
// the number of local limiters, the system's operational mode, and the health
// of the Redis connection pool. These stats are essential for observing memory
// usage, service availability, and connection efficiency.
func (r *rateLimitMiddleware) GetStats() (*Stats, error) {
	r.localMu.RLock()
	localCount := len(r.localLimiters)
	r.localMu.RUnlock()

	stats := &Stats{
		LocalLimiters: localCount,
		GlobalEnabled: r.globalConfig.Enabled,
		DegradedMode:  r.globalConfig.DegradedMode.Load(), // Atomic read
	}

	// Include Redis pool statistics when global client is available.
	if r.globalClient != nil {
		poolStats := r.globalClient.PoolStats()
		stats.PoolHits = poolStats.Hits
		stats.PoolMisses = poolStats.Misses
		stats.PoolTimeouts = poolStats.Timeouts
		stats.PoolTotalConns = poolStats.TotalConns
		stats.PoolIdleConns = poolStats.IdleConns
		stats.PoolStaleConns = poolStats.StaleConns
	}

	return stats, nil
}

// Start initiates the background cleanup process for stale local limiters.
//
// This method spawns a goroutine that periodically removes limiters that have
// not been used within the configured TTL. It is essential for preventing
// memory leaks in long-running services with dynamic rate-limiting keys.
// The method is idempotent and thread-safe.
func (r *rateLimitMiddleware) Start() {
	r.cleanupMu.Lock()
	defer r.cleanupMu.Unlock()

	if r.cleanupTicker != nil {
		return // Idempotent: already started, no-op
	}

	r.cleanupStop = make(chan struct{})
	r.cleanupTicker = time.NewTicker(CleanupInterval)

	r.cleanupDone.Add(1) // Track cleanup goroutine for graceful shutdown
	go r.cleanupLoop()   // Start background cleanup process

	r.logger.Info("rate limit cleanup started", "interval", CleanupInterval)
}

// Stop gracefully terminates the background cleanup goroutine.
//
// This method signals the cleanup goroutine to stop and waits for it to
// complete, ensuring a clean shutdown. It should be called when the
// application is shutting down. The method is idempotent and thread-safe.
func (r *rateLimitMiddleware) Stop() {
	r.cleanupMu.Lock()
	defer r.cleanupMu.Unlock()

	if r.cleanupTicker == nil {
		return // Idempotent: not started or already stopped, no-op
	}

	close(r.cleanupStop)   // Signal cleanup goroutine to terminate
	r.cleanupTicker.Stop() // Stop the ticker to prevent further ticks
	r.cleanupDone.Wait()   // Wait for cleanup goroutine to finish

	r.cleanupTicker = nil // Reset state to allow restart if needed
	r.logger.Info("rate limit cleanup stopped")
}

// cleanupLoop implements the background cleanup process for stale limiters.
//
// This goroutine runs until signaled to stop. It waits for ticks from the
// cleanup timer to trigger a cleanup of limiters that have not been accessed
// within the configured TTL.
func (r *rateLimitMiddleware) cleanupLoop() {
	defer r.cleanupDone.Done() // Signal completion for graceful shutdown

	for {
		select {
		case <-r.cleanupTicker.C:
			// Perform periodic cleanup of stale limiters.
			cutoff := time.Now().Add(-LimiterTTL) // Calculate stale timestamp
			r.CleanupStale(cutoff)
		case <-r.cleanupStop:
			return
		}
	}
}
