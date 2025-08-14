package domain

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"
	"testing/quick"
)

// Property: OverBy() calculation is always correct
func TestBudgetExceededError_OverBy_Property(t *testing.T) {
	f := func(limit, current, required int64) bool {
		// Avoid overflow scenarios for property testing
		if wouldOverflow(current, required) {
			return true // Skip overflow cases
		}

		err := BudgetExceededError{
			Type:     BudgetTokens,
			Limit:    limit,
			Current:  current,
			Required: required,
		}

		expected := current + required - limit
		actual := err.OverBy()

		return actual == expected
	}

	if err := quick.Check(f, nil); err != nil {
		t.Errorf("OverBy() property test failed: %v", err)
	}
}

// Property: If Current + Required <= Limit, then OverBy() <= 0
func TestBudgetExceededError_OverBy_UnderLimit_Property(t *testing.T) {
	f := func(limit, current, required int64) bool {
		// Ensure we're testing the under-limit case
		// Adjust values to avoid overflow
		if limit <= 0 {
			limit = math.MaxInt64 / 3
		}
		if current < 0 {
			current = -current
		}
		if required < 0 {
			required = -required
		}

		// Ensure current + required <= limit
		if current > limit {
			current = limit / 2
		}
		if required > limit-current {
			required = (limit - current) / 2
		}

		if wouldOverflow(current, required) {
			return true // Skip overflow cases
		}

		if current+required > limit {
			return true // Skip cases that don't meet our precondition
		}

		err := BudgetExceededError{
			Type:     BudgetCalls,
			Limit:    limit,
			Current:  current,
			Required: required,
		}

		return err.OverBy() <= 0
	}

	if err := quick.Check(f, nil); err != nil {
		t.Errorf("OverBy() under limit property test failed: %v", err)
	}
}

// Property: If Current + Required > Limit, then OverBy() > 0
func TestBudgetExceededError_OverBy_OverLimit_Property(t *testing.T) {
	f := func(limit, current, required int64) bool {
		// Ensure we're testing the over-limit case
		if limit <= 0 {
			limit = 100
		}
		if current <= 0 {
			current = limit / 2
		}
		if required <= 0 {
			required = limit / 2
		}

		// Ensure current + required > limit
		if current+required <= limit {
			required = limit - current + 1
		}

		if wouldOverflow(current, required) {
			return true // Skip overflow cases
		}

		err := BudgetExceededError{
			Type:     BudgetCost,
			Limit:    limit,
			Current:  current,
			Required: required,
		}

		overBy := err.OverBy()
		expected := current + required - limit

		return overBy > 0 && overBy == expected
	}

	if err := quick.Check(f, nil); err != nil {
		t.Errorf("OverBy() over limit property test failed: %v", err)
	}
}

// Property: Error message always contains all fields
func TestBudgetExceededError_Error_ContainsAllFields_Property(t *testing.T) {
	f := func(budgetType uint8, limit, current, required int64) bool {
		// Limit budget type to valid range plus some invalid values
		bt := BudgetType(budgetType % 10)

		err := BudgetExceededError{
			Type:     bt,
			Limit:    limit,
			Current:  current,
			Required: required,
		}

		msg := err.Error()

		// Check that all values are present in the message
		containsLimit := strings.Contains(msg, fmt.Sprintf("limit=%d", limit))
		containsCurrent := strings.Contains(msg, fmt.Sprintf("current=%d", current))
		containsRequired := strings.Contains(msg, fmt.Sprintf("required=%d", required))
		containsType := strings.Contains(msg, bt.String())

		return containsLimit && containsCurrent && containsRequired && containsType
	}

	if err := quick.Check(f, nil); err != nil {
		t.Errorf("Error message property test failed: %v", err)
	}
}

// Property: BudgetType.String() is always non-empty
func TestBudgetType_String_NonEmpty_Property(t *testing.T) {
	f := func(val uint8) bool {
		bt := BudgetType(val)
		str := bt.String()
		return len(str) > 0
	}

	if err := quick.Check(f, nil); err != nil {
		t.Errorf("BudgetType.String() non-empty property test failed: %v", err)
	}
}

// Property: BudgetType.String() is deterministic
func TestBudgetType_String_Deterministic_Property(t *testing.T) {
	f := func(val uint8) bool {
		bt := BudgetType(val)
		first := bt.String()
		second := bt.String()
		third := bt.String()

		return first == second && second == third
	}

	if err := quick.Check(f, nil); err != nil {
		t.Errorf("BudgetType.String() deterministic property test failed: %v", err)
	}
}

// Property: NewBudgetExceededError preserves all input values
func TestNewBudgetExceededError_PreservesValues_Property(t *testing.T) {
	f := func(budgetType uint8, limit, current, required int64) bool {
		bt := BudgetType(budgetType)

		err := NewBudgetExceededError(bt, limit, current, required)

		return err.Type == bt &&
			err.Limit == limit &&
			err.Current == current &&
			err.Required == required
	}

	if err := quick.Check(f, nil); err != nil {
		t.Errorf("NewBudgetExceededError preserves values property test failed: %v", err)
	}
}

// Property: DefaultBudgetLimits always returns valid limits
func TestDefaultBudgetLimits_AlwaysValid_Property(t *testing.T) {
	// Run this multiple times with different random seeds
	for i := 0; i < 100; i++ {
		t.Run(fmt.Sprintf("iteration_%d", i), func(t *testing.T) {
			limits := DefaultBudgetLimits()

			// Should always be valid
			if err := limits.Validate(); err != nil {
				t.Errorf("DefaultBudgetLimits() returned invalid limits: %v", err)
			}

			// Should always have positive values
			if limits.MaxTotalTokens <= 0 {
				t.Error("DefaultBudgetLimits().MaxTotalTokens should be positive")
			}
			if limits.MaxCalls <= 0 {
				t.Error("DefaultBudgetLimits().MaxCalls should be positive")
			}
			if limits.MaxCostCents <= 0 {
				t.Error("DefaultBudgetLimits().MaxCostCents should be positive")
			}
			if limits.TimeoutSecs <= 0 {
				t.Error("DefaultBudgetLimits().TimeoutSecs should be positive")
			}
		})
	}
}

// Property: Valid BudgetTypes (0-3) always return non-"unknown" strings
func TestBudgetType_ValidTypes_Property(t *testing.T) {
	validTypes := []BudgetType{
		BudgetTokens,
		BudgetCalls,
		BudgetCost,
		BudgetTime,
	}

	for _, bt := range validTypes {
		t.Run(bt.String(), func(t *testing.T) {
			str := bt.String()
			if str == "unknown" {
				t.Errorf("Valid BudgetType %d returned 'unknown'", bt)
			}
			if str == "" {
				t.Errorf("Valid BudgetType %d returned empty string", bt)
			}
		})
	}
}

// Property: Invalid BudgetTypes (>3) always return "unknown"
func TestBudgetType_InvalidTypes_Property(t *testing.T) {
	f := func(val uint8) bool {
		// Only test values outside the valid range
		if val <= 3 {
			return true // Skip valid types
		}

		bt := BudgetType(val)
		return bt.String() == "unknown"
	}

	if err := quick.Check(f, nil); err != nil {
		t.Errorf("Invalid BudgetType property test failed: %v", err)
	}
}

// Property: OverBy() is mathematically consistent across operations
func TestBudgetExceededError_OverBy_Mathematical_Property(t *testing.T) {
	f := func(limit, current, required int64) bool {
		if wouldOverflow(current, required) {
			return true // Skip overflow cases
		}

		err1 := BudgetExceededError{
			Type:     BudgetTokens,
			Limit:    limit,
			Current:  current,
			Required: required,
		}

		// Create equivalent error with rearranged values
		err2 := BudgetExceededError{
			Type:     BudgetTokens,
			Limit:    limit,
			Current:  required, // Swap current and required
			Required: current,
		}

		// OverBy should be commutative with respect to current and required
		// when comparing the sum
		sum1 := err1.Current + err1.Required
		sum2 := err2.Current + err2.Required

		if sum1 != sum2 {
			return false
		}

		return err1.OverBy() == err2.OverBy()
	}

	if err := quick.Check(f, nil); err != nil {
		t.Errorf("OverBy() mathematical consistency property test failed: %v", err)
	}
}

// Property: BudgetLimits with minimum valid values should always validate
func TestBudgetLimits_MinimumValues_Property(t *testing.T) {
	// Test edge cases around minimum values
	testCases := []struct {
		tokens  int64
		calls   int64
		cents   Cents
		timeout int64
		valid   bool
	}{
		{100, 1, 1, 30, true},  // All at minimum
		{99, 1, 1, 30, false},  // Tokens below minimum
		{100, 0, 1, 30, false}, // Calls below minimum
		{100, 1, 0, 30, false}, // Cents below minimum
		{100, 1, 1, 29, false}, // Timeout below minimum
		{101, 2, 2, 31, true},  // All above minimum
		{math.MaxInt64, math.MaxInt64, Cents(math.MaxInt64), math.MaxInt64, true}, // Max values
	}

	for i, tc := range testCases {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			limits := BudgetLimits{
				MaxTotalTokens: tc.tokens,
				MaxCalls:       tc.calls,
				MaxCostCents:   tc.cents,
				TimeoutSecs:    tc.timeout,
			}

			err := limits.Validate()
			isValid := err == nil

			if isValid != tc.valid {
				t.Errorf("BudgetLimits validation mismatch: got valid=%v, want valid=%v, err=%v",
					isValid, tc.valid, err)
			}
		})
	}
}

// Property: Error message format is consistent
func TestBudgetExceededError_Error_Format_Property(t *testing.T) {
	f := func(budgetType uint8, limit, current, required int64) bool {
		bt := BudgetType(budgetType % 10) // Limit to reasonable range

		err := NewBudgetExceededError(bt, limit, current, required)
		msg := err.Error()

		// Check format: "budget exceeded for %s: limit=%d, current=%d, required=%d"
		expectedPrefix := "budget exceeded for "
		if !strings.HasPrefix(msg, expectedPrefix) {
			return false
		}

		// Check that it contains the expected structure
		containsColon := strings.Contains(msg, ":")
		containsLimit := strings.Contains(msg, "limit=")
		containsCurrent := strings.Contains(msg, "current=")
		containsRequired := strings.Contains(msg, "required=")
		containsCommas := strings.Count(msg, ",") >= 2

		return containsColon && containsLimit && containsCurrent &&
			containsRequired && containsCommas
	}

	if err := quick.Check(f, nil); err != nil {
		t.Errorf("Error message format property test failed: %v", err)
	}
}

// Fuzz-like property test for budget validation edge cases
func TestBudgetLimits_Validate_RandomValues_Property(t *testing.T) {
	rng := rand.New(rand.NewSource(42)) // For reproducibility

	for i := 0; i < 1000; i++ {
		// Generate random values around the boundaries
		tokens := generateBoundaryValue(100, rng.Intn(3))
		calls := generateBoundaryValue(1, rng.Intn(3))
		cents := Cents(generateBoundaryValue(1, rng.Intn(3)))
		timeout := generateBoundaryValue(30, rng.Intn(3))

		limits := BudgetLimits{
			MaxTotalTokens: tokens,
			MaxCalls:       calls,
			MaxCostCents:   cents,
			TimeoutSecs:    timeout,
		}

		err := limits.Validate()

		// Verify the validation is correct
		shouldBeValid := tokens >= 100 && calls >= 1 && cents >= 1 && timeout >= 30
		isValid := err == nil

		if shouldBeValid != isValid {
			t.Errorf("Validation mismatch for tokens=%d, calls=%d, cents=%d, timeout=%d: "+
				"expected valid=%v, got valid=%v, err=%v",
				tokens, calls, cents, timeout, shouldBeValid, isValid, err)
		}
	}
}

// Helper function to check for potential integer overflow
func wouldOverflow(a, b int64) bool {
	if a > 0 && b > 0 && a > math.MaxInt64-b {
		return true
	}
	if a < 0 && b < 0 && a < math.MinInt64-b {
		return true
	}
	return false
}

// Helper function to generate values around boundaries
func generateBoundaryValue(boundary int64, variant int) int64 {
	switch variant {
	case 0:
		return boundary - 1 // Below boundary
	case 1:
		return boundary // At boundary
	case 2:
		return boundary + 1 // Above boundary
	default:
		return boundary
	}
}
