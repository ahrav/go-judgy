// Package cache provides Redis-based caching middleware for LLM responses.
// It implements atomic check-and-lease mechanisms to prevent duplicate work
// in distributed environments, with graceful degradation when Redis is
// unavailable and staleness protection through configurable TTL policies.
package cache

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ahrav/go-judgy/internal/llm/configuration"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

const (
	// Redis connection defaults.
	defaultPoolSize   = 10
	connectionTimeout = 5 * time.Second

	// Cache operation defaults.
	leaseTimeout       = 30 * time.Second
	retryCheckInterval = 100 * time.Millisecond
	cleanupTimeout     = 5 * time.Second
)

// cacheMiddleware implements Redis-based caching for LLM responses.
// All operations are thread-safe. Cache misses trigger atomic lease
// acquisition to prevent duplicate work. Redis failures result in graceful
// degradation with cache bypass.
type cacheMiddleware struct {
	client  *redis.Client
	ttl     time.Duration
	maxAge  time.Duration // Maximum age before entries are considered stale.
	enabled bool

	logger *slog.Logger

	// Metrics counters accessed atomically.
	hits   atomic.Int64
	misses atomic.Int64
	errors atomic.Int64
}

// NewCacheMiddlewareWithRedis creates a caching middleware for LLM responses.
// If client is nil and caching is enabled, creates a new Redis client using cfg.
// Redis connection failures disable caching for graceful degradation.
// Returns ready-to-use middleware suitable for production and testing.
func NewCacheMiddlewareWithRedis(ctx context.Context, cfg configuration.CacheConfig, client *redis.Client) (transport.Middleware, error) {
	if client == nil && cfg.Enabled {
		// Create Redis client with connection pooling.
		client = redis.NewClient(&redis.Options{
			Addr:     cfg.RedisAddr,
			Password: cfg.RedisPassword,
			DB:       cfg.RedisDB,
			PoolSize: defaultPoolSize,
		})

		// Test connection to detect Redis availability.
		timeoutCtx, cancel := context.WithTimeout(ctx, connectionTimeout)
		defer cancel()

		if err := client.Ping(timeoutCtx).Err(); err != nil {
			// Disable cache on connection failure for graceful degradation.
			slog.Warn("Redis connection failed, cache disabled", "error", err)
			cfg.Enabled = false
		}
	}

	cm := &cacheMiddleware{
		client:  client,
		ttl:     cfg.TTL,
		maxAge:  cfg.MaxAge,
		enabled: cfg.Enabled,
		logger:  slog.Default().With("component", "cache"),
	}

	return cm.middleware(), nil
}

// middleware returns the transport.Middleware function that intercepts requests.
// It implements the core caching logic, including the atomic check-and-lease
// mechanism to prevent redundant work in a distributed environment.
func (c *cacheMiddleware) middleware() transport.Middleware {
	return func(next transport.Handler) transport.Handler {
		return transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
			if !c.enabled || req.IdempotencyKey == "" {
				return next.Handle(ctx, req)
			}

			// Build cache key with field validation.
			key, keyErr := c.buildKey(req)
			if keyErr != nil {
				c.logger.Warn("cache key validation failed", "error", keyErr)
				return next.Handle(ctx, req)
			}

			// Atomic cache check and lease acquisition prevents duplicate work.
			leaseKey := key + ":lease"
			status, cached, acquired, err := c.atomicCheckAndLease(ctx, key, leaseKey, leaseTimeout)

			switch status {
			case cacheHit:
				c.hits.Add(1)
				c.logger.Debug("cache hit",
					"key", key,
					"provider", req.Provider,
					"model", req.Model,
					"operation", req.Operation)
				return cached, nil

			case leaseAcquired:
				c.misses.Add(1)

			case leaseFailed:
				c.misses.Add(1)
				// Another process holds the lease, wait and retry once.
				select {
				case <-time.After(retryCheckInterval):
					if retryResp, retryErr := c.get(ctx, key); retryErr == nil && retryResp != nil {
						c.hits.Add(1)
						c.logger.Debug("cache hit after lease wait", "key", key)
						return retryResp, nil
					}
					// Fallback to execution if retry fails.
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}

			if err != nil {
				c.errors.Add(1)
				c.logger.Warn("cache/lease operation error", "error", err, "key", key)
				// Graceful degradation: continue without cache protection.
			}

			// Cleanup lease to prevent deadlocks, even on request cancellation.
			defer func() { //nolint:contextcheck // Background context intentional for cleanup
				if acquired && c.client != nil {
					// Background context prevents cleanup cancellation.
					cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
					defer cancel()

					if delErr := c.client.Del(cleanupCtx, leaseKey).Err(); delErr != nil {
						c.logger.Warn("lease cleanup error", "error", delErr, "key", leaseKey)
					}
				}
			}()

			// Execute request through next handler in chain.
			resp, err := next.Handle(ctx, req)
			if err != nil {
				// Only cache successful responses, not errors.
				return nil, err
			}

			// Cache successful response for future requests.
			if resp != nil {
				if cacheErr := c.set(ctx, key, resp, req); cacheErr != nil {
					c.logger.Warn("cache set error", "error", cacheErr, "key", key)
					// Cache write errors don't fail the request.
				}
			}

			return resp, nil
		})
	}
}
