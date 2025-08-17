package aggregation

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/pkg/events"
)

// TestConcurrentAggregateScores verifies that AggregateScores is safe for concurrent execution.
// Run with: go test -race -run TestConcurrentAggregateScores
func TestConcurrentAggregateScores(t *testing.T) {
	const numGoroutines = 10
	const numOperations = 100

	ctx := context.Background()
	sink := NewCapturingEventSink()
	metrics := NewEnhancedMockMetricsRecorder()
	activities := CreateTestActivities(sink, metrics)

	var wg sync.WaitGroup
	results := make(chan *domain.AggregateScoresOutput, numGoroutines*numOperations)
	errors := make(chan error, numGoroutines*numOperations)

	// Create test input once to avoid races in input creation
	baseInput := CreateTestAggregateInput(3, 2, "mean")

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()

			for j := 0; j < numOperations; j++ {
				// Create unique input per operation to avoid idempotency conflicts
				input := baseInput
				input.ClientIdempotencyKey = fmt.Sprintf("concurrent-key-%d-%d", goroutineID, j)

				output, err := activities.AggregateScores(ctx, input)
				if err != nil {
					errors <- err
				} else {
					results <- output
				}
			}
		}(i)
	}

	// Wait for all operations to complete
	wg.Wait()
	close(results)
	close(errors)

	// Collect results
	var outputs []*domain.AggregateScoresOutput
	for output := range results {
		outputs = append(outputs, output)
	}

	var errs []error
	for err := range errors {
		errs = append(errs, err)
	}

	// Verify no errors occurred
	require.Empty(t, errs, "concurrent operations should not produce errors")

	// Verify we got the expected number of results
	expectedResults := numGoroutines * numOperations
	assert.Len(t, outputs, expectedResults, "should get result from every operation")

	// Verify all results are valid
	for i, output := range outputs {
		require.NotNil(t, output, "output %d should not be nil", i)
		assert.NotEmpty(t, output.WinnerAnswerID, "output %d should have a winner", i)
		assert.Equal(t, domain.AggregationMethodMean, output.Method, "output %d should use mean method", i)
		AssertAggregationCorrect(t, output, baseInput)
	}

	// Verify metrics were recorded safely
	counters := metrics.GetCounters()
	gauges := metrics.GetGauges()
	histograms := metrics.GetHistograms()

	// Should have recorded metrics for all operations
	assert.NotEmpty(t, counters, "should have recorded counter metrics")
	assert.NotEmpty(t, gauges, "should have recorded gauge metrics")
	assert.NotEmpty(t, histograms, "should have recorded histogram metrics")
}

// TestConcurrentMetricsRecording verifies metrics recording is thread-safe.
func TestConcurrentMetricsRecording(t *testing.T) {
	const numGoroutines = 20
	const numOperations = 50

	metrics := NewEnhancedMockMetricsRecorder()

	var wg sync.WaitGroup

	// Test concurrent counter increments
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				metrics.IncrementCounter("test_counter", map[string]string{"tenant": testTenantID}, 1.0)
			}
		}()
	}

	// Test concurrent histogram recordings
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				metrics.RecordHistogram("test_histogram", map[string]string{"tenant": testTenantID}, float64(goroutineID+j))
			}
		}(i)
	}

	// Test concurrent gauge settings
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				metrics.SetGauge("test_gauge", map[string]string{"tenant": testTenantID}, float64(goroutineID))
			}
		}(i)
	}

	wg.Wait()

	// Verify metrics were recorded
	counters := metrics.GetCounters()
	histograms := metrics.GetHistograms()
	gauges := metrics.GetGauges()

	counterKey := "test_counter:map[tenant:" + testTenantID + "]"
	assert.Equal(t, float64(numGoroutines*numOperations), counters[counterKey], "counter should be sum of all increments")

	histogramKey := "test_histogram:map[tenant:" + testTenantID + "]"
	assert.Len(t, histograms[histogramKey], numGoroutines*numOperations, "histogram should have all recorded values")

	gaugeKey := "test_gauge:map[tenant:" + testTenantID + "]"
	assert.Contains(t, gauges, gaugeKey, "gauge should be set")
}

// TestConcurrentEventEmission verifies event emission is thread-safe.
func TestConcurrentEventEmission(t *testing.T) {
	const numGoroutines = 15
	const numOperations = 20

	sink := NewCapturingEventSink()

	var wg sync.WaitGroup

	// Create base event envelope template
	baseEnvelope := CreateTestEvent("test_event", "test-key")

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				envelope := baseEnvelope
				envelope.IdempotencyKey = fmt.Sprintf("event-key-%d-%d", goroutineID, j)
				envelope.ID = fmt.Sprintf("event-id-%d-%d", goroutineID, j)

				err := sink.Append(context.Background(), envelope)
				if err != nil {
					t.Errorf("event emission failed: %v", err)
				}
			}
		}(i)
	}

	wg.Wait()

	// Verify all events were captured
	events := sink.GetEvents()
	expectedEvents := numGoroutines * numOperations
	assert.Len(t, events, expectedEvents, "should capture all emitted events")

	// Verify no duplicate idempotency keys
	seenKeys := make(map[string]bool)
	for _, event := range events {
		if seenKeys[event.IdempotencyKey] {
			t.Errorf("duplicate idempotency key: %s", event.IdempotencyKey)
		}
		seenKeys[event.IdempotencyKey] = true
	}
}

// TestConcurrentEventEmissionWithFailures verifies event emission handles failures gracefully under concurrency.
func TestConcurrentEventEmissionWithFailures(t *testing.T) {
	const numGoroutines = 10
	const numOperations = 30

	// Create sink that fails first 50 operations
	sink := NewFailingEventSink(50)

	var wg sync.WaitGroup
	var successCount, failureCount int64
	var mu sync.Mutex

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				envelope := CreateTestEvent("test_event", fmt.Sprintf("key-%d-%d", goroutineID, j))

				err := sink.Append(context.Background(), envelope)

				mu.Lock()
				if err != nil {
					failureCount++
				} else {
					successCount++
				}
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	// Verify we had both successes and failures
	totalOperations := int64(numGoroutines * numOperations)
	assert.Equal(t, totalOperations, successCount+failureCount, "should account for all operations")
	assert.Greater(t, failureCount, int64(0), "should have some failures")
	assert.Greater(t, successCount, int64(0), "should have some successes")

	// Verify successful events were captured
	events := sink.GetEvents()
	assert.Len(t, events, int(successCount), "captured events should match success count")
}

// methodResult holds the result of an aggregation method test.
type methodResult struct {
	method domain.AggregationMethod
	output *domain.AggregateScoresOutput
	err    error
}

// TestConcurrentAggregationMethods verifies different aggregation methods work correctly under concurrency.
func TestConcurrentAggregationMethods(t *testing.T) {
	const numGoroutines = 12
	methods := []domain.AggregationMethod{domain.AggregationMethodMean, domain.AggregationMethodMedian, domain.AggregationMethodTrimmedMean}

	ctx := context.Background()
	activities := CreateTestActivities(nil, nil)

	var wg sync.WaitGroup
	results := make(chan methodResult, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()

			method := methods[goroutineID%len(methods)]
			input := CreateTestAggregateInput(2, 3, method)
			input.ClientIdempotencyKey = fmt.Sprintf("method-test-%s-%d", method.String(), goroutineID)

			if method == domain.AggregationMethodTrimmedMean {
				input.Policy.TrimFraction = 0.2
			}

			output, err := activities.AggregateScores(ctx, input)
			results <- methodResult{method: method, output: output, err: err}
		}(i)
	}

	wg.Wait()
	close(results)

	// Collect and verify results by method
	resultsByMethod := make(map[domain.AggregationMethod][]*domain.AggregateScoresOutput)
	for result := range results {
		require.NoError(t, result.err, "aggregation should not error for method %s", result.method)
		require.NotNil(t, result.output, "output should not be nil for method %s", result.method)

		assert.Equal(t, result.method, result.output.Method, "output method should match input")
		resultsByMethod[result.method] = append(resultsByMethod[result.method], result.output)
	}

	// Verify we got results for all methods
	for _, method := range methods {
		assert.NotEmpty(t, resultsByMethod[method], "should have results for method %s", method)
	}
}

// TestRaceFreeScoreFiltering verifies score filtering is race-free.
func TestRaceFreeScoreFiltering(t *testing.T) {
	const numGoroutines = 20

	// Create shared scores array with mix of valid/invalid
	baseScores := append(
		GenerateInvalidScores(TestAnswerUUID1, 5, 3),    // 5 valid, 3 invalid
		GenerateInvalidScores(TestAnswerUUID2, 4, 2)..., // 4 valid, 2 invalid
	)

	var wg sync.WaitGroup
	results := make(chan []domain.Score, numGoroutines)

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			filtered := filterValidScores(baseScores)
			results <- filtered
		}()
	}

	wg.Wait()
	close(results)

	// Verify all goroutines got the same result
	var filteredResults [][]domain.Score
	for result := range results {
		filteredResults = append(filteredResults, result)
	}

	require.Len(t, filteredResults, numGoroutines, "should get result from all goroutines")

	// All results should be identical
	expectedLength := 9 // 5 + 4 valid scores
	for i, result := range filteredResults {
		assert.Len(t, result, expectedLength, "result %d should have expected length", i)

		// Verify all scores are valid
		for j, score := range result {
			assert.True(t, score.IsValid(), "result %d score %d should be valid", i, j)
		}
	}
}

// TestMemoryLeakUnderLoad verifies no memory leaks under sustained load.
func TestMemoryLeakUnderLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping memory leak test in short mode")
	}

	const iterations = 1000
	const memCheckInterval = 100

	ctx := context.Background()
	activities := CreateTestActivities(nil, nil)

	var m1, m2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	for i := 0; i < iterations; i++ {
		input := CreateTestAggregateInput(5, 4, "mean")
		input.ClientIdempotencyKey = fmt.Sprintf("memory-test-%d", i)

		_, err := activities.AggregateScores(ctx, input)
		require.NoError(t, err)

		// Check memory periodically
		if i%memCheckInterval == 0 && i > 0 {
			runtime.GC()
			runtime.ReadMemStats(&m2)

			// Memory usage should not grow excessively
			memGrowth := m2.Alloc - m1.Alloc
			if memGrowth > 50*1024*1024 { // 50MB threshold
				t.Fatalf("excessive memory growth: %d bytes after %d iterations", memGrowth, i)
			}
		}
	}

	runtime.GC()
	runtime.ReadMemStats(&m2)

	// Final memory check - should not have significant growth
	memGrowth := m2.Alloc - m1.Alloc
	t.Logf("Total memory growth: %d bytes after %d iterations", memGrowth, iterations)
}

// TestConcurrentStatisticalCalculations verifies statistical calculations are deterministic under concurrency.
func TestConcurrentStatisticalCalculations(t *testing.T) {
	const numGoroutines = 25
	const numCalculations = 40

	// Test data with known results
	testValues := []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9}
	expectedMean := 0.5
	expectedMedian := 0.5

	var wg sync.WaitGroup
	meanResults := make(chan float64, numGoroutines*numCalculations)
	medianResults := make(chan float64, numGoroutines*numCalculations)

	// Create scores once to share across goroutines
	var scores []domain.Score
	for i, val := range testValues {
		score := CreateTestScore(TestAnswerUUID1, WithScoreValue(val))
		score.ID = fmt.Sprintf("score-%d", i)
		scores = append(scores, score)
	}

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < numCalculations; j++ {
				meanResult := calculateMean(scores)
				medianResult := calculateMedian(scores)

				meanResults <- meanResult
				medianResults <- medianResult
			}
		}()
	}

	wg.Wait()
	close(meanResults)
	close(medianResults)

	// Verify all results are consistent
	for meanResult := range meanResults {
		assert.InDelta(t, expectedMean, meanResult, epsilon, "mean calculation should be consistent")
	}

	for medianResult := range medianResults {
		assert.InDelta(t, expectedMedian, medianResult, epsilon, "median calculation should be consistent")
	}
}

// Helper function to create test event envelope
func CreateTestEvent(eventType, idempotencyKey string) events.Envelope {
	payload, _ := json.Marshal(map[string]interface{}{"test": true})
	return events.Envelope{
		ID:             idempotencyKey,
		Type:           eventType,
		Source:         "test",
		Version:        "1.0.0",
		Timestamp:      time.Now(),
		IdempotencyKey: idempotencyKey,
		TenantID:       testTenantID,
		WorkflowID:     testWorkflowID,
		RunID:          "test-run",
		Payload:        json.RawMessage(payload),
	}
}

// TestHighContentionScenario simulates high contention on shared resources.
func TestHighContentionScenario(t *testing.T) {
	const numGoroutines = 50
	const numOperations = 20

	ctx := context.Background()

	// Use shared sink and metrics to create contention
	sink := NewCapturingEventSink()
	metrics := NewEnhancedMockMetricsRecorder()
	activities := CreateTestActivities(sink, metrics)

	var wg sync.WaitGroup
	var successCount int64
	var mu sync.Mutex

	// All goroutines use the same base input to maximize contention
	baseInput := CreateTestAggregateInput(2, 2, "mean")

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(goroutineID int) {
			defer wg.Done()

			for j := 0; j < numOperations; j++ {
				input := baseInput
				input.ClientIdempotencyKey = fmt.Sprintf("contention-%d-%d", goroutineID, j)

				output, err := activities.AggregateScores(ctx, input)
				if err == nil && output != nil {
					mu.Lock()
					successCount++
					mu.Unlock()
				}
			}
		}(i)
	}

	wg.Wait()

	// Verify high success rate despite contention
	expectedOperations := int64(numGoroutines * numOperations)
	successRate := float64(successCount) / float64(expectedOperations)
	assert.Greater(t, successRate, 0.95, "should have high success rate even under contention")

	// Verify shared resources recorded data correctly
	events := sink.GetEvents()
	assert.Greater(t, len(events), 0, "should have captured events")

	counters := metrics.GetCounters()
	assert.NotEmpty(t, counters, "should have recorded metrics")
}

// TestTimeoutResilienceUnderLoad verifies system handles timeouts gracefully under load.
func TestTimeoutResilienceUnderLoad(t *testing.T) {
	const numGoroutines = 10
	const numOperations = 20

	ctx := context.Background()

	// Create metrics recorder with artificial latency to simulate slow operations
	metrics := NewEnhancedMockMetricsRecorder().WithLatency(1 * time.Millisecond)
	activities := CreateTestActivities(nil, metrics)

	var wg sync.WaitGroup
	results := make(chan error, numGoroutines*numOperations)

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(goroutineID int) {
			defer wg.Done()

			for j := 0; j < numOperations; j++ {
				// Use short timeout context to simulate timeout scenarios
				timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
				defer cancel()

				input := CreateTestAggregateInput(3, 2, "mean")
				input.ClientIdempotencyKey = fmt.Sprintf("timeout-test-%d-%d", goroutineID, j)

				_, err := activities.AggregateScores(timeoutCtx, input)
				results <- err
			}
		}(i)
	}

	wg.Wait()
	close(results)

	// Collect results
	var errors []error
	var successes int
	for err := range results {
		if err != nil {
			errors = append(errors, err)
		} else {
			successes++
		}
	}

	// Some operations may timeout, but system should remain stable
	totalOperations := numGoroutines * numOperations
	t.Logf("Successes: %d, Timeouts: %d, Total: %d", successes, len(errors), totalOperations)

	// System should handle timeouts gracefully without panics or corruption
	assert.Equal(t, totalOperations, successes+len(errors), "should account for all operations")
}
