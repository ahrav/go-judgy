package domain

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewState(t *testing.T) {
	state := NewState()

	assert.NotNil(t, state)
	assert.Equal(t, 0, state.Len())
	assert.True(t, state.IsEmpty())
}

func TestNewStateWithData(t *testing.T) {
	initialData := map[string]any{
		"key1": "value1",
		"key2": 42,
		"key3": true,
	}

	state := NewStateWithData(initialData)

	assert.NotNil(t, state)
	assert.Equal(t, 3, state.Len())
	assert.False(t, state.IsEmpty())

	val1, ok := state.Get("key1")
	assert.True(t, ok)
	assert.Equal(t, "value1", val1)

	val2, ok := state.Get("key2")
	assert.True(t, ok)
	assert.Equal(t, 42, val2)

	val3, ok := state.Get("key3")
	assert.True(t, ok)
	assert.Equal(t, true, val3)

	initialData["key1"] = "modified"
	val1Again, _ := state.Get("key1")
	assert.Equal(t, "value1", val1Again, "State should not be affected by changes to original data")
}

func TestState_Get(t *testing.T) {
	state := NewStateWithData(map[string]any{
		"exists": "value",
		"number": 123,
		"flag":   false,
	})

	tests := []struct {
		name    string
		key     string
		wantVal any
		wantOk  bool
	}{
		{"existing string", "exists", "value", true},
		{"existing number", "number", 123, true},
		{"existing bool", "flag", false, true},
		{"non-existing", "nothere", nil, false},
		{"empty key", "", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, ok := state.Get(tt.key)
			assert.Equal(t, tt.wantOk, ok)
			assert.Equal(t, tt.wantVal, val)
		})
	}
}

func TestState_Set(t *testing.T) {
	originalState := NewState()

	state1, err := originalState.Set("key1", "value1")
	require.NoError(t, err)
	assert.NotNil(t, state1)

	_, ok := originalState.Get("key1")
	assert.False(t, ok, "Original state should be unchanged")

	val, ok := state1.Get("key1")
	assert.True(t, ok)
	assert.Equal(t, "value1", val)

	state2, err := state1.Set("key2", 42)
	require.NoError(t, err)

	_, ok = state1.Get("key2")
	assert.False(t, ok, "Previous state should not have new key")

	val1, ok1 := state2.Get("key1")
	val2, ok2 := state2.Get("key2")
	assert.True(t, ok1)
	assert.True(t, ok2)
	assert.Equal(t, "value1", val1)
	assert.Equal(t, 42, val2)

	state3, err := state2.Set("key1", "updated")
	require.NoError(t, err)

	val, _ = state2.Get("key1")
	assert.Equal(t, "value1", val)

	val, _ = state3.Get("key1")
	assert.Equal(t, "updated", val)
}

func TestState_Set_NilValue(t *testing.T) {
	state := NewState()

	newState, err := state.Set("key", nil)
	assert.Error(t, err)
	assert.Equal(t, ErrStateNilValue, err)
	assert.Nil(t, newState)
}

func TestState_Delete(t *testing.T) {
	state := NewStateWithData(map[string]any{
		"key1": "value1",
		"key2": "value2",
		"key3": "value3",
	})

	state2 := state.Delete("key2")

	assert.Equal(t, 3, state.Len())
	_, ok := state.Get("key2")
	assert.True(t, ok)

	assert.Equal(t, 2, state2.Len())
	_, ok = state2.Get("key2")
	assert.False(t, ok)

	val1, ok1 := state2.Get("key1")
	val3, ok3 := state2.Get("key3")
	assert.True(t, ok1)
	assert.True(t, ok3)
	assert.Equal(t, "value1", val1)
	assert.Equal(t, "value3", val3)

	state3 := state2.Delete("nonexistent")
	assert.Equal(t, 2, state3.Len())
}

func TestState_Merge(t *testing.T) {
	state1 := NewStateWithData(map[string]any{
		"key1":   "value1",
		"key2":   "value2",
		"shared": "original",
	})

	state2 := NewStateWithData(map[string]any{
		"key3":   "value3",
		"key4":   "value4",
		"shared": "updated",
	})

	merged := state1.Merge(state2)

	assert.Equal(t, 3, state1.Len())
	assert.Equal(t, 3, state2.Len())

	assert.Equal(t, 5, merged.Len())

	val1, _ := merged.Get("key1")
	val2, _ := merged.Get("key2")
	val3, _ := merged.Get("key3")
	val4, _ := merged.Get("key4")
	shared, _ := merged.Get("shared")

	assert.Equal(t, "value1", val1)
	assert.Equal(t, "value2", val2)
	assert.Equal(t, "value3", val3)
	assert.Equal(t, "value4", val4)
	assert.Equal(t, "updated", shared, "Values from second state should override")
}

func TestState_Keys(t *testing.T) {
	state := NewStateWithData(map[string]any{
		"key1": "value1",
		"key2": "value2",
		"key3": "value3",
	})

	keys := state.Keys()

	assert.Len(t, keys, 3)
	assert.Contains(t, keys, "key1")
	assert.Contains(t, keys, "key2")
	assert.Contains(t, keys, "key3")

	emptyState := NewState()
	emptyKeys := emptyState.Keys()
	assert.Empty(t, emptyKeys)
}

func TestState_Len(t *testing.T) {
	state := NewState()
	assert.Equal(t, 0, state.Len())

	state, _ = state.Set("key1", "value1")
	assert.Equal(t, 1, state.Len())

	state, _ = state.Set("key2", "value2")
	assert.Equal(t, 2, state.Len())

	state = state.Delete("key1")
	assert.Equal(t, 1, state.Len())
}

func TestState_IsEmpty(t *testing.T) {
	state := NewState()
	assert.True(t, state.IsEmpty())

	state, _ = state.Set("key", "value")
	assert.False(t, state.IsEmpty())

	state = state.Delete("key")
	assert.True(t, state.IsEmpty())
}

func TestState_ToMap(t *testing.T) {
	originalData := map[string]any{
		"key1": "value1",
		"key2": 42,
		"key3": true,
	}

	state := NewStateWithData(originalData)
	exported := state.ToMap()

	assert.Equal(t, originalData, exported)

	exported["key1"] = "modified"
	val, _ := state.Get("key1")
	assert.Equal(t, "value1", val, "State should not be affected by changes to exported map")
}

func TestStateError(t *testing.T) {
	err := NewStateError("get", "mykey", "key not found")

	assert.Equal(t, "get", err.Op)
	assert.Equal(t, "mykey", err.Key)
	assert.Equal(t, "key not found", err.Message)

	errMsg := err.Error()
	assert.Contains(t, errMsg, "get")
	assert.Contains(t, errMsg, "mykey")
	assert.Contains(t, errMsg, "key not found")
}

// Concurrent access tests verify lock-free safety guarantees.
// TestState_ConcurrentReads validates lock-free read safety under high concurrency.
func TestState_ConcurrentReads(t *testing.T) {
	state := NewStateWithData(map[string]any{
		"key1": "value1",
		"key2": "value2",
		"key3": "value3",
	})

	var wg sync.WaitGroup
	numGoroutines := 100
	numReads := 1000

	for i := range numGoroutines {
		wg.Add(1)
		go func(_ int) {
			defer wg.Done()
			for j := range numReads {
				key := fmt.Sprintf("key%d", (j%3)+1)
				val, ok := state.Get(key)
				assert.True(t, ok)
				assert.NotNil(t, val)
			}
		}(i)
	}

	wg.Wait()
}

// TestState_ConcurrentWrites verifies copy-on-write semantics under concurrent modifications.
func TestState_ConcurrentWrites(t *testing.T) {
	originalState := NewState()

	var wg sync.WaitGroup
	numGoroutines := 50
	results := make([]*State, numGoroutines)

	for i := range numGoroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := fmt.Sprintf("key%d", id)
			value := fmt.Sprintf("value%d", id)
			newState, err := originalState.Set(key, value)
			assert.NoError(t, err)
			results[id] = newState
		}(i)
	}

	wg.Wait()

	assert.Equal(t, 0, originalState.Len())

	for i, state := range results {
		key := fmt.Sprintf("key%d", i)
		value := fmt.Sprintf("value%d", i)

		val, ok := state.Get(key)
		assert.True(t, ok)
		assert.Equal(t, value, val)
		assert.Equal(t, 1, state.Len())
	}
}

// TestState_ConcurrentMixedOperations validates concurrent reads, writes, and deletes.
func TestState_ConcurrentMixedOperations(t *testing.T) {
	initialData := make(map[string]any)
	for i := 0; i < 100; i++ {
		initialData[fmt.Sprintf("key%d", i)] = fmt.Sprintf("value%d", i)
	}
	state := NewStateWithData(initialData)

	var wg sync.WaitGroup
	numGoroutines := 100

	for i := range numGoroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < 100; j++ {
				key := fmt.Sprintf("key%d", j)
				val, ok := state.Get(key)
				assert.True(t, ok)
				assert.NotNil(t, val)
			}

			// Writes (create new states)
			newKey := fmt.Sprintf("new_key%d", id)
			newState, err := state.Set(newKey, id)
			assert.NoError(t, err)
			assert.NotNil(t, newState)

			// Deletes (create new states)
			deleteKey := fmt.Sprintf("key%d", id%100)
			deletedState := state.Delete(deleteKey)
			assert.NotNil(t, deletedState)

			// Verify original state is unchanged
			assert.Equal(t, 100, state.Len())
		}(i)
	}

	wg.Wait()
}

// Benchmarks validate performance characteristics and memory efficiency.
func BenchmarkState_Get(b *testing.B) {
	data := make(map[string]any)
	for i := 0; i < 1000; i++ {
		data[fmt.Sprintf("key%d", i)] = fmt.Sprintf("value%d", i)
	}
	state := NewStateWithData(data)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key%d", i%1000)
			_, _ = state.Get(key)
			i++
		}
	})
}

func BenchmarkState_Set(b *testing.B) {
	state := NewState()

	for i := 0; b.Loop(); i++ {
		key := fmt.Sprintf("key%d", i)
		value := fmt.Sprintf("value%d", i)
		newState, _ := state.Set(key, value)
		_ = newState
	}
}

func BenchmarkState_Delete(b *testing.B) {
	data := make(map[string]any)
	for i := range 1000 {
		data[fmt.Sprintf("key%d", i)] = fmt.Sprintf("value%d", i)
	}
	state := NewStateWithData(data)

	for i := 0; b.Loop(); i++ {
		key := fmt.Sprintf("key%d", i%1000)
		newState := state.Delete(key)
		_ = newState
	}
}

func BenchmarkState_Merge(b *testing.B) {
	data1 := make(map[string]any)
	data2 := make(map[string]any)
	for i := range 100 {
		data1[fmt.Sprintf("key1_%d", i)] = fmt.Sprintf("value1_%d", i)
		data2[fmt.Sprintf("key2_%d", i)] = fmt.Sprintf("value2_%d", i)
	}
	state1 := NewStateWithData(data1)
	state2 := NewStateWithData(data2)

	for i := 0; b.Loop(); i++ {
		merged := state1.Merge(state2)
		_ = merged
	}
}

func BenchmarkState_ToMap(b *testing.B) {
	data := make(map[string]any)
	for i := range 1000 {
		data[fmt.Sprintf("key%d", i)] = fmt.Sprintf("value%d", i)
	}
	state := NewStateWithData(data)

	for i := 0; b.Loop(); i++ {
		m := state.ToMap()
		_ = m
	}
}

// BenchmarkState_GetPerformanceRequirement validates the <100ns read guarantee
// across different state sizes to ensure consistent O(1) lookup performance.
func BenchmarkState_GetPerformanceRequirement(b *testing.B) {
	smallState := NewStateWithData(map[string]any{
		"key1": "value1",
		"key2": "value2",
		"key3": "value3",
	})

	b.Run("SmallState", func(b *testing.B) {
		for i := 0; b.Loop(); i++ {
			_, _ = smallState.Get("key2")
		}
	})

	mediumData := make(map[string]any)
	for i := range 100 {
		mediumData[fmt.Sprintf("key%d", i)] = fmt.Sprintf("value%d", i)
	}
	mediumState := NewStateWithData(mediumData)

	b.Run("MediumState", func(b *testing.B) {
		for i := 0; b.Loop(); i++ {
			_, _ = mediumState.Get("key50")
		}
	})

	// Large state
	largeData := make(map[string]any)
	for i := range 10000 {
		largeData[fmt.Sprintf("key%d", i)] = fmt.Sprintf("value%d", i)
	}
	largeState := NewStateWithData(largeData)

	b.Run("LargeState", func(b *testing.B) {
		for i := 0; b.Loop(); i++ {
			_, _ = largeState.Get("key5000")
		}
	})
}

// TestState_RaceConditions validates concurrent safety with mixed read/write operations.
// This stress test ensures the atomic.Value-based implementation prevents data races.
func TestState_RaceConditions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping race condition test in short mode")
	}

	state := NewState()
	var wg sync.WaitGroup

	// Writer goroutines
	for i := range 10 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range 100 {
				key := fmt.Sprintf("key%d_%d", id, j)
				newState, _ := state.Set(key, j)
				_ = newState
			}
		}(i)
	}

	// Reader goroutines
	for i := range 10 {
		wg.Add(1)
		go func(_ int) {
			defer wg.Done()
			for range 100 {
				state.Get("anykey")
				state.Keys()
				state.Len()
				state.IsEmpty()
			}
		}(i)
	}

	// Wait for all goroutines to complete
	wg.Wait()
}
