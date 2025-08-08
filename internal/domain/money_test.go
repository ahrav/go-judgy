package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCents_String(t *testing.T) {
	tests := []struct {
		name     string
		cents    Cents
		expected string
	}{
		{
			name:     "zero cents",
			cents:    Cents(0),
			expected: "$0.00",
		},
		{
			name:     "one cent",
			cents:    Cents(1),
			expected: "$0.01",
		},
		{
			name:     "ten cents",
			cents:    Cents(10),
			expected: "$0.10",
		},
		{
			name:     "one dollar",
			cents:    Cents(100),
			expected: "$1.00",
		},
		{
			name:     "one dollar fifty cents",
			cents:    Cents(150),
			expected: "$1.50",
		},
		{
			name:     "large amount",
			cents:    Cents(999999),
			expected: "$9999.99",
		},
		{
			name:     "negative amount",
			cents:    Cents(-150),
			expected: "$-1.50",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.cents.String())
		})
	}
}

func TestCents_IsZero(t *testing.T) {
	tests := []struct {
		name     string
		cents    Cents
		expected bool
	}{
		{
			name:     "zero is zero",
			cents:    Cents(0),
			expected: true,
		},
		{
			name:     "positive is not zero",
			cents:    Cents(1),
			expected: false,
		},
		{
			name:     "negative is not zero",
			cents:    Cents(-1),
			expected: false,
		},
		{
			name:     "large positive is not zero",
			cents:    Cents(1000),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.cents.IsZero())
		})
	}
}

func TestCents_Add(t *testing.T) {
	tests := []struct {
		name     string
		cents    Cents
		other    Cents
		expected Cents
	}{
		{
			name:     "add zero to zero",
			cents:    Cents(0),
			other:    Cents(0),
			expected: Cents(0),
		},
		{
			name:     "add positive to zero",
			cents:    Cents(0),
			other:    Cents(100),
			expected: Cents(100),
		},
		{
			name:     "add two positives",
			cents:    Cents(150),
			other:    Cents(250),
			expected: Cents(400),
		},
		{
			name:     "add negative to positive",
			cents:    Cents(100),
			other:    Cents(-50),
			expected: Cents(50),
		},
		{
			name:     "add two negatives",
			cents:    Cents(-100),
			other:    Cents(-50),
			expected: Cents(-150),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.cents.Add(tt.other)
			assert.Equal(t, tt.expected, result)
		})
	}
}
