package transport_test

import (
	"context"
	"testing"
	"time"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// MockHandler provides predictable responses for testing Handler interface.
type MockHandler struct {
	response *transport.Response
	err      error
}

func NewMockHandler(response *transport.Response, err error) *MockHandler {
	return &MockHandler{
		response: response,
		err:      err,
	}
}

func (m *MockHandler) Handle(_ context.Context, _ *transport.Request) (*transport.Response, error) {
	return m.response, m.err
}

func TestHandler_Interface(t *testing.T) {
	ctx := context.Background()

	expectedResponse := &transport.Response{
		Content:      "test response",
		FinishReason: domain.FinishStop,
		Usage: transport.NormalizedUsage{
			PromptTokens:     10,
			CompletionTokens: 20,
			TotalTokens:      30,
			LatencyMs:        100,
		},
		EstimatedCostMilliCents: 150,
	}

	handler := NewMockHandler(expectedResponse, nil)

	req := &transport.Request{
		Operation:   transport.OpGeneration,
		Provider:    "openai",
		Model:       "gpt-4",
		Question:    "test question",
		MaxTokens:   100,
		Temperature: 0.7,
		Timeout:     30 * time.Second,
	}

	// Test that Handler.Handle returns Response (not Request)
	resp, err := handler.Handle(ctx, req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if resp == nil {
		t.Fatal("expected response, got nil")
	}

	// Verify response structure contains response data
	if resp.Content != expectedResponse.Content {
		t.Errorf("expected content %q, got %q", expectedResponse.Content, resp.Content)
	}

	if resp.FinishReason != expectedResponse.FinishReason {
		t.Errorf("expected finish reason %v, got %v", expectedResponse.FinishReason, resp.FinishReason)
	}

	if resp.Usage.TotalTokens != expectedResponse.Usage.TotalTokens {
		t.Errorf("expected total tokens %d, got %d", expectedResponse.Usage.TotalTokens, resp.Usage.TotalTokens)
	}
}

func TestHandlerFunc_Interface(t *testing.T) {
	ctx := context.Background()

	expectedResponse := &transport.Response{
		Content:      "handlerfunc response",
		FinishReason: domain.FinishStop,
		Usage: transport.NormalizedUsage{
			TotalTokens: 25,
			LatencyMs:   50,
		},
	}

	// Test HandlerFunc implements Handler interface correctly
	handlerFunc := transport.HandlerFunc(func(_ context.Context, _ *transport.Request) (*transport.Response, error) {
		return expectedResponse, nil
	})

	req := &transport.Request{
		Operation: transport.OpScoring,
		Provider:  "anthropic",
		Model:     "claude-3-sonnet",
	}

	resp, err := handlerFunc.Handle(ctx, req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if resp.Content != expectedResponse.Content {
		t.Errorf("expected content %q, got %q", expectedResponse.Content, resp.Content)
	}
}

func TestChain_MiddlewareExecution(t *testing.T) {
	ctx := context.Background()

	baseResponse := &transport.Response{
		Content:      "base response",
		FinishReason: domain.FinishStop,
		Usage:        transport.NormalizedUsage{TotalTokens: 10},
	}

	baseHandler := NewMockHandler(baseResponse, nil)

	// Create middleware that modifies the response
	testMiddleware := func(next transport.Handler) transport.Handler {
		return transport.HandlerFunc(func(_ context.Context, req *transport.Request) (*transport.Response, error) {
			resp, err := next.Handle(ctx, req)
			if err != nil {
				return nil, err
			}

			// Modify response to demonstrate middleware execution
			resp.Content = "modified by middleware"
			return resp, nil
		})
	}

	chainedHandler := transport.Chain(baseHandler, testMiddleware)

	req := &transport.Request{
		Operation: transport.OpGeneration,
		Provider:  "openai",
		Model:     "gpt-4",
	}

	resp, err := chainedHandler.Handle(ctx, req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if resp.Content != "modified by middleware" {
		t.Errorf("expected middleware to modify content, got %q", resp.Content)
	}
}
