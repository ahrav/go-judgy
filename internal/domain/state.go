package domain

import (
	"errors"
	"maps"
	"sync/atomic"
)

// State-specific errors.
var (
	// ErrStateKeyNotFound indicates that a requested key does not exist in the state.
	ErrStateKeyNotFound = errors.New("key not found in state")

	// ErrStateNilValue indicates an attempt to store a nil value in state.
	ErrStateNilValue = errors.New("cannot store nil value in state")
)

// State represents an immutable state container with copy-on-write semantics.
// It provides lock-free read access to key-value pairs with optimized performance.
// The underlying map is never mutated; all write operations return a new State instance.
type State struct {
	// m holds the immutable map[string]any via atomic.Value for lock-free reads
	m atomic.Value
}

// NewState creates a new empty state.
func NewState() *State {
	s := new(State)
	s.m.Store(make(map[string]any))
	return s
}

// NewStateWithData creates a new state with initial data.
// Input data is copied to preserve immutability.
func NewStateWithData(data map[string]any) *State {
	copied := make(map[string]any, len(data))
	maps.Copy(copied, data)
	s := new(State)
	s.m.Store(copied)
	return s
}

// Get retrieves a value by key.
// Returns the value and true if found, or nil and false if not found.
// Lock-free operation optimized for <100ns performance.
func (s *State) Get(key string) (any, bool) {
	m := s.m.Load().(map[string]any) //nolint:errcheck // always initialized with map
	val, ok := m[key]
	return val, ok
}

// Set creates a new state with the key-value pair added or updated.
// Returns a new State instance; the original is not modified.
func (s *State) Set(key string, value any) (*State, error) {
	if value == nil {
		return nil, ErrStateNilValue
	}

	old := s.m.Load().(map[string]any) //nolint:errcheck // always initialized with map

	newData := make(map[string]any, len(old)+1)
	maps.Copy(newData, old)

	newData[key] = value

	ns := new(State)
	ns.m.Store(newData)
	return ns, nil
}

// Delete creates a new state without the specified key.
// If key doesn't exist, returns a copy of the current state.
func (s *State) Delete(key string) *State {
	old := s.m.Load().(map[string]any) //nolint:errcheck // always initialized with map

	if _, exists := old[key]; !exists {
		return s.clone()
	}

	newData := make(map[string]any, len(old)-1)
	for k, v := range old {
		if k != key {
			newData[k] = v
		}
	}

	ns := new(State)
	ns.m.Store(newData)
	return ns
}

// Merge creates a new state by combining this state with another.
// Values from other override matching keys in this state.
func (s *State) Merge(other *State) *State {
	oldThis := s.m.Load().(map[string]any)      //nolint:errcheck // always initialized with map
	oldOther := other.m.Load().(map[string]any) //nolint:errcheck // always initialized with map

	newData := make(map[string]any, len(oldThis)+len(oldOther))

	maps.Copy(newData, oldThis)
	maps.Copy(newData, oldOther)

	ns := new(State)
	ns.m.Store(newData)
	return ns
}

// Keys returns all keys in the state.
func (s *State) Keys() []string {
	m := s.m.Load().(map[string]any) //nolint:errcheck // always initialized with map
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// Len returns the number of key-value pairs.
func (s *State) Len() int {
	m := s.m.Load().(map[string]any) //nolint:errcheck // always initialized with map
	return len(m)
}

// IsEmpty reports whether the state contains no data.
func (s *State) IsEmpty() bool {
	m := s.m.Load().(map[string]any) //nolint:errcheck // always initialized with map
	return len(m) == 0
}

// clone creates a copy of the current state.
func (s *State) clone() *State {
	old := s.m.Load().(map[string]any) //nolint:errcheck // always initialized with map
	newData := make(map[string]any, len(old))
	maps.Copy(newData, old)
	ns := new(State)
	ns.m.Store(newData)
	return ns
}

// ToMap returns a copy of the state data as a regular map.
// Modifications to the returned map don't affect the state.
func (s *State) ToMap() map[string]any {
	m := s.m.Load().(map[string]any) //nolint:errcheck // always initialized with map
	result := make(map[string]any, len(m))
	maps.Copy(result, m)
	return result
}

// StateError represents an error related to state operations.
type StateError struct {
	Op      string // Operation that failed.
	Key     string // State key involved in the operation.
	Message string // Additional error context.
}

// Error returns a formatted error message for the state error.
func (e StateError) Error() string {
	return "state operation '" + e.Op + "' failed for key '" + e.Key + "': " + e.Message
}

// NewStateError creates a new state error with the specified details.
func NewStateError(op, key, message string) StateError {
	return StateError{
		Op:      op,
		Key:     key,
		Message: message,
	}
}
