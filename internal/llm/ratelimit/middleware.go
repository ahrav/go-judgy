// Package ratelimit provides dual-layer rate limiting for LLM request processing.
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
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/time/rate"

	"github.com/ahrav/go-judgy/internal/llm/configuration"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// Cleanup and lifecycle constants.
const (
	// CleanupInterval determines the frequency of stale limiter cleanup.
	// A 1-hour interval balances memory usage with cleanup overhead.
	CleanupInterval = 1 * time.Hour

	// LimiterTTL defines the time-to-live for unused local limiters.
	// It matches the CleanupInterval to ensure deterministic cleanup behavior.
	LimiterTTL = 1 * time.Hour

	// LimiterTTLMultiplier is used to calculate the minimum TTL for rate limiters
	// based on the refill time. This ensures limiters stay active long enough
	// to be effective but are cleaned up appropriately.
	LimiterTTLMultiplier = 10
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
		limiterMinTTL = refillTime * LimiterTTLMultiplier
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
				if err := checkLocalLimit(r, key); err != nil {
					return nil, err // Return immediately on local rate limit
				}
			}

			// Phase 2: Global Redis-based limiting with graceful degradation.
			if r.globalConfig.Enabled && !r.globalConfig.DegradedMode.Load() {
				if err := r.handleGlobalLimit(ctx, key); err != nil {
					return nil, err
				}
			}

			// Phase 3: Fallback limiting when in degraded mode with local disabled.
			// This ensures we never fail open when both Redis and local limiting are unavailable.
			if r.globalConfig.Enabled && r.globalConfig.DegradedMode.Load() && !r.localConfig.Enabled {
				if err := checkFallbackLimit(r, key); err != nil {
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

// handleGlobalLimit handles global rate limiting with Redis error fallback.
// This method reduces complexity in the main middleware flow by extracting
// the nested error handling logic.
func (r *rateLimitMiddleware) handleGlobalLimit(ctx context.Context, key string) error {
	err := checkGlobalLimit(r, ctx, key)
	if err == nil {
		return nil
	}

	// Handle Redis connectivity issues by switching to degraded mode.
	if !r.isRedisError(err) {
		return err
	}

	r.logger.Warn("Redis error, switching to degraded mode", "error", err)
	r.globalConfig.DegradedMode.Store(true)

	// If local limiting is disabled, enforce fallback rate limiting
	// to prevent fail-open vulnerability when Redis is unavailable.
	if !r.localConfig.Enabled {
		return checkFallbackLimit(r, key)
	}

	return nil
}

// getOrCreateLimiter retrieves an existing token-bucket limiter or creates a new one.
//
// This method provides thread-safe limiter management using a double-checked
// locking pattern to minimize contention. It uses a read lock for the fast path
// and a write lock for creation. Timestamps are updated atomically to enable
// lock-free TTL tracking for cleanup. Exhausted limiters preserve their state
// to prevent rate limit bypass vulnerabilities.
func (r *rateLimitMiddleware) getOrCreateLimiter(key string) *rate.Limiter {
	now := time.Now().UnixNano()

	r.localMu.RLock()
	if tl, ok := r.localLimiters[key]; ok {
		// Touch while holding RLock so CleanupStale (writer) can't delete before we update.
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

	// Create new limiter with full burst capacity for truly fresh keys
	lim := rate.NewLimiter(rate.Limit(r.localConfig.TokensPerSecond), r.localConfig.BurstSize)
	tl := &timedLimiter{limiter: lim}
	tl.lastUsed.Store(now)
	// New limiters start as not exhausted (normal operation)
	tl.exhausted.Store(false)
	r.localLimiters[key] = tl
	r.localMu.Unlock()
	return lim
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
// This method marks stale limiters as exhausted to preserve rate limit state,
// preventing rate limit bypass vulnerabilities. Limiters that have not been accessed
// since the provided `before` timestamp are reset to empty state and marked as
// exhausted. Only limiters that have been exhausted for an extended period are
// eventually deleted to balance memory management with security.
func (r *rateLimitMiddleware) CleanupStale(before time.Time) {
	r.localMu.Lock()
	defer r.localMu.Unlock()

	cutoff := before.Add(-r.limiterMinTTL).UnixNano()

	for key, tl := range r.localLimiters {
		if tl.lastUsed.Load() < cutoff {
			// Check if limiter has full capacity (never used for rate limiting).
			reservation := tl.limiter.Reserve()
			hasFullCapacity := reservation.OK() && reservation.Delay() == 0
			reservation.Cancel()

			if hasFullCapacity && !tl.exhausted.Load() {
				// Unused limiter with full capacity and never exhausted - safe to delete.
				delete(r.localLimiters, key)
			} else {
				// Limiter has been used, is rate-limited, or was previously exhausted.
				// Mark as exhausted and reset to empty state to prevent rate limit bypass.
				tl.exhausted.Store(true)
				tl.limiter = rate.NewLimiter(rate.Limit(r.localConfig.TokensPerSecond), 0)
			}
		}
	}
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
