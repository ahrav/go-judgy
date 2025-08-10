// Package resilience provides components for building robust and reliable LLM
// clients. It includes implementations for common resilience patterns such as
// retries, circuit breakers, rate limiting, and caching. It also provides
// observability tools like logging and metrics middleware to monitor the health
// and performance of LLM interactions.
package resilience

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/configuration"
	llmerrors "github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/transport"
	"github.com/google/uuid"
)

// Logging constants define limits and settings for observability.
const (
	// ContentTruncationLimit specifies the maximum number of characters
	// to include from a response content in logs before truncating.
	ContentTruncationLimit = 200
)

// Metrics provides an interface for collecting observability data from LLM
// operations. It supports counters, histograms, and gauges with tag-based
// dimensionality to enable monitoring, alerting, and performance analysis
// across different providers.
type Metrics interface {
	// IncrementCounter increases a counter metric by a given value.
	// Counters are suitable for tracking cumulative values, such as the total
	// number of requests or errors.
	IncrementCounter(name string, tags map[string]string, value float64)
	// RecordHistogram records a value in a histogram metric.
	// Histograms are used to track the distribution of a set of values,
	// such as request latencies or token counts.
	RecordHistogram(name string, tags map[string]string, value float64)
	// SetGauge sets a gauge metric to a specific value.
	// Gauges represent a value that can go up or down, such as the number
	// of active requests or the current cache hit rate.
	SetGauge(name string, tags map[string]string, value float64)
}

// NoOpMetrics provides a no-op implementation of the Metrics interface.
// It is used in environments like testing or development where metrics
// collection is not needed, allowing observability code to run without
// performance overhead.
type NoOpMetrics struct{}

// NewNoOpMetrics returns a new no-op metrics collector.
// This implementation of the Metrics interface discards all data, making it
// suitable for environments where observability is disabled.
func NewNoOpMetrics() *NoOpMetrics {
	return &NoOpMetrics{}
}

// IncrementCounter is a no-op implementation of the IncrementCounter method.
// It performs no action and discards all input.
func (n *NoOpMetrics) IncrementCounter(_ string, _ map[string]string, _ float64) {}

// RecordHistogram is a no-op implementation of the RecordHistogram method.
// It performs no action and discards all input.
func (n *NoOpMetrics) RecordHistogram(_ string, _ map[string]string, _ float64) {}

// SetGauge is a no-op implementation of the SetGauge method.
// It performs no action and discards all input.
func (n *NoOpMetrics) SetGauge(_ string, _ map[string]string, _ float64) {}

// LoggingMiddleware provides comprehensive observability for the LLM request
// lifecycle. It captures structured logs, metrics, and performance data.
// It also supports configurable redaction for sensitive content and request
// tracing for debugging purposes.
type LoggingMiddleware struct {
	logger        *slog.Logger
	metrics       Metrics
	config        configuration.ObservabilityConfig
	redactPrompts bool
}

// NewLoggingMiddleware creates a transport.Middleware for observability.
// It provides comprehensive request and response logging, error classification,
// and metrics collection. The middleware supports configurable redaction and
// uses default fallbacks for the logger and metrics client if they are nil.
func NewLoggingMiddleware(config configuration.ObservabilityConfig, logger *slog.Logger, metrics Metrics) (transport.Middleware, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if metrics == nil {
		metrics = NewNoOpMetrics()
	}

	lm := &LoggingMiddleware{
		logger:        logger,
		metrics:       metrics,
		config:        config,
		redactPrompts: config.RedactPrompts,
	}

	return lm.Middleware(), nil
}

// Middleware returns a transport.Middleware that wraps an LLM handler with
// comprehensive observability features. It captures request start and completion
// events, measures latency, records usage metrics, and provides error
// classification for monitoring and alerting.
func (m *LoggingMiddleware) Middleware() transport.Middleware {
	return func(next transport.Handler) transport.Handler {
		return transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
			requestID := req.TraceID
			if requestID == "" {
				requestID = uuid.New().String()
			}

			baseTags := map[string]string{
				"provider":  req.Provider,
				"model":     req.Model,
				"operation": string(req.Operation),
				"tenant":    req.TenantID,
			}

			m.logRequest(ctx, req, requestID)

			m.metrics.IncrementCounter("llm.requests.total", baseTags, 1)

			start := time.Now()
			resp, err := next.Handle(ctx, req)
			duration := time.Since(start)

			m.metrics.RecordHistogram("llm.request.duration_ms", baseTags, float64(duration.Milliseconds()))

			if err != nil {
				m.handleError(ctx, req, err, requestID, duration, baseTags)
			} else if resp != nil {
				m.handleSuccess(ctx, req, resp, requestID, duration, baseTags)
			}

			return resp, err
		})
	}
}

// logRequest captures and logs structured request data.
// It includes provider, model, and operation details, and redacts sensitive
// prompt content based on the middleware's configuration to ensure compliance
// and security.
func (m *LoggingMiddleware) logRequest(_ context.Context, req *transport.Request, requestID string) {
	fields := []any{
		"request_id", requestID,
		"provider", req.Provider,
		"model", req.Model,
		"operation", req.Operation,
		"tenant_id", req.TenantID,
		"max_tokens", req.MaxTokens,
		"temperature", req.Temperature,
		"timeout_seconds", req.Timeout.Seconds(),
	}

	if req.Operation == transport.OpGeneration {
		if m.redactPrompts {
			fields = append(fields, "question_length", len(req.Question))
		} else {
			fields = append(fields, "question", req.Question)
		}
	}

	if req.Operation == transport.OpScoring {
		fields = append(fields, "answers_count", len(req.Answers))
		if len(req.Answers) > 0 {
			fields = append(fields, "answer_ids", m.extractAnswerIDs(req.Answers))
		}
	}

	if req.SystemPrompt != "" {
		if m.redactPrompts {
			fields = append(fields, "system_prompt_length", len(req.SystemPrompt))
		} else {
			fields = append(fields, "system_prompt", req.SystemPrompt)
		}
	}

	m.logger.Info("LLM request started", fields...)
}

// handleError processes and logs failures in LLM operations.
// It logs detailed error context, records error metrics classified by type,
// and provides structured data for error analysis and alerting systems.
func (m *LoggingMiddleware) handleError(
	_ context.Context,
	req *transport.Request,
	err error,
	requestID string,
	duration time.Duration,
	baseTags map[string]string,
) {
	errorType := "unknown"
	if wfErr := llmerrors.ClassifyLLMError(err); wfErr != nil {
		errorType = string(wfErr.Type)
	}

	errorTags := copyTags(baseTags)
	errorTags["error_type"] = errorType

	m.metrics.IncrementCounter("llm.requests.errors", errorTags, 1)

	fields := []any{
		"request_id", requestID,
		"provider", req.Provider,
		"model", req.Model,
		"operation", req.Operation,
		"tenant_id", req.TenantID,
		"duration_ms", duration.Milliseconds(),
		"error_type", errorType,
		"error", err.Error(),
	}

	m.logger.Error("LLM request failed", fields...)
}

// handleSuccess processes and logs successful LLM responses.
// It logs response details, records token usage and cost metrics, and supports
// configurable content redaction to balance observability with data protection.
func (m *LoggingMiddleware) handleSuccess(
	_ context.Context,
	req *transport.Request,
	resp *transport.Response,
	requestID string,
	duration time.Duration,
	baseTags map[string]string,
) {
	m.metrics.IncrementCounter("llm.requests.success", baseTags, 1)

	tokenTags := copyTags(baseTags)
	m.metrics.RecordHistogram("llm.tokens.prompt", tokenTags, float64(resp.Usage.PromptTokens))
	m.metrics.RecordHistogram("llm.tokens.completion", tokenTags, float64(resp.Usage.CompletionTokens))
	m.metrics.RecordHistogram("llm.tokens.total", tokenTags, float64(resp.Usage.TotalTokens))

	if resp.EstimatedCostMilliCents > 0 {
		m.metrics.RecordHistogram("llm.cost.milli_cents", baseTags, float64(resp.EstimatedCostMilliCents))
	}

	fields := []any{
		"request_id", requestID,
		"provider", req.Provider,
		"model", req.Model,
		"operation", req.Operation,
		"tenant_id", req.TenantID,
		"duration_ms", duration.Milliseconds(),
		"finish_reason", resp.FinishReason,
		"prompt_tokens", resp.Usage.PromptTokens,
		"completion_tokens", resp.Usage.CompletionTokens,
		"total_tokens", resp.Usage.TotalTokens,
		"cost_milli_cents", resp.EstimatedCostMilliCents,
		"provider_request_ids", strings.Join(resp.ProviderRequestIDs, ","),
	}

	if m.redactPrompts {
		fields = append(fields, "response_length", len(resp.Content))
	} else {
		content := resp.Content
		if len(content) > ContentTruncationLimit {
			content = content[:ContentTruncationLimit] + "..."
		}
		fields = append(fields, "response_preview", content)
	}

	m.logger.Info("LLM request completed", fields...)
}

// extractAnswerIDs extracts a slice of answer identifiers for secure logging.
// This provides traceability for answers in scoring operations without exposing
// sensitive answer content in logs.
func (m *LoggingMiddleware) extractAnswerIDs(answers []domain.Answer) []string {
	ids := make([]string, len(answers))
	for i, answer := range answers {
		ids[i] = answer.ID
	}
	return ids
}

// copyTags creates an immutable copy of a metric tag map.
// This prevents tag mutation between different metric calls, ensuring
// consistent tagging and avoiding unintended metric correlation.
func copyTags(original map[string]string) map[string]string {
	tagsCopy := make(map[string]string, len(original))
	for k, v := range original {
		tagsCopy[k] = v
	}
	return tagsCopy
}

// ObservabilityStats provides a snapshot of comprehensive observability statistics.
// It is used to report the state of LLM operations over a period.
type ObservabilityStats struct {
	// RequestsTotal is the total number of requests processed.
	RequestsTotal int64 `json:"requests_total"`
	// RequestsSuccess is the total number of successfully processed requests.
	RequestsSuccess int64 `json:"requests_success"`
	// RequestsError is the total number of failed requests.
	RequestsError int64 `json:"requests_error"`
	// ErrorsByType maps error types to the count of their occurrences.
	ErrorsByType map[string]int64 `json:"errors_by_type"`
	// AverageLatencyMs is the average request latency in milliseconds.
	AverageLatencyMs float64 `json:"average_latency_ms"`
	// TotalTokens is the cumulative number of tokens processed across all requests.
	TotalTokens int64 `json:"total_tokens"`
	// TotalCostCents is the estimated total cost of all requests in cents.
	TotalCostCents int64 `json:"total_cost_cents"`
	// RequestsByStatus maps finish reasons to the count of their occurrences.
	RequestsByStatus map[string]int64 `json:"requests_by_status"`
}

// observabilityMiddleware tracks observability metrics internally.
type observabilityMiddleware struct {
	stats *ObservabilityStats
}

// NewObservabilityMiddleware creates a middleware that tracks observability
// metrics internally. It provides a simple, in-memory way to monitor
// LLM operations without external dependencies.
func NewObservabilityMiddleware() (transport.Middleware, error) {
	om := &observabilityMiddleware{
		stats: &ObservabilityStats{
			ErrorsByType:     make(map[string]int64),
			RequestsByStatus: make(map[string]int64),
		},
	}

	return om.middleware(), nil
}

// middleware returns the observability middleware function.
func (o *observabilityMiddleware) middleware() transport.Middleware {
	return func(next transport.Handler) transport.Handler {
		return transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
			start := time.Now()
			resp, err := next.Handle(ctx, req)
			duration := time.Since(start)

			// Update basic request metrics
			o.stats.RequestsTotal++

			if err != nil {
				o.stats.RequestsError++

				// Classify error type
				errorType := "unknown"
				if wfErr := llmerrors.ClassifyLLMError(err); wfErr != nil {
					errorType = string(wfErr.Type)
				}
				o.stats.ErrorsByType[errorType]++
			} else {
				o.stats.RequestsSuccess++

				if resp != nil {
					// Track token usage
					o.stats.TotalTokens += resp.Usage.TotalTokens

					// Track costs (convert from milli-cents to cents)
					o.stats.TotalCostCents += resp.EstimatedCostMilliCents / 10

					// Track by finish reason
					status := string(resp.FinishReason)
					o.stats.RequestsByStatus[status]++
				}
			}

			// Update average latency (simple moving average approximation)
			if o.stats.RequestsTotal > 0 {
				currentAvg := o.stats.AverageLatencyMs
				newLatency := float64(duration.Milliseconds())
				totalRequests := float64(o.stats.RequestsTotal)
				o.stats.AverageLatencyMs = (currentAvg*(totalRequests-1) + newLatency) / totalRequests
			}

			return resp, err
		})
	}
}

// GetStats returns a copy of the current observability statistics.
// The returned struct is a snapshot and is safe for concurrent access, as it
// does not share memory with the internal state of the middleware.
func (o *observabilityMiddleware) GetStats() *ObservabilityStats {
	// Return a copy to prevent external mutation
	statsCopy := &ObservabilityStats{
		RequestsTotal:    o.stats.RequestsTotal,
		RequestsSuccess:  o.stats.RequestsSuccess,
		RequestsError:    o.stats.RequestsError,
		AverageLatencyMs: o.stats.AverageLatencyMs,
		TotalTokens:      o.stats.TotalTokens,
		TotalCostCents:   o.stats.TotalCostCents,
		ErrorsByType:     make(map[string]int64),
		RequestsByStatus: make(map[string]int64),
	}

	// Deep copy maps
	for k, v := range o.stats.ErrorsByType {
		statsCopy.ErrorsByType[k] = v
	}
	for k, v := range o.stats.RequestsByStatus {
		statsCopy.RequestsByStatus[k] = v
	}

	return statsCopy
}
