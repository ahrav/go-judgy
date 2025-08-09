package llm_test

import (
	"context"
	"testing"
	"time"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm"
)

// MockHandler provides predictable responses for testing Handler interface.
type MockHandler struct {
	response *llm.LLMResponse
	err      error
}

func NewMockHandler(response *llm.LLMResponse, err error) *MockHandler {
	return &MockHandler{
		response: response,
		err:      err,
	}
}

func (m *MockHandler) Handle(_ context.Context, req *llm.Request) (*llm.LLMResponse, error) {
	return m.response, m.err
}

func TestHandler_Interface(t *testing.T) {
	ctx := context.Background()

	expectedResponse := &llm.LLMResponse{
		Content:      "test response",
		FinishReason: domain.FinishStop,
		Usage: llm.NormalizedUsage{
			PromptTokens:     10,
			CompletionTokens: 20,
			TotalTokens:      30,
			LatencyMs:        100,
		},
		EstimatedCostMilliCents: 150,
	}

	handler := NewMockHandler(expectedResponse, nil)

	req := &llm.Request{
		Operation:   llm.OpGeneration,
		Provider:    "openai",
		Model:       "gpt-4",
		Question:    "test question",
		MaxTokens:   100,
		Temperature: 0.7,
		Timeout:     30 * time.Second,
	}

	// Test that Handler.Handle returns LLMResponse (not Request)
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

	expectedResponse := &llm.LLMResponse{
		Content:      "handlerfunc response",
		FinishReason: domain.FinishStop,
		Usage: llm.NormalizedUsage{
			TotalTokens: 25,
			LatencyMs:   50,
		},
	}

	// Test HandlerFunc implements Handler interface correctly
	handlerFunc := llm.HandlerFunc(func(_ context.Context, req *llm.Request) (*llm.LLMResponse, error) {
		return expectedResponse, nil
	})

	req := &llm.Request{
		Operation: llm.OpScoring,
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

	baseResponse := &llm.LLMResponse{
		Content:      "base response",
		FinishReason: domain.FinishStop,
		Usage:        llm.NormalizedUsage{TotalTokens: 10},
	}

	baseHandler := NewMockHandler(baseResponse, nil)

	// Create middleware that modifies the response
	testMiddleware := func(next llm.Handler) llm.Handler {
		return llm.HandlerFunc(func(_ context.Context, req *llm.Request) (*llm.LLMResponse, error) {
			resp, err := next.Handle(ctx, req)
			if err != nil {
				return nil, err
			}

			// Modify response to demonstrate middleware execution
			resp.Content = "modified by middleware"
			return resp, nil
		})
	}

	chainedHandler := llm.Chain(baseHandler, testMiddleware)

	req := &llm.Request{
		Operation: llm.OpGeneration,
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
