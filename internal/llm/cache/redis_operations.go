package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// checkCacheEntryValidityAndLease is a Lua script that atomically checks for a cached value,
// validates it for corruption and staleness, and acquires a lease if needed.
// It returns a table containing a status code and the cached data, if any.
// Status codes: 1 for a cache hit, 2 for a successful lease acquisition,
// and 0 if the lease is already held.
//
// KEYS[1] = cacheKey
// KEYS[2] = leaseKey
// ARGV[1] = lease TTL in seconds
// ARGV[2] = maxAgeMs (pass -1 to disable staleness checking).
const atomicCacheHitOrLease = `
	local cached = redis.call('GET', KEYS[1])
	if cached then
		-- Optional fast reject for obviously invalid data
		if string.len(cached) < 2 or string.sub(cached, 1, 1) ~= '{' then
			redis.call('DEL', KEYS[1])
			local leased = redis.call('SET', KEYS[2], '1', 'NX', 'EX', ARGV[1])
			if leased then return {2, nil} else return {0, nil} end
		end

		local maxAgeMs = tonumber(ARGV[2]) or -1
		if maxAgeMs >= 0 then
			local ok, obj = pcall(cjson.decode, cached)
			if not ok or type(obj) ~= 'table' then
				-- JSON decode failed, treat as corrupted
				redis.call('DEL', KEYS[1])
				local leased = redis.call('SET', KEYS[2], '1', 'NX', 'EX', ARGV[1])
				if leased then return {2, nil} else return {0, nil} end
			end

			local storedAt = tonumber(obj["stored_at_ms"])
			if not storedAt then
				-- Missing or invalid stored_at_ms field
				redis.call('DEL', KEYS[1])
				local leased = redis.call('SET', KEYS[2], '1', 'NX', 'EX', ARGV[1])
				if leased then return {2, nil} else return {0, nil} end
			end

			-- Get server time and calculate age
			local now = redis.call('TIME')        -- {sec, usec}
			local nowMs = now[1] * 1000 + math.floor(now[2] / 1000)
			local age = nowMs - storedAt

			-- Check for negative age (future timestamp) or exceeds maxAge
			if age < 0 or age > maxAgeMs then
				redis.call('DEL', KEYS[1])
				local leased = redis.call('SET', KEYS[2], '1', 'NX', 'EX', ARGV[1])
				if leased then return {2, nil} else return {0, nil} end
			end
		end

		-- Entry is valid and fresh
		return {1, cached}
	end

	-- Cache miss, try to acquire lease
	local leased = redis.call('SET', KEYS[2], '1', 'NX', 'EX', ARGV[1])
	if leased then return {2, nil} end
	return {0, nil}
`

// cacheStatus represents the outcome of an atomic cache-and-lease operation.
type cacheStatus int

const (
	leaseFailed   cacheStatus = 0
	cacheHit      cacheStatus = 1
	leaseAcquired cacheStatus = 2
)

// atomicCheckAndLease performs an atomic cache check and lease acquisition
// using a Lua script. This approach prevents race conditions where multiple
// instances might simultaneously miss the cache and attempt to perform the same
// work. It returns the status of the operation (hit, miss, or lease failed),
// the cached response if available, and a flag indicating if the lease was acquired.
func (c *cacheMiddleware) atomicCheckAndLease(
	ctx context.Context, cacheKey, leaseKey string, leaseTTL time.Duration,
) (cacheStatus, *transport.Response, bool, error) {
	if c.client == nil {
		return leaseAcquired, nil, true, nil
	}

	// Configure staleness checking for Lua script.
	maxAgeMs := int64(-1) // Negative value disables staleness validation.
	if c.maxAge > 0 {
		maxAgeMs = c.maxAge.Milliseconds()
	}

	// Execute Lua script for atomic cache check and lease acquisition.
	result, err := c.client.Eval(ctx, atomicCacheHitOrLease,
		[]string{cacheKey, leaseKey},
		int(leaseTTL.Seconds()), maxAgeMs).Result()
	if err != nil {
		return leaseFailed, nil, false, fmt.Errorf("atomic check-and-lease failed: %w", err)
	}

	// Parse Lua script return values.
	resultSlice, ok := result.([]any)
	if !ok || len(resultSlice) != 2 {
		return leaseFailed, nil, false, fmt.Errorf("unexpected script result format")
	}

	statusCode, ok := resultSlice[0].(int64)
	if !ok {
		return leaseFailed, nil, false, fmt.Errorf("invalid status code in script result")
	}

	status := cacheStatus(statusCode)

	switch status {
	case cacheHit:
		// Redis Eval returns different types based on Lua script output.
		var raw []byte
		switch v := resultSlice[1].(type) {
		case string:
			raw = []byte(v)
		case []byte:
			raw = v
		default:
			return leaseFailed, nil, false, fmt.Errorf("invalid cached data type %T", v)
		}

		var entry transport.IdempotentCacheEntry
		if err := json.Unmarshal(raw, &entry); err != nil {
			// Lua script validates JSON, but keep safety fallback.
			c.logger.Error("unexpected cache unmarshal error after script validation",
				"error", err, "key", cacheKey)
			return leaseFailed, nil, false, fmt.Errorf("cache entry unmarshal failed: %w", err)
		}

		resp := c.entryToResponse(&entry)
		return cacheHit, resp, false, nil

	case leaseAcquired:
		return leaseAcquired, nil, true, nil

	default: // leaseFailed - another process holds the lease.
		return leaseFailed, nil, false, nil
	}
}
