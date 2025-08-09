package llm

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// PricingRegistry provides cost estimation and budget enforcement for LLM operations.
// It supports dynamic pricing updates, fail-closed behavior for missing data,
// and multi-region pricing to enable cost-aware LLM request routing.
type PricingRegistry interface {
	// GetCost calculates estimated cost in milli-cents for LLM usage.
	// Returns pricing error if data unavailable or expired in fail-closed mode.
	GetCost(ctx context.Context, provider, model, region string, usage NormalizedUsage) (int64, error)

	// IsAvailable checks pricing data availability for provider/model/region combinations.
	// Returns false if data is missing or expired, enabling fallback strategies.
	IsAvailable(provider, model, region string) bool

	// Refresh updates pricing data from external sources.
	// In production implementations, this fetches current rates from provider APIs.
	Refresh(ctx context.Context) error

	// LastUpdated provides the timestamp of the last successful pricing refresh.
	// Enables staleness detection and refresh scheduling for cost accuracy.
	LastUpdated() time.Time
}

// PricingEntry contains cost data for specific provider/model/region combinations.
// It stores per-token pricing rates in milli-cents with expiration timestamps
// to support dynamic pricing updates and cost budget enforcement.
type PricingEntry struct {
	Provider          string    `json:"provider"`
	Model             string    `json:"model"`
	Region            string    `json:"region,omitempty"`
	PromptCostPer1000 int64     `json:"prompt_cost_per_1000"` // Milli-cents per 1000 prompt tokens
	OutputCostPer1000 int64     `json:"output_cost_per_1000"` // Milli-cents per 1000 output tokens
	ValidUntil        time.Time `json:"valid_until"`          // Pricing expiration timestamp
}

// Key generates a unique identifier for pricing entry lookup.
// Creates hierarchical keys with optional region specificity for
// efficient pricing registry operations and cache management.
func (p *PricingEntry) Key() string {
	if p.Region != "" {
		return fmt.Sprintf("%s/%s/%s", p.Provider, p.Model, p.Region)
	}
	return fmt.Sprintf("%s/%s", p.Provider, p.Model)
}

// IsExpired determines if pricing data is stale and requires refresh.
// Returns true when current time exceeds ValidUntil timestamp,
// triggering fail-closed behavior or refresh operations.
func (p *PricingEntry) IsExpired() bool {
	return time.Now().After(p.ValidUntil)
}

// Calculate computes total cost in milli-cents for token usage.
// Applies separate rates for prompt and completion tokens with
// precise scaling to avoid rounding errors in cost calculations.
func (p *PricingEntry) Calculate(usage NormalizedUsage) int64 {
	promptCost := (usage.PromptTokens * p.PromptCostPer1000) / 1000
	outputCost := (usage.CompletionTokens * p.OutputCostPer1000) / 1000
	return promptCost + outputCost
}

// InMemoryPricingRegistry provides in-memory cost estimation with thread safety.
// Supports fail-closed operation for production cost control and regional
// pricing fallbacks. Production implementations should use distributed storage.
type InMemoryPricingRegistry struct {
	mu          sync.RWMutex
	entries     map[string]*PricingEntry
	lastUpdated time.Time
	failClosed  bool
}

// NewInMemoryPricingRegistry creates a pricing registry with default rates.
// Initializes with common model pricing for immediate use while supporting
// fail-closed behavior for production cost control.
func NewInMemoryPricingRegistry(failClosed bool) *InMemoryPricingRegistry {
	registry := &InMemoryPricingRegistry{
		entries:    make(map[string]*PricingEntry),
		failClosed: failClosed,
	}

	// Load default pricing data for testing/development
	registry.loadDefaults()

	return registry
}

// GetCost implements PricingRegistry interface for cost calculation.
// Provides regional pricing fallbacks and fail-closed error handling
// to ensure cost visibility and prevent unbounded spending.
func (r *InMemoryPricingRegistry) GetCost(ctx context.Context, provider, model, region string, usage NormalizedUsage) (int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	key := buildPricingKey(provider, model, region)
	entry, exists := r.entries[key]

	if !exists {
		if region != "" {
			key = buildPricingKey(provider, model, "")
			entry, exists = r.entries[key]
		}
	}

	if !exists {
		if r.failClosed {
			return 0, &PricingError{
				Provider: provider,
				Model:    model,
				Region:   region,
				Reason:   "pricing data not available",
				Type:     ErrorTypePricingUnavailable,
			}
		}
		return 0, nil
	}

	if entry.IsExpired() {
		if r.failClosed {
			return 0, &PricingError{
				Provider: provider,
				Model:    model,
				Region:   region,
				Reason:   fmt.Sprintf("pricing data expired at %v", entry.ValidUntil),
				Type:     ErrorTypePricingUnavailable,
			}
		}
		// Use expired pricing if fail-open.
	}

	return entry.Calculate(usage), nil
}

// IsAvailable implements PricingRegistry interface for availability checks.
// Provides regional fallback logic and expiration awareness to support
// intelligent pricing strategy and refresh scheduling.
func (r *InMemoryPricingRegistry) IsAvailable(provider, model, region string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	key := buildPricingKey(provider, model, region)
	entry, exists := r.entries[key]

	if !exists && region != "" {
		key = buildPricingKey(provider, model, "")
		entry, exists = r.entries[key]
	}

	return exists && !entry.IsExpired()
}

// Refresh implements PricingRegistry interface for data updates.
// Extends expired pricing validity and updates timestamps to maintain
// cost accuracy and prevent unnecessary external API calls.
func (r *InMemoryPricingRegistry) Refresh(ctx context.Context) error {
	// TODO: come back to this.
	// In production, this would fetch from an external pricing API
	// For now, we just update the timestamp
	r.mu.Lock()
	defer r.mu.Unlock()

	r.lastUpdated = time.Now()

	// Could extend pricing validity here
	for _, entry := range r.entries {
		if entry.IsExpired() {
			entry.ValidUntil = time.Now().Add(24 * time.Hour)
		}
	}

	return nil
}

// LastUpdated implements PricingRegistry interface for staleness detection.
// Provides thread-safe access to last refresh timestamp for monitoring
// and automated refresh scheduling in production environments.
func (r *InMemoryPricingRegistry) LastUpdated() time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastUpdated
}

// AddEntry adds or updates pricing data with automatic timestamp tracking.
// Provides thread-safe entry management and updates registry modification
// time for staleness monitoring and refresh coordination.
func (r *InMemoryPricingRegistry) AddEntry(entry *PricingEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.entries[entry.Key()] = entry
	r.lastUpdated = time.Now()
}

// loadDefaults initializes registry with common model pricing rates.
// Provides immediate pricing availability for development and testing
// while establishing baseline costs for production budget planning.
func (r *InMemoryPricingRegistry) loadDefaults() {
	now := time.Now()
	validFor := 24 * time.Hour

	defaults := []*PricingEntry{
		// OpenAI pricing (example rates in milli-cents per 1000 tokens).
		{
			Provider:          "openai",
			Model:             "gpt-4",
			PromptCostPer1000: 30000,
			OutputCostPer1000: 60000,
			ValidUntil:        now.Add(validFor),
		},
		{
			Provider:          "openai",
			Model:             "gpt-3.5-turbo",
			PromptCostPer1000: 1500,
			OutputCostPer1000: 2000,
			ValidUntil:        now.Add(validFor),
		},

		// Anthropic pricing (example rates).
		{
			Provider:          "anthropic",
			Model:             "claude-3-opus-20240229",
			PromptCostPer1000: 15000,
			OutputCostPer1000: 75000,
			ValidUntil:        now.Add(validFor),
		},
		{
			Provider:          "anthropic",
			Model:             "claude-3-sonnet-20240229",
			PromptCostPer1000: 3000,
			OutputCostPer1000: 15000,
			ValidUntil:        now.Add(validFor),
		},

		// Google pricing (example rates).
		{
			Provider:          "google",
			Model:             "gemini-pro",
			PromptCostPer1000: 500,
			OutputCostPer1000: 1500,
			ValidUntil:        now.Add(validFor),
		},
	}

	for _, entry := range defaults {
		r.entries[entry.Key()] = entry
	}

	r.lastUpdated = now
}

// buildPricingKey generates consistent lookup keys for pricing entries.
// Creates hierarchical provider/model/region keys with optional regional
// specificity to support fallback pricing strategies.
func buildPricingKey(provider, model, region string) string {
	if region != "" {
		return fmt.Sprintf("%s/%s/%s", provider, model, region)
	}
	return fmt.Sprintf("%s/%s", provider, model)
}

// NewPricingMiddlewareWithRegistry creates pricing middleware with custom registry.
// Wraps LLM handlers to add cost estimation after successful operations,
// enabling request-level cost tracking and budget enforcement.
func NewPricingMiddlewareWithRegistry(registry PricingRegistry) Middleware {
	return func(next Handler) Handler {
		return HandlerFunc(func(ctx context.Context, req *LLMRequest) (*Request, error) {
			// Call next handler first to get usage data.
			resp, err := next.Handle(ctx, req)
			if err != nil {
				return nil, err
			}

			cost, err := registry.GetCost(ctx, req.Provider, req.Model, "", resp.Usage)
			if err != nil {
				// Pricing error should be non-retryable
				return nil, err
			}

			resp.EstimatedCostMilliCents = cost

			return resp, nil
		})
	}
}
