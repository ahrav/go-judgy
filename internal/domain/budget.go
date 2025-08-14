package domain

import "fmt"

const unknownBudgetType = "unknown"

// BudgetType represents the type of budget limit that can be exceeded.
// Using typed constants provides compile-time safety and enables exhaustive
// switch statements for budget violation handling.
type BudgetType uint8

const (
	// BudgetTokens represents token consumption limits.
	BudgetTokens BudgetType = iota

	// BudgetCalls represents API call count limits.
	BudgetCalls

	// BudgetCost represents monetary cost limits.
	BudgetCost

	// BudgetTime represents execution time limits.
	BudgetTime
)

// String returns the string representation of a BudgetType.
func (b BudgetType) String() string {
	switch b {
	case BudgetTokens:
		return "tokens"
	case BudgetCalls:
		return "calls"
	case BudgetCost:
		return "cost"
	case BudgetTime:
		return "time"
	default:
		return unknownBudgetType
	}
}

const (
	defaultMaxTotalTokensLimit = 10000
	defaultMaxCallsLimit       = 10
	defaultMaxCostCentsLimit   = 100
	defaultTimeoutSecsLimit    = 300
)

// BudgetLimits defines resource constraints for an evaluation.
// These limits prevent runaway costs and ensure predictable resource usage
// across token consumption, API calls, financial costs, and execution time.
type BudgetLimits struct {
	// MaxTotalTokens limits the total tokens that can be consumed (minimum 100).
	// Includes input and output tokens across all API calls.
	MaxTotalTokens int64 `json:"max_total_tokens" validate:"required,min=100"`

	// MaxCalls limits the number of LLM API calls (minimum 1).
	// Includes both answer generation and scoring/judging calls.
	MaxCalls int64 `json:"max_calls" validate:"required,min=1"`

	// MaxCostCents limits the total cost using the Cents type (minimum 1).
	// Calculated based on provider pricing for tokens consumed.
	MaxCostCents Cents `json:"max_cost_cents" validate:"required,min=1"`

	// TimeoutSecs sets the overall timeout in seconds (minimum 30).
	// Covers the entire evaluation process, not individual API calls.
	TimeoutSecs int64 `json:"timeout_secs" validate:"required,min=30"`
}

// DefaultBudgetLimits returns default budget limits with reasonable constraints.
// The defaults are:
//   - MaxTotalTokens: 10000 (generous token allowance)
//   - MaxCalls: 10 (reasonable API call limit)
//   - MaxCostCents: 100 (1 USD cost limit)
//   - TimeoutSecs: 300 (5 minute timeout)
func DefaultBudgetLimits() BudgetLimits {
	return BudgetLimits{
		MaxTotalTokens: defaultMaxTotalTokensLimit,
		MaxCalls:       defaultMaxCallsLimit,
		MaxCostCents:   Cents(defaultMaxCostCentsLimit),
		TimeoutSecs:    defaultTimeoutSecsLimit,
	}
}

// Validate checks if the budget limits meet all requirements.
// Returns nil if valid, or a validation error describing the first constraint violation.
func (b *BudgetLimits) Validate() error { return validate.Struct(b) }

// BudgetExceededError indicates that an operation would exceed budget limits.
// It provides detailed information about which limit was exceeded and by how much,
// enabling precise error reporting and recovery strategies.
type BudgetExceededError struct {
	// Type indicates which budget limit was exceeded.
	Type BudgetType

	// Limit is the budget limit that would be exceeded.
	Limit int64

	// Current is the current usage before the attempted operation.
	Current int64

	// Required is the amount required for the attempted operation.
	Required int64
}

// Error returns a formatted error message describing the budget violation.
// The message includes the budget type, limit, current usage, and required amount.
func (e BudgetExceededError) Error() string {
	return fmt.Sprintf("budget exceeded for %s: limit=%d, current=%d, required=%d",
		e.Type, e.Limit, e.Current, e.Required)
}

// OverBy returns how much the operation would exceed the budget limit.
// Useful for determining the magnitude of budget violations and planning recovery.
func (e BudgetExceededError) OverBy() int64 { return e.Current + e.Required - e.Limit }

// NewBudgetExceededError creates a new budget exceeded error with detailed context.
// The budgetType should be one of the defined BudgetType constants.
func NewBudgetExceededError(budgetType BudgetType, limit, current, required int64) BudgetExceededError {
	return BudgetExceededError{
		Type:     budgetType,
		Limit:    limit,
		Current:  current,
		Required: required,
	}
}
