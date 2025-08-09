// Package llm provides a unified, resilient HTTP client for Large Language Model providers.
package llm

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Handler processes LLM requests through composable middleware pipeline.
// Core abstraction enabling request preprocessing, response postprocessing,
// and cross-cutting concerns like caching, rate limiting, and observability.
type Handler interface {
	Handle(ctx context.Context, req *Request) (*LLMResponse, error)
}

// HandlerFunc adapts a function to the Handler interface.
// Enables middleware composition with function-based handlers.
type HandlerFunc func(context.Context, *Request) (*LLMResponse, error)

// Handle implements the Handler interface.
func (f HandlerFunc) Handle(ctx context.Context, req *Request) (*LLMResponse, error) {
	return f(ctx, req)
}

// Middleware transforms Handler into enhanced Handler for composable behavior.
// Applied in reverse order with last middleware closest to core handler,
// enabling layered request processing and response transformation.
type Middleware func(Handler) Handler

// Chain builds a middleware pipeline around a core handler.
// Middleware executes in the order provided with first middleware outermost,
// enabling request preprocessing and response postprocessing in proper order.
func Chain(h Handler, middlewares ...Middleware) Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}

// httpHandler is the core handler that makes actual HTTP requests.
type httpHandler struct {
	client *http.Client
	router Router
}

// Handle implements Handler by making HTTP requests to providers.
func (h *httpHandler) Handle(ctx context.Context, req *Request) (*LLMResponse, error) {
	// Select provider adapter.
	adapter, err := h.router.Pick(req.Provider, req.Model)
	if err != nil {
		return nil, fmt.Errorf("failed to select provider: %w", err)
	}

	// Create context with per-request timeout if specified.
	reqCtx := ctx
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	// Build HTTP request with per-request timeout context.
	httpReq, err := adapter.Build(reqCtx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}

	start := time.Now()
	httpResp, err := h.client.Do(httpReq)
	latency := time.Since(start)

	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer func() {
		if closeErr := httpResp.Body.Close(); closeErr != nil {
			// Log the error but don't return it since we're in a defer
			// In production, this would be logged to observability
			_ = closeErr
		}
	}()

	resp, err := adapter.Parse(httpResp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	resp.Usage.LatencyMs = latency.Milliseconds()

	if err := ValidateProviderResponse(resp); err != nil {
		return nil, fmt.Errorf("invalid provider response: %w", err)
	}

	if req.Operation == OpGeneration {
		if err := ValidateGenerationResponse(resp.Content); err != nil {
			return nil, fmt.Errorf("invalid generation response: %w", err)
		}
	}

	return resp, nil
}
