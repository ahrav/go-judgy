package cache

import (
	"fmt"
	"time"

	"github.com/ahrav/go-judgy/internal/llm/transport"
)

const (
	// Idempotency key constraints.
	maxIdempotencyKeyLength = 256
	minIdempotencyKeyLength = 8

	// Cache TTL durations.
	generationCacheTTL = 24 * time.Hour
)

// buildKey constructs a cache key for a given request after validating its fields.
// The key format is "llm:{tenant}:{operation}:{idemkey}".
// It returns an error if any required fields for the key are missing or invalid.
func (c *cacheMiddleware) buildKey(req *transport.Request) (string, error) {
	// Validate required fields for cache key construction.
	if err := c.validateCacheKeyFields(req); err != nil {
		return "", fmt.Errorf("invalid request for cache key: %w", err)
	}

	return transport.CacheKey(req.TenantID, req.Operation, transport.IdemKey(req.IdempotencyKey)), nil
}

// validateCacheKeyFields checks if a request contains the necessary and valid
// fields for generating a cache key. It enforces the presence of a tenant ID
// and operation, validates the idempotency key length, and ensures the
// operation type is supported for caching.
func (c *cacheMiddleware) validateCacheKeyFields(req *transport.Request) error {
	if req.TenantID == "" {
		return fmt.Errorf("tenant ID is required")
	}
	if req.Operation == "" {
		return fmt.Errorf("operation is required")
	}
	if req.IdempotencyKey == "" {
		return fmt.Errorf("idempotency key is required for caching")
	}

	// Enforce idempotency key length constraints.
	if len(req.IdempotencyKey) > maxIdempotencyKeyLength {
		return fmt.Errorf("idempotency key too long (max %d chars): %d", maxIdempotencyKeyLength, len(req.IdempotencyKey))
	}
	if len(req.IdempotencyKey) < minIdempotencyKeyLength {
		return fmt.Errorf("idempotency key too short (min %d chars): %d", minIdempotencyKeyLength, len(req.IdempotencyKey))
	}

	// Verify operation type is cacheable.
	switch req.Operation {
	case transport.OpGeneration, transport.OpScoring:
	default:
		return fmt.Errorf("invalid operation: %s", req.Operation)
	}

	return nil
}

// getTTL determines the cache TTL for a request based on its operation type.
// More expensive operations, like content generation, are given a longer TTL
// than cheaper operations, like scoring, to maximize cache utility.
func (c *cacheMiddleware) getTTL(req *transport.Request) time.Duration {
	switch req.Operation {
	case transport.OpGeneration:
		// Expensive operations benefit from longer cache duration.
		return generationCacheTTL
	case transport.OpScoring:
		// Cheaper operations use shorter cache duration.
		return 1 * time.Hour
	default:
		// Fallback to default TTL for unrecognized operations.
		return c.ttl
	}
}
