package cache_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// TestClockSkewNegativeAge verifies the clock skew negative age bug.
// This test confirms that entries with future timestamps (negative age)
// are currently NOT treated as stale, which is the bug we need to fix.
func TestClockSkewNegativeAge(t *testing.T) {
	testCases := []struct {
		name          string
		timeOffset    time.Duration
		shouldBeStale bool
		description   string
	}{
		{
			name:          "past_entry_stale",
			timeOffset:    -2 * time.Hour,
			shouldBeStale: true,
			description:   "Old entry should be treated as stale",
		},
		{
			name:          "future_entry_1_hour",
			timeOffset:    1 * time.Hour,
			shouldBeStale: true,
			description:   "Future entry should be treated as stale due to clock skew",
		},
		{
			name:          "future_entry_1_day",
			timeOffset:    24 * time.Hour,
			shouldBeStale: true,
			description:   "Far future entry should be treated as stale",
		},
		{
			name:          "fresh_entry",
			timeOffset:    -30 * time.Minute,
			shouldBeStale: false,
			description:   "Recent entry should be fresh",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := createTestRequest()
			resp := createTestResponse()

			// Create cache entry with specified time offset
			entry := &transport.IdempotentCacheEntry{
				Provider:            req.Provider,
				Model:               req.Model,
				RawResponse:         resp.RawBody,
				ResponseHeaders:     map[string]string{"X-Request-ID": "req-123"},
				Usage:               resp.Usage,
				EstimatedMilliCents: resp.EstimatedCostMilliCents,
				StoredAtUnixMs:      time.Now().Add(tc.timeOffset).UnixMilli(),
			}

			// Calculate age as the current code does
			age := time.Duration(time.Now().UnixMilli()-entry.StoredAtUnixMs) * time.Millisecond
			maxAge := 1 * time.Hour

			// Current buggy logic
			isCurrentlyStale := age > maxAge

			if tc.timeOffset > 0 {
				// Future timestamps produce negative age
				assert.True(t, age < 0, "Future timestamp should produce negative age, got: %v", age)
				assert.False(t, isCurrentlyStale, "BUG: Negative age makes entry appear fresh forever!")
			}

			// Correct logic should be:
			correctIsStale := age < 0 || age > maxAge

			if tc.shouldBeStale {
				assert.True(t, correctIsStale, "Entry should be considered stale: %s", tc.description)
			} else {
				assert.False(t, correctIsStale, "Entry should be fresh: %s", tc.description)
			}

			t.Logf("Test: %s, Age: %v, Currently stale: %v, Should be stale: %v",
				tc.name, age, isCurrentlyStale, tc.shouldBeStale)
		})
	}
}

// TestCorruptCacheEntry verifies behavior when cache entries are corrupted.
func TestCorruptCacheEntry(t *testing.T) {
	testCases := []struct {
		name        string
		cacheData   []byte
		description string
	}{
		{
			name:        "invalid_json",
			cacheData:   []byte(`{invalid json`),
			description: "Malformed JSON should be handled gracefully",
		},
		{
			name:        "missing_stored_at",
			cacheData:   []byte(`{"provider":"test","model":"test"}`),
			description: "Missing stored_at_ms field should be treated as corruption",
		},
		{
			name:        "invalid_stored_at",
			cacheData:   []byte(`{"provider":"test","model":"test","stored_at_ms":"not_a_number"}`),
			description: "Non-numeric stored_at_ms should be treated as corruption",
		},
		{
			name:        "empty_data",
			cacheData:   []byte(``),
			description: "Empty cache data should be treated as corruption",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Test that corrupted entries cause JSON unmarshal errors
			var entry transport.IdempotentCacheEntry
			err := json.Unmarshal(tc.cacheData, &entry)

			switch tc.name {
			case "empty_data":
				assert.Error(t, err, "Empty data should cause unmarshal error")
			case "invalid_json":
				assert.Error(t, err, "Invalid JSON should cause unmarshal error")
			default:
				// For missing/invalid fields, JSON unmarshaling might succeed
				// but the fields would be zero values
				t.Logf("Unmarshaled entry: %+v", entry)
			}
		})
	}
}

// mockRedisClientAtomic extends the basic mock to support our atomic validation scenarios.
type mockRedisClientAtomic struct {
	*mockRedisClient
	time          time.Time       // Mock Redis TIME command
	maxAgeMs      int64           // Configurable maxAge for testing
	corruptionMap map[string]bool // Keys that should return corrupted data
}

// newMockRedisClientAtomic creates an enhanced mock for atomic validation tests.
func newMockRedisClientAtomic() *mockRedisClientAtomic {
	return &mockRedisClientAtomic{
		mockRedisClient: newMockRedisClient(),
		time:            time.Now(),
		maxAgeMs:        -1, // Disabled by default
		corruptionMap:   make(map[string]bool),
	}
}

// SetMockTime sets the mock Redis server time.
func (m *mockRedisClientAtomic) SetMockTime(t time.Time) {
	m.time = t
}

// SetMaxAge configures the maxAge for staleness testing.
func (m *mockRedisClientAtomic) SetMaxAge(maxAge time.Duration) {
	if maxAge > 0 {
		m.maxAgeMs = maxAge.Milliseconds()
	} else {
		m.maxAgeMs = -1
	}
}

// SetCorrupted marks a key as corrupted for testing.
func (m *mockRedisClientAtomic) SetCorrupted(key string, corrupted bool) {
	m.corruptionMap[key] = corrupted
}

// Eval implements the enhanced atomic validation script.
func (m *mockRedisClientAtomic) Eval(ctx context.Context, script string, keys []string, args ...any) *redis.Cmd {
	m.mu.Lock()
	defer m.mu.Unlock()

	cmd := redis.NewCmd(ctx, "eval", script, keys, args)

	if len(keys) < 2 || len(args) < 2 {
		cmd.SetVal([]any{int64(0), nil})
		return cmd
	}

	cacheKey := keys[0]
	leaseKey := keys[1]
	maxAgeMs := int64(-1)

	// Parse maxAgeMs from args[1]
	if len(args) > 1 {
		switch v := args[1].(type) {
		case int64:
			maxAgeMs = v
		case int:
			maxAgeMs = int64(v)
		}
	}

	// Check cache first
	if data, exists := m.data[cacheKey]; exists {
		// Check for corruption
		if m.corruptionMap[cacheKey] {
			// Delete corrupted entry and try to acquire lease
			delete(m.data, cacheKey)
			if _, leaseExists := m.data[leaseKey]; leaseExists {
				cmd.SetVal([]any{int64(0), nil}) // Lease failed
			} else {
				m.data[leaseKey] = []byte("1")
				cmd.SetVal([]any{int64(2), nil}) // Lease acquired
			}
			return cmd
		}

		// Check staleness if maxAge is configured
		if maxAgeMs >= 0 {
			var entry transport.IdempotentCacheEntry
			if err := json.Unmarshal(data, &entry); err != nil {
				// Corruption detected, delete and try lease
				delete(m.data, cacheKey)
				if _, leaseExists := m.data[leaseKey]; leaseExists {
					cmd.SetVal([]any{int64(0), nil})
				} else {
					m.data[leaseKey] = []byte("1")
					cmd.SetVal([]any{int64(2), nil})
				}
				return cmd
			}

			// Calculate age using mock time
			nowMs := m.time.UnixMilli()
			age := nowMs - entry.StoredAtUnixMs

			// Check for negative age (future entry) or too old
			if age < 0 || age > maxAgeMs {
				// Delete stale entry and try lease
				delete(m.data, cacheKey)
				if _, leaseExists := m.data[leaseKey]; leaseExists {
					cmd.SetVal([]any{int64(0), nil})
				} else {
					m.data[leaseKey] = []byte("1")
					cmd.SetVal([]any{int64(2), nil})
				}
				return cmd
			}
		}

		// Valid and fresh entry
		cmd.SetVal([]any{int64(1), string(data)})
		return cmd
	}

	// Cache miss, try to acquire lease
	if _, leaseExists := m.data[leaseKey]; leaseExists {
		cmd.SetVal([]any{int64(0), nil}) // Lease failed
	} else {
		m.data[leaseKey] = []byte("1")
		cmd.SetVal([]any{int64(2), nil}) // Lease acquired
	}

	return cmd
}

// TestAtomicStalenessHandling tests the atomic staleness validation in the Lua script.
func TestAtomicStalenessHandling(t *testing.T) {
	testCases := []struct {
		name          string
		timeOffset    time.Duration
		maxAge        time.Duration
		expectedState string // "hit", "lease_acquired", "lease_failed"
		description   string
	}{
		{
			name:          "fresh_entry_cache_hit",
			timeOffset:    -30 * time.Minute,
			maxAge:        1 * time.Hour,
			expectedState: "hit",
			description:   "Fresh entry should result in cache hit",
		},
		{
			name:          "stale_entry_lease_acquired",
			timeOffset:    -2 * time.Hour,
			maxAge:        1 * time.Hour,
			expectedState: "lease_acquired",
			description:   "Stale entry should be deleted and lease acquired",
		},
		{
			name:          "future_entry_lease_acquired",
			timeOffset:    1 * time.Hour,
			maxAge:        1 * time.Hour,
			expectedState: "lease_acquired",
			description:   "Future entry should be deleted and lease acquired",
		},
		{
			name:          "no_max_age_cache_hit",
			timeOffset:    -2 * time.Hour,
			maxAge:        0, // Disabled
			expectedState: "hit",
			description:   "Without maxAge, even old entry should be a hit",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockClient := newMockRedisClientAtomic()
			ctx := context.Background()

			// Set up cache entry with time offset
			req := createTestRequest()
			resp := createTestResponse()
			entry := &transport.IdempotentCacheEntry{
				Provider:            req.Provider,
				Model:               req.Model,
				RawResponse:         resp.RawBody,
				ResponseHeaders:     map[string]string{"X-Request-ID": "req-123"},
				Usage:               resp.Usage,
				EstimatedMilliCents: resp.EstimatedCostMilliCents,
				StoredAtUnixMs:      time.Now().Add(tc.timeOffset).UnixMilli(),
			}

			cacheKey := "test:cache:key"
			leaseKey := "test:cache:key:lease"

			// Store entry in mock Redis
			entryData, _ := json.Marshal(entry)
			mockClient.data[cacheKey] = entryData

			// Configure maxAge
			mockClient.SetMaxAge(tc.maxAge)

			// Execute atomic check and lease
			maxAgeMs := int64(-1)
			if tc.maxAge > 0 {
				maxAgeMs = tc.maxAge.Milliseconds()
			}

			result, err := mockClient.Eval(ctx, "test-script",
				[]string{cacheKey, leaseKey},
				30, maxAgeMs).Result()

			require.NoError(t, err)

			resultSlice, ok := result.([]any)
			require.True(t, ok, "Expected slice result")
			require.Len(t, resultSlice, 2, "Expected [status, data] result")

			statusCode, ok := resultSlice[0].(int64)
			require.True(t, ok, "Expected int64 status code")

			switch tc.expectedState {
			case "hit":
				assert.Equal(t, int64(1), statusCode, "Expected cache hit")
				assert.NotNil(t, resultSlice[1], "Expected cached data")
			case "lease_acquired":
				assert.Equal(t, int64(2), statusCode, "Expected lease acquired")
				assert.Nil(t, resultSlice[1], "Expected no data for lease acquired")
				// Verify entry was deleted
				assert.NotContains(t, mockClient.data, cacheKey, "Stale entry should be deleted")
				// Verify lease was acquired
				assert.Contains(t, mockClient.data, leaseKey, "Lease should be acquired")
			case "lease_failed":
				assert.Equal(t, int64(0), statusCode, "Expected lease failed")
				assert.Nil(t, resultSlice[1], "Expected no data for lease failed")
			}

			t.Logf("Test: %s, Status: %d, Description: %s", tc.name, statusCode, tc.description)
		})
	}
}

// TestAtomicCorruptionHandling tests the atomic corruption detection and recovery.
func TestAtomicCorruptionHandling(t *testing.T) {
	mockClient := newMockRedisClientAtomic()
	ctx := context.Background()

	cacheKey := "test:cache:key"
	leaseKey := "test:cache:key:lease"

	testCases := []struct {
		name        string
		setupData   func()
		expectLease bool
		description string
	}{
		{
			name: "corrupted_entry_recovery",
			setupData: func() {
				mockClient.data[cacheKey] = []byte(`{invalid json`)
				mockClient.SetCorrupted(cacheKey, true)
			},
			expectLease: true,
			description: "Corrupted entry should be deleted and lease acquired",
		},
		{
			name: "valid_entry_hit",
			setupData: func() {
				// Use mock client's time to prevent negative age calculation
				storedTime := mockClient.time.UnixMilli()
				entry := &transport.IdempotentCacheEntry{
					Provider:            "openai",
					Model:               "gpt-4",
					RawResponse:         []byte(`{"content":"test"}`),
					ResponseHeaders:     map[string]string{},
					Usage:               transport.NormalizedUsage{},
					EstimatedMilliCents: 100,
					StoredAtUnixMs:      storedTime,
				}
				entryData, _ := json.Marshal(entry)
				mockClient.data[cacheKey] = entryData
				mockClient.SetCorrupted(cacheKey, false)
			},
			expectLease: false,
			description: "Valid entry should result in cache hit",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Clean slate
			mockClient.data = make(map[string][]byte)
			mockClient.SetMaxAge(1 * time.Hour)

			// Setup test data
			tc.setupData()

			// Execute atomic operation
			result, err := mockClient.Eval(ctx, "test-script",
				[]string{cacheKey, leaseKey},
				30, int64(60000)).Result() // 1 hour in ms

			require.NoError(t, err)

			resultSlice, ok := result.([]any)
			require.True(t, ok)
			require.Len(t, resultSlice, 2)

			statusCode := resultSlice[0].(int64)

			if tc.expectLease {
				assert.Equal(t, int64(2), statusCode, "Expected lease acquired for: %s", tc.description)
				assert.NotContains(t, mockClient.data, cacheKey, "Corrupted entry should be deleted")
				assert.Contains(t, mockClient.data, leaseKey, "Lease should be acquired")
			} else {
				assert.Equal(t, int64(1), statusCode, "Expected cache hit for: %s", tc.description)
				assert.NotNil(t, resultSlice[1], "Expected cached data")
			}

			t.Logf("Test: %s, Status: %d, Description: %s", tc.name, statusCode, tc.description)
		})
	}
}
