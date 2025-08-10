package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// get retrieves and deserializes a cached response from Redis.
// This method is primarily used as a fallback in lease retry scenarios.
// Staleness validation is now handled atomically in the Lua script for the main flow.
// It returns redis.Nil if the entry is not found or is corrupted.
func (c *cacheMiddleware) get(ctx context.Context, key string) (*transport.Response, error) {
	if c.client == nil {
		return nil, redis.Nil
	}

	data, err := c.client.Get(ctx, key).Bytes()
	if err != nil {
		return nil, err
	}

	var entry transport.IdempotentCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		c.logger.Error("cache unmarshal error", "error", err, "key", key)
		// Remove corrupted cache entry.
		_ = c.client.Del(ctx, key)
		return nil, redis.Nil
	}

	// Reconstruct full Response from compact cache entry.
	resp := c.entryToResponse(&entry)
	return resp, nil
}

// set serializes and stores a response in Redis with a dynamically determined TTL.
// It converts the transport.Response into a more compact IdempotentCacheEntry
// to save space. The TTL is chosen based on the operation type to optimize
// cache effectiveness. An error during this operation will be logged but will
// not fail the overall request.
func (c *cacheMiddleware) set(
	ctx context.Context,
	key string,
	resp *transport.Response,
	req *transport.Request,
) error {
	if c.client == nil {
		return nil
	}

	// Create compact cache entry from full Response.
	entry := c.responseToEntry(resp, req)

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal cache entry: %w", err)
	}

	// Apply operation-specific TTL for optimal cache efficiency.
	ttl := c.getTTL(req)
	return c.client.Set(ctx, key, data, ttl).Err()
}

// responseToEntry converts a transport.Response into a lightweight
// transport.IdempotentCacheEntry for storage. This reduces the memory
// footprint in Redis by storing only essential information, such as a subset
// of headers, usage data, and the raw response body.
func (c *cacheMiddleware) responseToEntry(
	resp *transport.Response,
	req *transport.Request,
) *transport.IdempotentCacheEntry {
	// Store only essential headers to minimize Redis memory usage.
	essential := make(map[string]string)
	for key, values := range resp.Headers {
		if len(values) > 0 {
			// Preserve headers needed for debugging and audit trails.
			switch key {
			case "Content-Type", "X-Request-ID", "X-RateLimit-Remaining":
				essential[key] = values[0]
			}
		}
	}

	return &transport.IdempotentCacheEntry{
		Provider:            req.Provider,
		Model:               req.Model,
		RawResponse:         resp.RawBody,
		ResponseHeaders:     essential,
		Usage:               resp.Usage,
		EstimatedMilliCents: resp.EstimatedCostMilliCents,
		StoredAtUnixMs:      time.Now().UnixMilli(),
	}
}

// entryToResponse reconstructs a full transport.Response from a cached
// transport.IdempotentCacheEntry. It parses provider-specific content from the
// raw response body and restores headers, usage data, and other metadata.
func (c *cacheMiddleware) entryToResponse(entry *transport.IdempotentCacheEntry) *transport.Response {
	// Rebuild header map from stored essential headers.
	headers := make(map[string][]string)
	for key, value := range entry.ResponseHeaders {
		headers[key] = []string{value}
	}

	return &transport.Response{
		Content:                 extractContentFromRawResponse(entry.RawResponse, entry.Provider),
		FinishReason:            extractFinishReasonFromUsage(&entry.Usage),
		ProviderRequestIDs:      extractRequestIDsFromHeaders(entry.ResponseHeaders),
		Usage:                   entry.Usage,
		EstimatedCostMilliCents: entry.EstimatedMilliCents,
		Headers:                 headers,
		RawBody:                 entry.RawResponse,
	}
}
