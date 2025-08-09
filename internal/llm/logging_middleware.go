package llm

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ahrav/go-judgy/internal/domain"
)

// Metrics provides observability data collection for LLM operations.
// Supports counters, histograms, and gauges with tag-based dimensionality
// to enable monitoring, alerting, and performance analysis across providers.
type Metrics interface {
	IncrementCounter(name string, tags map[string]string, value float64)
	RecordHistogram(name string, tags map[string]string, value float64)
	SetGauge(name string, tags map[string]string, value float64)
}

// NoOpMetrics provides a no-op metrics implementation for testing and development.
// Satisfies the Metrics interface without actual data collection to enable
// testing scenarios where metrics overhead should be eliminated.
type NoOpMetrics struct{}

// NewNoOpMetrics creates a no-op metrics collector for testing environments.
// Returns a metrics implementation that discards all data to enable
// performance testing without observability overhead.
func NewNoOpMetrics() *NoOpMetrics {
	return &NoOpMetrics{}
}

func (n *NoOpMetrics) IncrementCounter(_ string, _ map[string]string, _ float64) {}

func (n *NoOpMetrics) RecordHistogram(_ string, _ map[string]string, _ float64) {}

func (n *NoOpMetrics) SetGauge(_ string, _ map[string]string, _ float64) {}

// LoggingMiddleware provides comprehensive observability for LLM request lifecycle.
// Captures structured logs, metrics, and performance data with configurable
// redaction for sensitive content and request tracing for debugging.
type LoggingMiddleware struct {
	logger        *slog.Logger
	metrics       Metrics
	config        ObservabilityConfig
	redactPrompts bool
}

// NewLoggingMiddleware creates observability middleware with structured logging.
// Provides comprehensive request/response logging, error classification, and
// metrics collection with configurable redaction and fallback defaults.
func NewLoggingMiddleware(config ObservabilityConfig, logger *slog.Logger, metrics Metrics) Middleware {
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

	return lm.Middleware
}

// Middleware wraps LLM handlers with comprehensive observability.
// Captures request start/completion events, measures latency, records usage metrics,
// and provides error classification for monitoring and alerting.
func (m *LoggingMiddleware) Middleware(next Handler) Handler {
	return HandlerFunc(func(ctx context.Context, req *LLMRequest) (*Request, error) {
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

// logRequest captures structured request data with configurable content redaction.
// Logs provider, model, operation details while protecting sensitive prompts
// based on redaction configuration for compliance and security.
func (m *LoggingMiddleware) logRequest(ctx context.Context, req *LLMRequest, requestID string) {
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

	if req.Operation == OpGeneration {
		if m.redactPrompts {
			fields = append(fields, "question_length", len(req.Question))
		} else {
			fields = append(fields, "question", req.Question)
		}
	}

	if req.Operation == OpScoring {
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

// handleError processes LLM operation failures with error classification.
// Logs detailed error context, records error metrics by type, and provides
// structured data for error analysis and alerting systems.
func (m *LoggingMiddleware) handleError(
	ctx context.Context,
	req *LLMRequest,
	err error,
	requestID string,
	duration time.Duration,
	baseTags map[string]string,
) {
	errorType := "unknown"
	if wfErr := ClassifyLLMError(err); wfErr != nil {
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

// handleSuccess processes successful LLM responses with comprehensive metrics.
// Logs response details, records token usage and cost metrics, with configurable
// content redaction to balance observability and data protection.
func (m *LoggingMiddleware) handleSuccess(
	ctx context.Context,
	req *LLMRequest,
	resp *Request,
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
		if len(content) > 200 {
			content = content[:200] + "..."
		}
		fields = append(fields, "response_preview", content)
	}

	m.logger.Info("LLM request completed", fields...)
}

// extractAnswerIDs extracts answer identifiers for secure logging.
// Provides answer traceability in scoring operations without exposing
// sensitive answer content to logging systems.
func (m *LoggingMiddleware) extractAnswerIDs(answers []domain.Answer) []string {
	ids := make([]string, len(answers))
	for i, answer := range answers {
		ids[i] = answer.ID
	}
	return ids
}

// copyTags creates immutable copies of metric tag maps.
// Prevents tag mutation between different metric calls to ensure
// consistent tagging and avoid unintended metric correlation.
func copyTags(original map[string]string) map[string]string {
	tagsCopy := make(map[string]string, len(original))
	for k, v := range original {
		tagsCopy[k] = v
	}
	return tagsCopy
}
