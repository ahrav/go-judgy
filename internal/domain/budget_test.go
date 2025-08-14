package domain

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
)

func TestBudgetType_String(t *testing.T) {
	tests := []struct {
		name string
		b    BudgetType
		want string
	}{
		{
			name: "tokens budget type",
			b:    BudgetTokens,
			want: "tokens",
		},
		{
			name: "calls budget type",
			b:    BudgetCalls,
			want: "calls",
		},
		{
			name: "cost budget type",
			b:    BudgetCost,
			want: "cost",
		},
		{
			name: "time budget type",
			b:    BudgetTime,
			want: "time",
		},
		{
			name: "invalid budget type returns unknown",
			b:    BudgetType(99),
			want: "unknown",
		},
		{
			name: "max uint8 budget type returns unknown",
			b:    BudgetType(255),
			want: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.b.String(); got != tt.want {
				t.Errorf("BudgetType.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDefaultBudgetLimits(t *testing.T) {
	// Test multiple calls to ensure consistency
	for i := 0; i < 3; i++ {
		t.Run(fmt.Sprintf("call_%d", i+1), func(t *testing.T) {
			got := DefaultBudgetLimits()

			// Verify all fields match expected defaults
			if got.MaxTotalTokens != defaultMaxTotalTokensLimit {
				t.Errorf("MaxTotalTokens = %v, want %v", got.MaxTotalTokens, defaultMaxTotalTokensLimit)
			}
			if got.MaxCalls != defaultMaxCallsLimit {
				t.Errorf("MaxCalls = %v, want %v", got.MaxCalls, defaultMaxCallsLimit)
			}
			if got.MaxCostCents != Cents(defaultMaxCostCentsLimit) {
				t.Errorf("MaxCostCents = %v, want %v", got.MaxCostCents, defaultMaxCostCentsLimit)
			}
			if got.TimeoutSecs != defaultTimeoutSecsLimit {
				t.Errorf("TimeoutSecs = %v, want %v", got.TimeoutSecs, defaultTimeoutSecsLimit)
			}

			// Verify the defaults are valid
			if err := got.Validate(); err != nil {
				t.Errorf("DefaultBudgetLimits().Validate() error = %v, want nil", err)
			}
		})
	}

	// Test that modifications don't affect future calls
	t.Run("immutability", func(t *testing.T) {
		first := DefaultBudgetLimits()
		first.MaxTotalTokens = 999999

		second := DefaultBudgetLimits()
		if second.MaxTotalTokens == first.MaxTotalTokens {
			t.Error("DefaultBudgetLimits() returns mutable shared state")
		}
		if second.MaxTotalTokens != defaultMaxTotalTokensLimit {
			t.Errorf("DefaultBudgetLimits() affected by previous modification: got %v, want %v",
				second.MaxTotalTokens, defaultMaxTotalTokensLimit)
		}
	})
}

func TestBudgetLimits_Validate(t *testing.T) {
	tests := []struct {
		name    string
		budget  *BudgetLimits
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid limits with minimum values",
			budget: &BudgetLimits{
				MaxTotalTokens: 100,
				MaxCalls:       1,
				MaxCostCents:   1,
				TimeoutSecs:    30,
			},
			wantErr: false,
		},
		{
			name: "valid limits with large values",
			budget: &BudgetLimits{
				MaxTotalTokens: math.MaxInt64,
				MaxCalls:       math.MaxInt64,
				MaxCostCents:   Cents(math.MaxInt64),
				TimeoutSecs:    math.MaxInt64,
			},
			wantErr: false,
		},
		{
			name: "invalid MaxTotalTokens below minimum",
			budget: &BudgetLimits{
				MaxTotalTokens: 99,
				MaxCalls:       1,
				MaxCostCents:   1,
				TimeoutSecs:    30,
			},
			wantErr: true,
			errMsg:  "MaxTotalTokens",
		},
		{
			name: "invalid MaxTotalTokens zero",
			budget: &BudgetLimits{
				MaxTotalTokens: 0,
				MaxCalls:       1,
				MaxCostCents:   1,
				TimeoutSecs:    30,
			},
			wantErr: true,
			errMsg:  "MaxTotalTokens",
		},
		{
			name: "invalid MaxTotalTokens negative",
			budget: &BudgetLimits{
				MaxTotalTokens: -1,
				MaxCalls:       1,
				MaxCostCents:   1,
				TimeoutSecs:    30,
			},
			wantErr: true,
			errMsg:  "MaxTotalTokens",
		},
		{
			name: "invalid MaxCalls below minimum",
			budget: &BudgetLimits{
				MaxTotalTokens: 100,
				MaxCalls:       0,
				MaxCostCents:   1,
				TimeoutSecs:    30,
			},
			wantErr: true,
			errMsg:  "MaxCalls",
		},
		{
			name: "invalid MaxCalls negative",
			budget: &BudgetLimits{
				MaxTotalTokens: 100,
				MaxCalls:       -1,
				MaxCostCents:   1,
				TimeoutSecs:    30,
			},
			wantErr: true,
			errMsg:  "MaxCalls",
		},
		{
			name: "invalid MaxCostCents below minimum",
			budget: &BudgetLimits{
				MaxTotalTokens: 100,
				MaxCalls:       1,
				MaxCostCents:   0,
				TimeoutSecs:    30,
			},
			wantErr: true,
			errMsg:  "MaxCostCents",
		},
		{
			name: "invalid MaxCostCents negative",
			budget: &BudgetLimits{
				MaxTotalTokens: 100,
				MaxCalls:       1,
				MaxCostCents:   -1,
				TimeoutSecs:    30,
			},
			wantErr: true,
			errMsg:  "MaxCostCents",
		},
		{
			name: "invalid TimeoutSecs below minimum",
			budget: &BudgetLimits{
				MaxTotalTokens: 100,
				MaxCalls:       1,
				MaxCostCents:   1,
				TimeoutSecs:    29,
			},
			wantErr: true,
			errMsg:  "TimeoutSecs",
		},
		{
			name: "invalid TimeoutSecs zero",
			budget: &BudgetLimits{
				MaxTotalTokens: 100,
				MaxCalls:       1,
				MaxCostCents:   1,
				TimeoutSecs:    0,
			},
			wantErr: true,
			errMsg:  "TimeoutSecs",
		},
		{
			name: "invalid TimeoutSecs negative",
			budget: &BudgetLimits{
				MaxTotalTokens: 100,
				MaxCalls:       1,
				MaxCostCents:   1,
				TimeoutSecs:    -1,
			},
			wantErr: true,
			errMsg:  "TimeoutSecs",
		},
		{
			name: "all fields invalid",
			budget: &BudgetLimits{
				MaxTotalTokens: 0,
				MaxCalls:       0,
				MaxCostCents:   0,
				TimeoutSecs:    0,
			},
			wantErr: true,
			errMsg:  "", // Any field error is acceptable
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.budget.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("BudgetLimits.Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if err != nil && tt.errMsg != "" {
				var ve validator.ValidationErrors
				if errors.As(err, &ve) && len(ve) > 0 {
					if !strings.Contains(ve[0].Field(), tt.errMsg) {
						t.Errorf("BudgetLimits.Validate() error field = %v, want field containing %v",
							ve[0].Field(), tt.errMsg)
					}
				}
			}
		})
	}

	// Test nil pointer receiver
	t.Run("nil receiver", func(t *testing.T) {
		var b *BudgetLimits
		err := b.Validate()
		if err == nil {
			t.Error("BudgetLimits.Validate() on nil receiver should return error")
		}
	})
}

func TestBudgetExceededError_Error(t *testing.T) {
	tests := []struct {
		name string
		err  BudgetExceededError
		want string
	}{
		{
			name: "tokens budget exceeded",
			err: BudgetExceededError{
				Type:     BudgetTokens,
				Limit:    1000,
				Current:  800,
				Required: 300,
			},
			want: "budget exceeded for tokens: limit=1000, current=800, required=300",
		},
		{
			name: "calls budget exceeded",
			err: BudgetExceededError{
				Type:     BudgetCalls,
				Limit:    10,
				Current:  9,
				Required: 2,
			},
			want: "budget exceeded for calls: limit=10, current=9, required=2",
		},
		{
			name: "cost budget exceeded",
			err: BudgetExceededError{
				Type:     BudgetCost,
				Limit:    100,
				Current:  75,
				Required: 50,
			},
			want: "budget exceeded for cost: limit=100, current=75, required=50",
		},
		{
			name: "time budget exceeded",
			err: BudgetExceededError{
				Type:     BudgetTime,
				Limit:    300,
				Current:  250,
				Required: 100,
			},
			want: "budget exceeded for time: limit=300, current=250, required=100",
		},
		{
			name: "zero values",
			err: BudgetExceededError{
				Type:     BudgetTokens,
				Limit:    0,
				Current:  0,
				Required: 0,
			},
			want: "budget exceeded for tokens: limit=0, current=0, required=0",
		},
		{
			name: "negative values",
			err: BudgetExceededError{
				Type:     BudgetCalls,
				Limit:    -10,
				Current:  -5,
				Required: -2,
			},
			want: "budget exceeded for calls: limit=-10, current=-5, required=-2",
		},
		{
			name: "large int64 values",
			err: BudgetExceededError{
				Type:     BudgetTokens,
				Limit:    math.MaxInt64,
				Current:  math.MaxInt64 - 1000,
				Required: 2000,
			},
			want: fmt.Sprintf("budget exceeded for tokens: limit=%d, current=%d, required=2000",
				math.MaxInt64, math.MaxInt64-1000),
		},
		{
			name: "unknown budget type",
			err: BudgetExceededError{
				Type:     BudgetType(99),
				Limit:    100,
				Current:  50,
				Required: 60,
			},
			want: "budget exceeded for unknown: limit=100, current=50, required=60",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("BudgetExceededError.Error() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBudgetExceededError_OverBy(t *testing.T) {
	tests := []struct {
		name string
		err  BudgetExceededError
		want int64
	}{
		{
			name: "exactly at limit",
			err: BudgetExceededError{
				Type:     BudgetTokens,
				Limit:    1000,
				Current:  500,
				Required: 500,
			},
			want: 0,
		},
		{
			name: "just over by 1",
			err: BudgetExceededError{
				Type:     BudgetCalls,
				Limit:    10,
				Current:  5,
				Required: 6,
			},
			want: 1,
		},
		{
			name: "significantly over limit",
			err: BudgetExceededError{
				Type:     BudgetCost,
				Limit:    100,
				Current:  80,
				Required: 50,
			},
			want: 30,
		},
		{
			name: "under limit (negative overby)",
			err: BudgetExceededError{
				Type:     BudgetTime,
				Limit:    300,
				Current:  100,
				Required: 50,
			},
			want: -150,
		},
		{
			name: "zero values",
			err: BudgetExceededError{
				Type:     BudgetTokens,
				Limit:    0,
				Current:  0,
				Required: 0,
			},
			want: 0,
		},
		{
			name: "negative current",
			err: BudgetExceededError{
				Type:     BudgetCalls,
				Limit:    10,
				Current:  -5,
				Required: 20,
			},
			want: 5,
		},
		{
			name: "negative required",
			err: BudgetExceededError{
				Type:     BudgetCost,
				Limit:    100,
				Current:  120,
				Required: -10,
			},
			want: 10,
		},
		{
			name: "all negative",
			err: BudgetExceededError{
				Type:     BudgetTime,
				Limit:    -100,
				Current:  -50,
				Required: -30,
			},
			want: 20,
		},
		{
			name: "large positive values",
			err: BudgetExceededError{
				Type:     BudgetTokens,
				Limit:    math.MaxInt64 - 2000,
				Current:  math.MaxInt64 - 1500,
				Required: 1000,
			},
			want: 1500,
		},
		{
			name: "edge case near max int64",
			err: BudgetExceededError{
				Type:     BudgetTokens,
				Limit:    math.MaxInt64 - 100,
				Current:  50,
				Required: 60,
			},
			want: 210 - math.MaxInt64,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.OverBy(); got != tt.want {
				t.Errorf("BudgetExceededError.OverBy() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewBudgetExceededError(t *testing.T) {
	tests := []struct {
		name        string
		budgetType  BudgetType
		limit       int64
		current     int64
		required    int64
		wantType    BudgetType
		wantLimit   int64
		wantCurrent int64
		wantReq     int64
	}{
		{
			name:        "tokens budget error",
			budgetType:  BudgetTokens,
			limit:       1000,
			current:     800,
			required:    300,
			wantType:    BudgetTokens,
			wantLimit:   1000,
			wantCurrent: 800,
			wantReq:     300,
		},
		{
			name:        "calls budget error",
			budgetType:  BudgetCalls,
			limit:       10,
			current:     9,
			required:    2,
			wantType:    BudgetCalls,
			wantLimit:   10,
			wantCurrent: 9,
			wantReq:     2,
		},
		{
			name:        "cost budget error",
			budgetType:  BudgetCost,
			limit:       100,
			current:     75,
			required:    50,
			wantType:    BudgetCost,
			wantLimit:   100,
			wantCurrent: 75,
			wantReq:     50,
		},
		{
			name:        "time budget error",
			budgetType:  BudgetTime,
			limit:       300,
			current:     250,
			required:    100,
			wantType:    BudgetTime,
			wantLimit:   300,
			wantCurrent: 250,
			wantReq:     100,
		},
		{
			name:        "zero values",
			budgetType:  BudgetTokens,
			limit:       0,
			current:     0,
			required:    0,
			wantType:    BudgetTokens,
			wantLimit:   0,
			wantCurrent: 0,
			wantReq:     0,
		},
		{
			name:        "negative values",
			budgetType:  BudgetCalls,
			limit:       -10,
			current:     -5,
			required:    -2,
			wantType:    BudgetCalls,
			wantLimit:   -10,
			wantCurrent: -5,
			wantReq:     -2,
		},
		{
			name:        "max int64 values",
			budgetType:  BudgetCost,
			limit:       math.MaxInt64,
			current:     math.MaxInt64,
			required:    math.MaxInt64,
			wantType:    BudgetCost,
			wantLimit:   math.MaxInt64,
			wantCurrent: math.MaxInt64,
			wantReq:     math.MaxInt64,
		},
		{
			name:        "min int64 values",
			budgetType:  BudgetTime,
			limit:       math.MinInt64,
			current:     math.MinInt64,
			required:    math.MinInt64,
			wantType:    BudgetTime,
			wantLimit:   math.MinInt64,
			wantCurrent: math.MinInt64,
			wantReq:     math.MinInt64,
		},
		{
			name:        "invalid budget type",
			budgetType:  BudgetType(255),
			limit:       100,
			current:     50,
			required:    60,
			wantType:    BudgetType(255),
			wantLimit:   100,
			wantCurrent: 50,
			wantReq:     60,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewBudgetExceededError(tt.budgetType, tt.limit, tt.current, tt.required)

			if got.Type != tt.wantType {
				t.Errorf("NewBudgetExceededError().Type = %v, want %v", got.Type, tt.wantType)
			}
			if got.Limit != tt.wantLimit {
				t.Errorf("NewBudgetExceededError().Limit = %v, want %v", got.Limit, tt.wantLimit)
			}
			if got.Current != tt.wantCurrent {
				t.Errorf("NewBudgetExceededError().Current = %v, want %v", got.Current, tt.wantCurrent)
			}
			if got.Required != tt.wantReq {
				t.Errorf("NewBudgetExceededError().Required = %v, want %v", got.Required, tt.wantReq)
			}
		})
	}
}

// Test that BudgetExceededError implements the error interface
func TestBudgetExceededError_ImplementsError(t *testing.T) {
	var _ error = BudgetExceededError{}
	var _ error = &BudgetExceededError{}

	// Test that it can be used as an error
	err := NewBudgetExceededError(BudgetTokens, 100, 90, 20)
	var e error = err
	if e.Error() != err.Error() {
		t.Error("BudgetExceededError does not properly implement error interface")
	}
}

// Test basic BudgetExceededError creation and functionality (migrated from evaluation_test.go)
func TestBudgetExceededError_BasicFunctionality(t *testing.T) {
	err := NewBudgetExceededError(BudgetTokens, 1000, 900, 200)

	// Verify all fields are set correctly
	if err.Type != BudgetTokens {
		t.Errorf("BudgetExceededError.Type = %v, want %v", err.Type, BudgetTokens)
	}
	if err.Limit != 1000 {
		t.Errorf("BudgetExceededError.Limit = %v, want %v", err.Limit, 1000)
	}
	if err.Current != 900 {
		t.Errorf("BudgetExceededError.Current = %v, want %v", err.Current, 900)
	}
	if err.Required != 200 {
		t.Errorf("BudgetExceededError.Required = %v, want %v", err.Required, 200)
	}

	// Test OverBy method
	if overBy := err.OverBy(); overBy != 100 {
		t.Errorf("BudgetExceededError.OverBy() = %v, want %v", overBy, 100)
	}

	// Test error message contains all expected values
	errMsg := err.Error()
	expectedParts := []string{"budget exceeded", "tokens", "1000", "900", "200"}
	for _, part := range expectedParts {
		if !strings.Contains(errMsg, part) {
			t.Errorf("BudgetExceededError.Error() missing %q in message: %v", part, errMsg)
		}
	}
}

// Comprehensive edge case testing for all budget types
func TestBudgetType_AllValues(t *testing.T) {
	// Test all defined budget types
	definedTypes := []BudgetType{
		BudgetTokens,
		BudgetCalls,
		BudgetCost,
		BudgetTime,
	}

	expectedStrings := []string{
		"tokens",
		"calls",
		"cost",
		"time",
	}

	for i, bt := range definedTypes {
		t.Run(fmt.Sprintf("defined_type_%d", i), func(t *testing.T) {
			if bt != BudgetType(i) {
				t.Errorf("BudgetType constant value mismatch: %v != %v", bt, i)
			}
			if bt.String() != expectedStrings[i] {
				t.Errorf("BudgetType.String() = %v, want %v", bt.String(), expectedStrings[i])
			}
		})
	}

	// Test that the next value after defined types returns "unknown"
	nextType := BudgetType(len(definedTypes))
	if nextType.String() != "unknown" {
		t.Errorf("Undefined BudgetType(%d).String() = %v, want 'unknown'",
			len(definedTypes), nextType.String())
	}
}
