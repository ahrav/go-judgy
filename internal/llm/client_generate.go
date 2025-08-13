package llm

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ahrav/go-judgy/internal/domain"
	"github.com/ahrav/go-judgy/internal/llm/configuration"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// baseRequest returns a value (not pointer) you can copy per attempt safely.
func (c *client) baseRequest(ctx context.Context, in domain.GenerateAnswersInput) transport.Request {
	return transport.Request{
		Operation:   transport.OpGeneration,
		Provider:    in.Config.Provider,
		Model:       in.Config.Model,
		TenantID:    transport.ExtractTenantID(ctx),
		Question:    in.Question,
		MaxTokens:   in.Config.MaxAnswerTokens,
		Temperature: in.Config.Temperature,
		Timeout:     time.Duration(in.Config.Timeout) * time.Second,
		TraceID:     transport.ExtractTraceID(ctx),
		// ArtifactStore assigned per-request to avoid sharing across goroutines.
	}
}

// Generate implements Client.Generate with bounded concurrency and no code duplication.
func (c *client) Generate(
	ctx context.Context, in domain.GenerateAnswersInput,
) (*domain.GenerateAnswersOutput, error) {
	out := &domain.GenerateAnswersOutput{
		Answers: make([]domain.Answer, 0, in.NumAnswers),
	}

	// Canonical idempotency key for the logical request.
	canonicalReq := &transport.Request{
		Operation:   transport.OpGeneration,
		Provider:    in.Config.Provider,
		Model:       in.Config.Model,
		TenantID:    transport.ExtractTenantID(ctx),
		Question:    in.Question,
		MaxTokens:   in.Config.MaxAnswerTokens,
		Temperature: in.Config.Temperature,
	}
	canonicalKey, err := transport.GenerateIdemKey(canonicalReq)
	if err != nil {
		out.Error = fmt.Sprintf("failed to generate canonical idempotency key: %v", err)
		return out, nil
	}
	out.ClientIdemKey = canonicalKey.String()

	n := in.NumAnswers
	if n <= 0 {
		out.Error = "no answers requested"
		return out, nil
	}

	// Concurrency cap.
	maxConc := c.config.MaxConcurrency
	if maxConc <= 0 {
		maxConc = configuration.DefaultMaxConcurrency
	}
	if maxConc > n {
		maxConc = n
	}

	base := c.baseRequest(ctx, in) // by-value

	type res struct {
		ans    *domain.Answer
		tokens int64
		err    error
	}
	results := make([]res, n)

	sem := make(chan struct{}, maxConc)
	var wg sync.WaitGroup
	wg.Add(n)

	for i := range n {
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			// Copy request to prevent data races across goroutines.
			req := base
			req.IdempotencyKey = fmt.Sprintf("%s:answer:%d", canonicalKey, i)
			// Create adapter per request for thread safety.
			req.ArtifactStore = newArtifactStoreAdapter(c.artifactStore)

			resp, err := c.handler.Handle(ctx, &req)
			if err != nil {
				results[i] = res{err: err}
				return
			}
			ans := transport.ResponseToAnswer(resp, &req)
			results[i] = res{
				ans:    ans,
				tokens: resp.Usage.TotalTokens,
			}
		}()
	}

	wg.Wait()

	// Aggregate results maintaining original answer order.
	var (
		firstErr error
		tokens   int64
		calls    int64
	)
	for i := range n {
		r := results[i]
		if r.err != nil && firstErr == nil {
			firstErr = fmt.Errorf("failed to generate answer %d: %w", i+1, r.err)
			continue
		}
		if r.ans != nil {
			out.Answers = append(out.Answers, *r.ans)
			tokens += r.tokens
			calls++
		}
	}
	out.TokensUsed = tokens
	out.CallsMade = calls

	if firstErr != nil {
		out.Error = firstErr.Error()
	}
	if len(out.Answers) == 0 && out.Error == "" {
		out.Error = "no answers generated"
	}

	return out, nil
}
