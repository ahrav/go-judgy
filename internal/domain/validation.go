package domain

import (
	"maps"

	"github.com/go-playground/validator/v10"
)

// validate is the package-level validator instance used for struct validation.
var validate = validator.New(validator.WithRequiredStructEnabled())

// cloneStringMap creates a deep copy of a string map to prevent aliasing.
// Returns nil for nil input to maintain consistency.
func cloneStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	result := make(map[string]string, len(m))
	maps.Copy(result, m)
	return result
}
