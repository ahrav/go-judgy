package circuit_breaker

import (
	"fmt"
	"sync"
	"sync/atomic"

	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
)

// Constants for sharding implementation.
const (
	// HashMultiplier is used in the hash function for shard selection.
	hashMultiplier = 31
)

// shardedBreakers implements sharded locking to reduce contention.
// It distributes circuit breakers across 16 shards using a hash function.
// This minimizes lock contention in high-concurrency scenarios while maintaining
// consistent access patterns and performance characteristics.
type shardedBreakers struct {
	shards [16]struct {
		sync.RWMutex
		breakers map[string]*circuitBreaker
	}
	total atomic.Int64 // Thread-safe total count of circuit breakers
}

// newShardedBreakers creates a new sharded breaker store.
func newShardedBreakers() *shardedBreakers {
	sb := new(shardedBreakers)
	for i := range sb.shards {
		sb.shards[i].breakers = make(map[string]*circuitBreaker)
	}
	return sb
}

// getShard returns the shard index for a given key.
func (sb *shardedBreakers) getShard(key string) int {
	var hash uint32
	for i := 0; i < len(key); i++ {
		hash = hash*hashMultiplier + uint32(key[i])
	}
	return int(hash % uint32(len(sb.shards)))
}

// get returns a circuit breaker for the key if it exists.
func (sb *shardedBreakers) get(key string) (*circuitBreaker, bool) {
	shard := &sb.shards[sb.getShard(key)]
	shard.RLock()
	breaker, exists := shard.breakers[key]
	shard.RUnlock()
	return breaker, exists
}

// getOrCreate returns an existing breaker or creates a new one atomically.
// It implements double-checked locking to minimize contention while ensuring thread safety.
// It returns the breaker and an error if the maximum breaker limit is reached.
func (sb *shardedBreakers) getOrCreate(
	key string,
	create func() *circuitBreaker,
	maxBreakers int,
) (*circuitBreaker, error) {
	if breaker, exists := sb.get(key); exists {
		return breaker, nil
	}

	shard := &sb.shards[sb.getShard(key)]
	shard.Lock()
	defer shard.Unlock()

	if breaker, exists := shard.breakers[key]; exists {
		return breaker, nil
	}

	if maxBreakers > 0 && int(sb.total.Load()) >= maxBreakers {
		return nil, &llmerrors.ProviderError{
			Code:    "CIRCUIT_BREAKER_LIMIT",
			Message: fmt.Sprintf("circuit breaker limit reached (%d), cannot create new breaker for key: %s", maxBreakers, key),
			Type:    llmerrors.ErrorTypeCircuitBreaker,
		}
	}

	breaker := create()
	shard.breakers[key] = breaker
	sb.total.Add(1)
	return breaker, nil
}
