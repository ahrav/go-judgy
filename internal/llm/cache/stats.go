package cache

import (
	"context"
	"time"
)

// Stats holds performance metrics for the cache middleware.
// It includes counters for cache hits, misses, and errors, as well as
// statistics for the underlying Redis connection pool.
type Stats struct {
	// Hits is the total number of cache hits.
	Hits int64
	// Misses is the total number of cache misses.
	Misses int64
	// Errors is the total number of errors encountered during cache operations.
	Errors int64
	// HitRate is the ratio of hits to the total number of lookups (hits + misses).
	HitRate float64
	// AvgLatency is the average latency for cache operations.
	// This field is not yet implemented.
	AvgLatency time.Duration

	// PoolHits is the number of times a free connection was found in the pool.
	PoolHits uint32
	// PoolMisses is the number of times a free connection was not found in the pool.
	PoolMisses uint32
	// PoolTimeouts is the number of times a wait for a connection timed out.
	PoolTimeouts uint32
	// PoolTotalConns is the total number of connections in the pool.
	PoolTotalConns uint32
	// PoolIdleConns is the number of idle connections in the pool.
	PoolIdleConns uint32
	// PoolStaleConns is the number of stale connections that were closed.
	PoolStaleConns uint32
}

// GetStats returns current cache performance metrics.
// All counters are read atomically and safe for concurrent access.
// Includes hit/miss ratios, error counts, and Redis pool statistics.
func (c *cacheMiddleware) GetStats(_ context.Context) (*Stats, error) {
	hits := c.hits.Load()
	misses := c.misses.Load()
	errors := c.errors.Load()

	var hitRate float64
	if total := hits + misses; total > 0 {
		hitRate = float64(hits) / float64(total)
	}

	stats := &Stats{
		Hits:    hits,
		Misses:  misses,
		Errors:  errors,
		HitRate: hitRate,
	}

	if c.client != nil {
		poolStats := c.client.PoolStats()
		stats.PoolHits = poolStats.Hits
		stats.PoolMisses = poolStats.Misses
		stats.PoolTimeouts = poolStats.Timeouts
		stats.PoolTotalConns = poolStats.TotalConns
		stats.PoolIdleConns = poolStats.IdleConns
		stats.PoolStaleConns = poolStats.StaleConns
	}

	return stats, nil
}
