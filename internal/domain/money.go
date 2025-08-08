package domain

import "fmt"

// Cents represents monetary values in cents (1/100 of a dollar).
// Using cents avoids floating-point precision issues while providing
// type safety for monetary operations throughout the system.
type Cents int64

const (
	// CentsPerDollar represents the number of cents in a dollar.
	CentsPerDollar = 100
)

// String formats cents as a dollar amount (e.g., 150 → "$1.50").
func (c Cents) String() string { return fmt.Sprintf("$%.2f", float64(c)/CentsPerDollar) }

// IsZero returns true if the amount is zero.
func (c Cents) IsZero() bool { return c == 0 }

// Add returns the sum of two cent amounts.
func (c Cents) Add(x Cents) Cents { return c + x }
