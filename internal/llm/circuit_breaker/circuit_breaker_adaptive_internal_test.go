// Package resilience provides internal tests for adaptive threshold functionality.
// These tests require access to unexported types and functions.
package circuit_breaker

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestAdaptiveThreshold_WindowReset_NoLostCounts verifies that the window reset
// mechanism correctly handles concurrent requests without losing counts.
// This test catches the race condition where multiple goroutines could
// simultaneously reset the window, causing lost increments.
func TestAdaptiveThreshold_WindowReset_NoLostCounts(t *testing.T) {
	at := newAdaptiveThresholds(10)
	at.windowDuration = 1 * time.Millisecond // Short window for testing

	// Force window to be expired
	at.windowStart.Store(time.Now().Add(-10 * time.Millisecond).UnixNano())

	const N = 200
	var wg sync.WaitGroup
	wg.Add(N)

	// Align goroutines to increase overlap in the reset section
	start := make(chan struct{})
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			<-start
			at.recordRequest(true) // All successful requests
		}()
	}

	// Release all goroutines simultaneously
	close(start)
	runtime.Gosched() // Give goroutines a chance to start
	wg.Wait()

	got := at.totalRequests.Load()
	if got != N {
		t.Fatalf("lost counts due to window reset race: got=%d want=%d", got, N)
	}
}

// TestAdaptiveThreshold_WindowReset_MixedSuccessFailure tests window reset
// with a mix of successful and failed requests to ensure both counters
// are correctly maintained during concurrent resets.
func TestAdaptiveThreshold_WindowReset_MixedSuccessFailure(t *testing.T) {
	at := newAdaptiveThresholds(10)
	at.windowDuration = 1 * time.Millisecond

	// Force window to be expired
	at.windowStart.Store(time.Now().Add(-10 * time.Millisecond).UnixNano())

	const N = 200
	const expectedFailures = N / 2
	var wg sync.WaitGroup
	wg.Add(N)

	// Align goroutines to increase overlap
	start := make(chan struct{})
	for i := 0; i < N; i++ {
		shouldFail := i%2 == 0
		go func(fail bool) {
			defer wg.Done()
			<-start
			at.recordRequest(!fail)
		}(shouldFail)
	}

	close(start)
	runtime.Gosched()
	wg.Wait()

	gotRequests := at.totalRequests.Load()
	gotFailures := at.totalFailures.Load()

	if gotRequests != N {
		t.Errorf("lost request counts: got=%d want=%d", gotRequests, N)
	}
	if gotFailures != expectedFailures {
		t.Errorf("lost failure counts: got=%d want=%d", gotFailures, expectedFailures)
	}
}

// TestAdaptiveThreshold_ThresholdAdjustmentDuringReset verifies that threshold
// adjustments work correctly even when window resets occur concurrently.
func TestAdaptiveThreshold_ThresholdAdjustmentDuringReset(t *testing.T) {
	at := newAdaptiveThresholds(10)
	at.windowDuration = 5 * time.Millisecond

	// Initial requests to establish a pattern
	for i := 0; i < 20; i++ {
		at.recordRequest(i%2 == 0) // 50% failure rate
	}

	// Check that threshold was adjusted (should be reduced from base 10)
	// With 50% error rate, it should be either 5 (50% of base) or 7 (75% of base)
	// depending on exact timing of the adjust() calls
	threshold := at.getThreshold()
	if threshold >= 10 {
		t.Errorf("initial threshold adjustment failed: got=%d, expected reduction from base 10", threshold)
	}

	// Force window expiry and concurrent requests
	time.Sleep(10 * time.Millisecond)

	const N = 100
	var wg sync.WaitGroup
	wg.Add(N)

	start := make(chan struct{})
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			<-start
			at.recordRequest(true) // All successful
		}()
	}

	close(start)
	wg.Wait()

	// After all successful requests, threshold should return to base (10)
	// Give adjust() time to run
	time.Sleep(1 * time.Millisecond)

	finalThreshold := at.getThreshold()
	if finalThreshold != 10 {
		t.Errorf("threshold not restored after successful requests: got=%d want=10", finalThreshold)
	}

	// Verify counts are correct
	got := at.totalRequests.Load()
	if got != N {
		t.Errorf("lost counts during threshold adjustment: got=%d want=%d", got, N)
	}
}

// TestAdaptiveThreshold_MultipleWindowResets tests behavior across multiple
// window boundaries to ensure the fix handles repeated resets correctly.
func TestAdaptiveThreshold_MultipleWindowResets(t *testing.T) {
	at := newAdaptiveThresholds(10)
	at.windowDuration = 2 * time.Millisecond

	var totalRequests int64

	// Simulate 5 window periods
	for window := 0; window < 5; window++ {
		const requestsPerWindow = 50
		var wg sync.WaitGroup
		wg.Add(requestsPerWindow)

		for i := 0; i < requestsPerWindow; i++ {
			go func() {
				defer wg.Done()
				at.recordRequest(true)
			}()
		}

		wg.Wait()
		totalRequests += requestsPerWindow

		// Current window should have the requests from this iteration
		currentCount := at.totalRequests.Load()
		if currentCount > requestsPerWindow {
			t.Errorf("window %d: count exceeds expected: got=%d want<=%d",
				window, currentCount, requestsPerWindow)
		}

		// Wait for window to expire
		time.Sleep(3 * time.Millisecond)
	}
}

// BenchmarkAdaptiveThreshold_WindowReset benchmarks the performance impact
// of the mutex-based fix for window resets.
func BenchmarkAdaptiveThreshold_WindowReset(b *testing.B) {
	at := newAdaptiveThresholds(10)
	at.windowDuration = 10 * time.Millisecond

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			at.recordRequest(true)
		}
	})

	b.ReportMetric(float64(at.totalRequests.Load())/float64(b.N), "requests/op")
}

// BenchmarkAdaptiveThreshold_NoReset benchmarks the fast path when no reset is needed.
func BenchmarkAdaptiveThreshold_NoReset(b *testing.B) {
	at := newAdaptiveThresholds(10)
	at.windowDuration = 1 * time.Hour // Long window to avoid resets

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			at.recordRequest(true)
		}
	})
}
