package circuitbreaker

import (
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// Constants for adaptive threshold adjustment.
const (
	// MinRequestsForAdjustment is the minimum number of requests before adjusting thresholds.
	minRequestsForAdjustment = 10
	// HighErrorRateThreshold is the error rate threshold for aggressive reduction.
	highErrorRateThreshold = 0.5
	// MediumErrorRateThreshold is the error rate threshold for moderate reduction.
	mediumErrorRateThreshold = 0.3
	// MediumThresholdMultiplier is the multiplier for medium error rate threshold adjustment.
	mediumThresholdMultiplier = 0.75
	// HighThresholdDivisor is used to calculate the threshold for high error rates.
	highThresholdDivisor = 2
)

// adaptiveThresholds manages dynamic threshold adjustment based on error rates.
// It implements a sliding window algorithm that adjusts failure thresholds based on
// historical error patterns, enabling more sensitive detection during high error periods
// and more permissive behavior during stable periods.
type adaptiveThresholds struct {
	mu                   sync.Mutex // Protects window reset operations
	baseFailureThreshold int
	currentThreshold     atomic.Int32
	totalRequests        atomic.Int64
	totalFailures        atomic.Int64
	windowStart          atomic.Int64
	windowDuration       time.Duration
}

// newAdaptiveThresholds creates a new adaptive threshold manager.
func newAdaptiveThresholds(baseThreshold int) *adaptiveThresholds {
	at := &adaptiveThresholds{
		baseFailureThreshold: baseThreshold,
		windowDuration:       1 * time.Minute,
	}
	// Ensure baseThreshold fits in int32.
	var threshold int32
	if baseThreshold < 0 || baseThreshold > math.MaxInt32 {
		threshold = math.MaxInt32
	} else {
		threshold = int32(baseThreshold)
	}
	at.currentThreshold.Store(threshold)
	at.windowStart.Store(time.Now().UnixNano())
	return at
}

// recordRequest records a request and its outcome.
func (at *adaptiveThresholds) recordRequest(success bool) {
	now := time.Now().UnixNano()

	// Check if window needs reset (lock-free fast path).
	if now-at.windowStart.Load() > int64(at.windowDuration) {
		at.mu.Lock()
		// Re-check under lock to avoid double resets.
		if now-at.windowStart.Load() > int64(at.windowDuration) {
			// Zero counters first, then publish the new windowStart.
			// This prevents other goroutines from seeing a half-reset state.
			at.totalRequests.Store(0)
			at.totalFailures.Store(0)
			at.windowStart.Store(now)
		}
		at.mu.Unlock()
	}

	at.totalRequests.Add(1)
	if !success {
		at.totalFailures.Add(1)
	}

	at.adjust()
}

// adjust updates the threshold based on current error rate.
func (at *adaptiveThresholds) adjust() {
	total := at.totalRequests.Load()
	if total < minRequestsForAdjustment {
		return
	}

	failures := at.totalFailures.Load()
	errorRate := float64(failures) / float64(total)

	var newThreshold int32
	switch {
	case errorRate > highErrorRateThreshold:
		// Ensure division result fits in int32.
		halfThreshold := at.baseFailureThreshold / highThresholdDivisor
		if halfThreshold < 0 || halfThreshold > math.MaxInt32 {
			newThreshold = math.MaxInt32
		} else {
			newThreshold = int32(halfThreshold)
		}
	case errorRate > mediumErrorRateThreshold:
		// Ensure multiplication result fits in int32.
		adjustedThreshold := float64(at.baseFailureThreshold) * mediumThresholdMultiplier
		if adjustedThreshold > math.MaxInt32 {
			newThreshold = math.MaxInt32
		} else {
			newThreshold = int32(adjustedThreshold)
		}
	default:
		// Ensure baseFailureThreshold fits in int32.
		if at.baseFailureThreshold < 0 || at.baseFailureThreshold > math.MaxInt32 {
			newThreshold = math.MaxInt32
		} else {
			newThreshold = int32(at.baseFailureThreshold)
		}
	}

	if newThreshold < 1 {
		newThreshold = 1
	}

	at.currentThreshold.Store(newThreshold)
}

// getThreshold returns the current failure threshold.
func (at *adaptiveThresholds) getThreshold() int { return int(at.currentThreshold.Load()) }
