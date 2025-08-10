package circuitbreaker

import (
	"sync/atomic"
	"time"
)

// circuitBreakerMetrics tracks circuit breaker performance metrics.
// These metrics provide observability into circuit breaker behavior and
// can be used for monitoring, alerting, and performance analysis.
type circuitBreakerMetrics struct {
	stateTransitions    atomic.Int64 // Total state transitions
	requestsAllowed     atomic.Int64 // Total requests allowed
	requestsRejected    atomic.Int64 // Total requests rejected
	probeAttempts       atomic.Int64 // Total probe attempts
	probeSuccesses      atomic.Int64 // Total successful probes
	probeGuardConflicts atomic.Int64 // Redis probe guard conflicts
	timeInClosed        atomic.Int64 // Nanoseconds in closed state
	timeInOpen          atomic.Int64 // Nanoseconds in open state
	timeInHalfOpen      atomic.Int64 // Nanoseconds in half-open state
	lastStateChange     atomic.Int64 // Timestamp of last state change
}

// updateStateTime updates the time spent in the current state.
func (m *circuitBreakerMetrics) updateStateTime(currentState CircuitState) {
	now := time.Now().UnixNano()
	lastChange := m.lastStateChange.Load()
	if lastChange == 0 {
		m.lastStateChange.Store(now)
		return
	}

	duration := now - lastChange
	switch currentState {
	case StateClosed:
		m.timeInClosed.Add(duration)
	case StateOpen:
		m.timeInOpen.Add(duration)
	case StateHalfOpen:
		m.timeInHalfOpen.Add(duration)
	}
	m.lastStateChange.Store(now)
}

// Stats provides comprehensive circuit breaker performance metrics.
type Stats struct {
	// TotalBreakers is the total number of active circuit breakers.
	TotalBreakers int `json:"total_breakers"`
	// StateCount maps each circuit state to the number of breakers in that state.
	StateCount map[string]int `json:"state_count"`

	// TotalStateTransitions is the total number of state changes across all breakers.
	TotalStateTransitions int64 `json:"total_state_transitions"`
	// TotalRequestsAllowed is the total number of requests permitted by all breakers.
	TotalRequestsAllowed int64 `json:"total_requests_allowed"`
	// TotalRequestsRejected is the total number of requests blocked by all breakers.
	TotalRequestsRejected int64 `json:"total_requests_rejected"`
	// TotalProbeAttempts is the total number of half-open probe requests initiated.
	TotalProbeAttempts int64 `json:"total_probe_attempts"`
	// TotalProbeSuccesses is the total number of successful half-open probe requests.
	TotalProbeSuccesses int64 `json:"total_probe_successes"`
	// TotalProbeGuardConflicts is the total number of Redis probe guard conflicts.
	TotalProbeGuardConflicts int64 `json:"total_probe_guard_conflicts"`
}
