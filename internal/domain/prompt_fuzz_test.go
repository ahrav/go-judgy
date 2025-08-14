package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzNewPromptSpec tests NewPromptSpec with various string inputs to find edge cases
func FuzzNewPromptSpec(f *testing.F) {
	// Seed corpus with interesting edge cases
	f.Add("", "", "")                                                          // Empty strings
	f.Add("Hello {{.name}}", "world", "Hello world")                           // Basic template
	f.Add("🌍{{.emoji}}🚀", "⭐", "🌍⭐🚀")                                          // Unicode content
	f.Add("Line1\nLine2\nLine3", "test", "Line1\nLine2\nLine3")                // Multiline
	f.Add("Special: \x00\x01\x02", "binary", "Special: \x00\x01\x02")          // Binary content
	f.Add(strings.Repeat("a", 1000), "large", strings.Repeat("a", 1000))       // Large strings
	f.Add("Tab\tSpace NewLine\n", "whitespace", "Tab\tSpace NewLine\n")        // Whitespace
	f.Add("\"quotes\"'apostrophes'", "punctuation", "\"quotes\"'apostrophes'") // Punctuation
	f.Add("{{.nested}}{{.template}}", "complex", "value1value2")               // Complex templates

	f.Fuzz(func(t *testing.T, template, varValue, renderedContent string) {
		// Create a valid ArtifactRef for testing
		validRef := ArtifactRef{
			Key:  "fuzz-test.txt",
			Size: int64(len(renderedContent)),
			Kind: ArtifactRawPrompt,
		}

		// Create variables map with a single entry
		variables := map[string]string{"key": varValue}

		// Test NewPromptSpec
		spec, err := NewPromptSpec(template, variables, renderedContent, validRef)

		if err != nil {
			// Errors are expected for invalid inputs, but they should be well-formed
			if err.Error() == "" {
				t.Errorf("Error should have a non-empty message")
			}
			return
		}

		// For successful cases, validate invariants

		// 1. Template should be preserved exactly
		if spec.Template != template {
			t.Errorf("Template not preserved: got %q, want %q", spec.Template, template)
		}

		// 2. Variables should be preserved exactly
		if spec.Variables == nil {
			t.Errorf("Variables should not be nil when input was not nil")
		} else if spec.Variables["key"] != varValue {
			t.Errorf("Variable value not preserved: got %q, want %q", spec.Variables["key"], varValue)
		}

		// 3. Hash should be consistent with rendered content
		expectedHasher := sha256.New()
		expectedHasher.Write([]byte(renderedContent))
		expectedHash := hex.EncodeToString(expectedHasher.Sum(nil))

		if spec.Hash != expectedHash {
			t.Errorf("Hash mismatch: got %q, want %q", spec.Hash, expectedHash)
		}

		// 4. RenderedRef should be preserved
		if spec.RenderedRef != validRef {
			t.Errorf("RenderedRef not preserved")
		}

		// 5. Hash should be deterministic - create again and verify
		spec2, err2 := NewPromptSpec(template, variables, renderedContent, validRef)
		if err2 != nil {
			t.Errorf("Second creation failed: %v", err2)
		} else if spec.Hash != spec2.Hash {
			t.Errorf("Hash not deterministic: first=%q, second=%q", spec.Hash, spec2.Hash)
		}

		// 6. Different content should produce different hashes (when possible)
		if len(renderedContent) > 0 {
			modifiedContent := renderedContent + "x"
			validRef2 := ArtifactRef{
				Key:  "fuzz-test-2.txt",
				Size: int64(len(modifiedContent)),
				Kind: ArtifactRawPrompt,
			}

			spec3, err3 := NewPromptSpec(template, variables, modifiedContent, validRef2)
			if err3 == nil && spec.Hash == spec3.Hash {
				t.Errorf("Different content produced same hash: %q", spec.Hash)
			}
		}

		// 7. Validate that the result passes validation
		if validateErr := spec.Validate(); validateErr != nil {
			t.Errorf("Created spec failed validation: %v", validateErr)
		}
	})
}

// FuzzNewPromptSpec_VariableMap tests NewPromptSpec with various map configurations
func FuzzNewPromptSpec_VariableMap(f *testing.F) {
	// Seed corpus with different map patterns
	f.Add("key1", "value1", "key2", "value2")                                        // Two entries
	f.Add("", "", "", "")                                                            // Empty keys/values
	f.Add("unicode🌍", "value🚀", "normal", "val")                                     // Unicode keys/values
	f.Add("long"+strings.Repeat("a", 100), "short", "key", strings.Repeat("b", 500)) // Long values

	f.Fuzz(func(t *testing.T, key1, value1, key2, value2 string) {
		template := "Template with {{." + key1 + "}} and {{." + key2 + "}}"
		renderedContent := "Template with " + value1 + " and " + value2

		// Create variables map
		variables := map[string]string{
			key1: value1,
			key2: value2,
		}

		validRef := ArtifactRef{
			Key:  "fuzz-map-test.txt",
			Size: int64(len(renderedContent)),
			Kind: ArtifactRawPrompt,
		}

		spec, err := NewPromptSpec(template, variables, renderedContent, validRef)
		if err != nil {
			// Errors are acceptable for invalid inputs
			return
		}

		// Verify map cloning - modify original and check spec is unaffected
		originalValue1 := variables[key1]
		originalValue2 := variables[key2]

		variables[key1] = "modified1"
		variables[key2] = "modified2"
		variables["new_key"] = "new_value"

		// Spec should be unaffected
		if spec.Variables[key1] != originalValue1 {
			t.Errorf("Variables not properly cloned: key1 modified")
		}
		if spec.Variables[key2] != originalValue2 {
			t.Errorf("Variables not properly cloned: key2 modified")
		}
		if _, exists := spec.Variables["new_key"]; exists {
			t.Errorf("Variables not properly cloned: new key appeared")
		}

		// Verify map has expected entries
		// When key1 == key2, the map will only have 1 entry
		expectedLen := 2
		if key1 == key2 {
			expectedLen = 1
		}
		if len(spec.Variables) != expectedLen {
			t.Errorf("Variables map has wrong length: got %d, want %d", len(spec.Variables), expectedLen)
		}
	})
}

// FuzzPromptSpec_Validate tests the Validate method with various struct configurations
func FuzzPromptSpec_Validate(f *testing.F) {
	// Seed with valid and edge case configurations
	f.Add("template", "hash", "key", int64(100))
	f.Add("", "", "", int64(0))    // Empty values
	f.Add("t", "h", "k", int64(1)) // Minimal values

	f.Fuzz(func(t *testing.T, template, hash, key string, size int64) {
		// Create ArtifactRef with fuzzed values
		artifactRef := ArtifactRef{
			Key:  key,
			Size: size,
			Kind: ArtifactRawPrompt,
		}

		spec := PromptSpec{
			Template:    template,
			Variables:   map[string]string{"test": "value"},
			Hash:        hash,
			RenderedRef: artifactRef,
		}

		err := spec.Validate()

		// Check that validation behaves consistently
		err2 := spec.Validate()
		if (err == nil) != (err2 == nil) {
			t.Errorf("Validate() not deterministic: first=%v, second=%v", err, err2)
		}

		// If validation passes, the struct should meet basic requirements
		if err == nil {
			if spec.Template == "" {
				t.Errorf("Validation passed but Template is empty")
			}
			if spec.Hash == "" {
				t.Errorf("Validation passed but Hash is empty")
			}
			if spec.RenderedRef.Key == "" {
				t.Errorf("Validation passed but RenderedRef.Key is empty")
			}
			if spec.RenderedRef.Size < 0 {
				t.Errorf("Validation passed but RenderedRef.Size is negative: %d", spec.RenderedRef.Size)
			}
			if spec.RenderedRef.Kind != ArtifactRawPrompt {
				t.Errorf("Validation passed but RenderedRef.Kind is wrong: %q", spec.RenderedRef.Kind)
			}
		}
	})
}

// FuzzNewPromptSpec_UTF8 specifically tests UTF-8 handling and edge cases
func FuzzNewPromptSpec_UTF8(f *testing.F) {
	// Seed with various UTF-8 edge cases
	f.Add("\xFF\xFE")     // Invalid UTF-8
	f.Add("\xED\xA0\x80") // Invalid UTF-8 (surrogate)
	f.Add("🌍🚀⭐🎉")         // Valid multi-byte UTF-8
	f.Add("\u0000")       // Null character
	f.Add("\u200B")       // Zero-width space
	f.Add("\uFFFD")       // Replacement character

	f.Fuzz(func(t *testing.T, input string) {
		// Use the fuzzed string as template, variable value, and rendered content
		variables := map[string]string{"key": input}

		validRef := ArtifactRef{
			Key:  "utf8-test.txt",
			Size: int64(len(input)),
			Kind: ArtifactRawPrompt,
		}

		spec, err := NewPromptSpec(input, variables, input, validRef)
		if err != nil {
			// Errors are acceptable for invalid inputs
			return
		}

		// For successful cases, verify UTF-8 handling
		if !utf8.ValidString(spec.Template) && utf8.ValidString(input) {
			t.Errorf("Template corrupted UTF-8: input was valid, result is not")
		}

		if !utf8.ValidString(spec.Variables["key"]) && utf8.ValidString(input) {
			t.Errorf("Variable value corrupted UTF-8: input was valid, result is not")
		}

		// Hash should always be valid hex
		if spec.Hash != "" {
			for _, r := range spec.Hash {
				if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
					t.Errorf("Hash contains invalid hex character: %q in %q", r, spec.Hash)
				}
			}
			// SHA-256 hex should be exactly 64 characters
			if len(spec.Hash) != 64 {
				t.Errorf("Hash has wrong length: got %d, want 64", len(spec.Hash))
			}
		}
	})
}
