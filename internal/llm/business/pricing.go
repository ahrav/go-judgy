package business

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ahrav/go-judgy/internal/llm/configuration"
	"github.com/ahrav/go-judgy/internal/llm/errors"
	"github.com/ahrav/go-judgy/internal/llm/transport"
)

// Pricing constants.
const (
	MilliCentsToFactor   = 1000
	HundredPercent       = 100
	RefreshBufferMinutes = 5

	// GPT-4o pricing.
	GPT4oPromptCost = 30000
	GPT4oOutputCost = 60000

	// GPT-4o Mini pricing.
	GPT4oMiniPromptCost = 1500
	GPT4oMiniOutputCost = 2000

	// Claude 3.5 Sonnet pricing.
	Claude35SonnetPromptCost = 15000
	Claude35SonnetOutputCost = 75000

	// Claude 3.5 Haiku pricing.
	Claude35HaikuPromptCost = 3000
	Claude35HaikuOutputCost = 15000

	// Gemini 1.5 Flash pricing.
	Gemini15FlashPromptCost = 500
	Gemini15FlashOutputCost = 1500
)

// PricingRegistry provides cost estimation and budget enforcement for LLM operations.
// It supports dynamic pricing updates, fail-closed behavior for missing data,
// and multi-region pricing to enable cost-aware LLM request routing.
type PricingRegistry interface {
	// GetCost calculates estimated cost in milli-cents for LLM usage.
	// Returns pricing error if data unavailable or expired in fail-closed mode.
	GetCost(ctx context.Context, provider, model, region string, usage transport.NormalizedUsage) (int64, error)

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
//
// Rounding Rules:
// 1. All calculations use integer arithmetic to avoid floating-point errors
// 2. Division by 1000 (MilliCentsToFactor) happens last to minimize rounding loss
// 3. Results are truncated (rounded down) to nearest milli-cent
// 4. Zero usage results in zero cost (no minimum charge).
func (p *PricingEntry) Calculate(usage transport.NormalizedUsage) int64 {
	// Calculate cost per token type using integer arithmetic
	// Formula: (tokens * costPer1000) / 1000
	promptCost := (usage.PromptTokens * p.PromptCostPer1000) / MilliCentsToFactor
	outputCost := (usage.CompletionTokens * p.OutputCostPer1000) / MilliCentsToFactor
	return promptCost + outputCost
}

// InMemoryPricingRegistry provides in-memory cost estimation with thread safety.
// Supports fail-closed operation for production cost control and regional
// pricing fallbacks. Production implementations should use distributed storage.
type InMemoryPricingRegistry struct {
	mu           sync.RWMutex
	entries      map[string]*PricingEntry
	lastUpdated  time.Time
	failClosed   bool
	driftMetrics *CostDriftMetrics
}

// CostDriftMetrics tracks differences between estimated and actual costs.
// Enables pricing accuracy monitoring and adjustment when providers
// return explicit cost data in their responses.
type CostDriftMetrics struct {
	mu           sync.RWMutex
	observations map[string]*DriftObservation
}

// DriftObservation records cost drift for a specific provider/model.
type DriftObservation struct {
	Provider         string
	Model            string
	EstimatedSum     int64 // Sum of estimated costs in milli-cents
	ActualSum        int64 // Sum of actual costs from provider
	ObservationCount int64 // Number of observations
	MaxDrift         int64 // Maximum single drift observed
	LastUpdated      time.Time
}

// RecordDrift records a cost drift observation.
func (m *CostDriftMetrics) RecordDrift(provider, model string, estimated, actual int64) {
	if m == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf("%s/%s", provider, model)
	obs, exists := m.observations[key]
	if !exists {
		obs = &DriftObservation{
			Provider: provider,
			Model:    model,
		}
		m.observations[key] = obs
	}

	obs.EstimatedSum += estimated
	obs.ActualSum += actual
	obs.ObservationCount++

	drift := actual - estimated
	if abs(drift) > abs(obs.MaxDrift) {
		obs.MaxDrift = drift
	}
	obs.LastUpdated = time.Now()
}

// GetDriftPercentage returns the average drift percentage for a provider/model.
func (m *CostDriftMetrics) GetDriftPercentage(provider, model string) float64 {
	if m == nil {
		return 0
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	key := fmt.Sprintf("%s/%s", provider, model)
	obs, exists := m.observations[key]
	if !exists || obs.EstimatedSum == 0 {
		return 0
	}

	return float64(obs.ActualSum-obs.EstimatedSum) / float64(obs.EstimatedSum) * HundredPercent
}

// abs returns absolute value of an int64.
func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// NewInMemoryPricingRegistry creates a pricing registry with default rates.
// Initializes with common model pricing for immediate use while supporting
// fail-closed behavior for production cost control.
func NewInMemoryPricingRegistry(failClosed bool) *InMemoryPricingRegistry {
	registry := &InMemoryPricingRegistry{
		entries:    make(map[string]*PricingEntry),
		failClosed: failClosed,
		driftMetrics: &CostDriftMetrics{
			observations: make(map[string]*DriftObservation),
		},
	}

	// Load default pricing data for testing/development
	registry.loadDefaults()

	return registry
}

// GetCost implements PricingRegistry interface for cost calculation.
// Provides regional pricing fallbacks and fail-closed error handling
// to ensure cost visibility and prevent unbounded spending.
func (r *InMemoryPricingRegistry) GetCost(_ context.Context, provider, model, region string, usage transport.NormalizedUsage) (int64, error) {
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
			return 0, &errors.PricingError{
				Provider: provider,
				Model:    model,
				Region:   region,
				Reason:   "pricing data not available",
				Type:     errors.ErrorTypePricingUnavailable,
			}
		}
		return 0, nil
	}

	if entry.IsExpired() {
		if r.failClosed {
			return 0, &errors.PricingError{
				Provider: provider,
				Model:    model,
				Region:   region,
				Reason:   fmt.Sprintf("pricing data expired at %v", entry.ValidUntil),
				Type:     errors.ErrorTypePricingUnavailable,
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
	// In production, this would fetch from an external pricing API
	// with conditional refresh using ETag or If-Modified-Since headers

	// Check if refresh is needed based on TTL
	if !r.shouldRefresh() {
		return nil // Data is still fresh
	}

	// Simulate conditional refresh check
	// In production: check ETag or Last-Modified headers
	newEntries, updated, err := r.fetchPricingData(ctx)
	if err != nil {
		// On error, decide based on fail-closed policy
		if r.failClosed && r.hasExpiredEntries() {
			return &errors.PricingError{
				Provider: "all",
				Model:    "all",
				Reason:   fmt.Sprintf("failed to refresh pricing data: %v", err),
				Type:     errors.ErrorTypePricingUnavailable,
			}
		}
		// If fail-open, continue with existing data
		return nil
	}

	if !updated {
		// Data hasn't changed, just extend validity
		r.extendValidity()
		return nil
	}

	// Update with new pricing data
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, entry := range newEntries {
		r.entries[entry.Key()] = entry
	}
	r.lastUpdated = time.Now()

	return nil
}

// shouldRefresh determines if pricing data needs refreshing.
func (r *InMemoryPricingRegistry) shouldRefresh() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Refresh if we haven't updated in the TTL period
	if time.Since(r.lastUpdated) > configuration.PricingValidFor {
		return true
	}

	// Refresh if any entries are expired or about to expire
	threshold := time.Now().Add(RefreshBufferMinutes * time.Minute) // 5 minute buffer
	for _, entry := range r.entries {
		if entry.ValidUntil.Before(threshold) {
			return true
		}
	}

	return false
}

// hasExpiredEntries checks if any pricing entries are expired.
func (r *InMemoryPricingRegistry) hasExpiredEntries() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	now := time.Now()
	for _, entry := range r.entries {
		if entry.ValidUntil.Before(now) {
			return true
		}
	}
	return false
}

// extendValidity extends the validity of all pricing entries.
func (r *InMemoryPricingRegistry) extendValidity() {
	r.mu.Lock()
	defer r.mu.Unlock()

	extension := time.Now().Add(configuration.PricingValidFor)
	for _, entry := range r.entries {
		entry.ValidUntil = extension
	}
	r.lastUpdated = time.Now()
}

// fetchPricingData simulates fetching pricing from external source.
// In production, this would make HTTP requests with conditional headers.
//
//nolint:unparam // error return is nil in this stub implementation but required for production interface
func (r *InMemoryPricingRegistry) fetchPricingData(_ context.Context) ([]*PricingEntry, bool, error) {
	// In production:
	// 1. Send request with If-Modified-Since or ETag headers
	// 2. Handle 304 Not Modified responses
	// 3. Parse and validate pricing data
	// 4. Return entries, whether data was updated, and any error

	// For now, return the default entries as if freshly fetched
	now := time.Now()
	validFor := configuration.PricingValidFor

	entries := []*PricingEntry{
		// OpenAI pricing (rates in milli-cents per 1000 tokens)
		{
			Provider:          "openai",
			Model:             "gpt-4",
			PromptCostPer1000: GPT4oPromptCost,
			OutputCostPer1000: GPT4oOutputCost,
			ValidUntil:        now.Add(validFor),
		},
		{
			Provider:          "openai",
			Model:             "gpt-3.5-turbo",
			PromptCostPer1000: GPT4oMiniPromptCost,
			OutputCostPer1000: GPT4oMiniOutputCost,
			ValidUntil:        now.Add(validFor),
		},
		// Anthropic pricing
		{
			Provider:          "anthropic",
			Model:             "claude-3-opus-20240229",
			PromptCostPer1000: Claude35SonnetPromptCost,
			OutputCostPer1000: Claude35SonnetOutputCost,
			ValidUntil:        now.Add(validFor),
		},
		{
			Provider:          "anthropic",
			Model:             "claude-3-sonnet-20240229",
			PromptCostPer1000: Claude35HaikuPromptCost,
			OutputCostPer1000: Claude35HaikuOutputCost,
			ValidUntil:        now.Add(validFor),
		},
		// Google pricing
		{
			Provider:          "google",
			Model:             "gemini-pro",
			PromptCostPer1000: Gemini15FlashPromptCost,
			OutputCostPer1000: Gemini15FlashOutputCost,
			ValidUntil:        now.Add(validFor),
		},
	}

	return entries, true, nil
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
	validFor := configuration.PricingValidFor

	defaults := []*PricingEntry{
		// OpenAI pricing (example rates in milli-cents per 1000 tokens).
		{
			Provider:          "openai",
			Model:             "gpt-4",
			PromptCostPer1000: GPT4oPromptCost,
			OutputCostPer1000: GPT4oOutputCost,
			ValidUntil:        now.Add(validFor),
		},
		{
			Provider:          "openai",
			Model:             "gpt-3.5-turbo",
			PromptCostPer1000: GPT4oMiniPromptCost,
			OutputCostPer1000: GPT4oMiniOutputCost,
			ValidUntil:        now.Add(validFor),
		},

		// Anthropic pricing (example rates).
		{
			Provider:          "anthropic",
			Model:             "claude-3-opus-20240229",
			PromptCostPer1000: Claude35SonnetPromptCost,
			OutputCostPer1000: Claude35SonnetOutputCost,
			ValidUntil:        now.Add(validFor),
		},
		{
			Provider:          "anthropic",
			Model:             "claude-3-sonnet-20240229",
			PromptCostPer1000: Claude35HaikuPromptCost,
			OutputCostPer1000: Claude35HaikuOutputCost,
			ValidUntil:        now.Add(validFor),
		},

		// Google pricing (example rates).
		{
			Provider:          "google",
			Model:             "gemini-pro",
			PromptCostPer1000: Gemini15FlashPromptCost,
			OutputCostPer1000: Gemini15FlashOutputCost,
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
func NewPricingMiddlewareWithRegistry(registry PricingRegistry) transport.Middleware {
	return func(next transport.Handler) transport.Handler {
		return transport.HandlerFunc(func(ctx context.Context, req *transport.Request) (*transport.Response, error) {
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

			// In production, if provider returns actual cost in response headers or body,
			// we would record drift here for monitoring pricing accuracy.
			// Example: r.driftMetrics.RecordDrift(provider, model, estimated, actual)

			return resp, nil
		})
	}
}

// RecordCostDrift records the difference between estimated and actual costs.
// This method enables tracking of pricing accuracy when providers return
// explicit cost data in their responses.
func (r *InMemoryPricingRegistry) RecordCostDrift(provider, model string, estimated, actual int64) {
	if r.driftMetrics != nil {
		r.driftMetrics.RecordDrift(provider, model, estimated, actual)
	}
}

// GetDriftMetrics returns the drift percentage for a specific provider/model.
// Returns 0 if no drift data is available or metrics are disabled.
func (r *InMemoryPricingRegistry) GetDriftMetrics(provider, model string) float64 {
	if r.driftMetrics != nil {
		return r.driftMetrics.GetDriftPercentage(provider, model)
	}
	return 0
}
