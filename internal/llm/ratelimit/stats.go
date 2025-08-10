package ratelimit

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
