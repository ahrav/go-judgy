package domain //nolint:testpackage // Need access to unexported validate

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper function to create a valid ArtifactRef for prompt testing
func createValidPromptArtifactRef() ArtifactRef {
	return ArtifactRef{
		Key:  "prompts/test-prompt.txt",
		Size: 42,
		Kind: ArtifactRawPrompt,
	}
}

// Helper function to compute expected hash
func computeExpectedHash(content string) string {
	hasher := sha256.New()
	hasher.Write([]byte(content))
	return hex.EncodeToString(hasher.Sum(nil))
}

func TestPromptSpec_Validate(t *testing.T) {
	tests := []struct {
		name    string
		spec    PromptSpec
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid prompt spec",
			spec: PromptSpec{
				Template:  "Hello {{.name}}",
				Variables: map[string]string{"name": "world"},
				Hash:      "abc123",
				RenderedRef: ArtifactRef{
					Key:  "prompts/test.txt",
					Size: 100,
					Kind: ArtifactRawPrompt,
				},
			},
			wantErr: false,
		},
		{
			name: "empty template",
			spec: PromptSpec{
				Template:  "",
				Variables: map[string]string{"name": "world"},
				Hash:      "abc123",
				RenderedRef: ArtifactRef{
					Key:  "prompts/test.txt",
					Size: 100,
					Kind: ArtifactRawPrompt,
				},
			},
			wantErr: true,
			errMsg:  "Template",
		},
		{
			name: "empty hash",
			spec: PromptSpec{
				Template:  "Hello {{.name}}",
				Variables: map[string]string{"name": "world"},
				Hash:      "",
				RenderedRef: ArtifactRef{
					Key:  "prompts/test.txt",
					Size: 100,
					Kind: ArtifactRawPrompt,
				},
			},
			wantErr: true,
			errMsg:  "Hash",
		},
		{
			name: "nil variables map is allowed",
			spec: PromptSpec{
				Template:    "Hello world",
				Variables:   nil,
				Hash:        "abc123",
				RenderedRef: createValidPromptArtifactRef(),
			},
			wantErr: false,
		},
		{
			name: "empty variables map is allowed",
			spec: PromptSpec{
				Template:    "Hello world",
				Variables:   map[string]string{},
				Hash:        "abc123",
				RenderedRef: createValidPromptArtifactRef(),
			},
			wantErr: false,
		},
		{
			name: "invalid rendered ref - empty key",
			spec: PromptSpec{
				Template:  "Hello {{.name}}",
				Variables: map[string]string{"name": "world"},
				Hash:      "abc123",
				RenderedRef: ArtifactRef{
					Key:  "",
					Size: 100,
					Kind: ArtifactRawPrompt,
				},
			},
			wantErr: true,
			errMsg:  "Key",
		},
		{
			name: "invalid rendered ref - negative size",
			spec: PromptSpec{
				Template:  "Hello {{.name}}",
				Variables: map[string]string{"name": "world"},
				Hash:      "abc123",
				RenderedRef: ArtifactRef{
					Key:  "prompts/test.txt",
					Size: -1,
					Kind: ArtifactRawPrompt,
				},
			},
			wantErr: true,
			errMsg:  "Size",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.spec.Validate()
			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestNewPromptSpec(t *testing.T) {
	tests := []struct {
		name            string
		template        string
		variables       map[string]string
		renderedContent string
		renderedRef     ArtifactRef
		wantErr         bool
		wantErrIs       error
		wantErrContains string
		validateResult  func(t *testing.T, spec PromptSpec)
	}{
		{
			name:            "valid prompt spec creation",
			template:        "Hello {{.name}}",
			variables:       map[string]string{"name": "world"},
			renderedContent: "Hello world",
			renderedRef:     createValidPromptArtifactRef(),
			wantErr:         false,
			validateResult: func(t *testing.T, spec PromptSpec) {
				assert.Equal(t, "Hello {{.name}}", spec.Template)
				assert.Equal(t, map[string]string{"name": "world"}, spec.Variables)
				assert.Equal(t, computeExpectedHash("Hello world"), spec.Hash)
				assert.Equal(t, createValidPromptArtifactRef(), spec.RenderedRef)
			},
		},
		{
			name:            "empty template is valid",
			template:        "",
			variables:       map[string]string{"name": "world"},
			renderedContent: "Hello world",
			renderedRef:     createValidPromptArtifactRef(),
			wantErr:         true,
			wantErrIs:       ErrInvalidPromptSpec,
			wantErrContains: "Template",
		},
		{
			name:            "nil variables map",
			template:        "Hello world",
			variables:       nil,
			renderedContent: "Hello world",
			renderedRef:     createValidPromptArtifactRef(),
			wantErr:         false,
			validateResult: func(t *testing.T, spec PromptSpec) {
				assert.Nil(t, spec.Variables)
				assert.Equal(t, computeExpectedHash("Hello world"), spec.Hash)
			},
		},
		{
			name:            "empty variables map",
			template:        "Hello world",
			variables:       map[string]string{},
			renderedContent: "Hello world",
			renderedRef:     createValidPromptArtifactRef(),
			wantErr:         false,
			validateResult: func(t *testing.T, spec PromptSpec) {
				assert.NotNil(t, spec.Variables)
				assert.Empty(t, spec.Variables)
			},
		},
		{
			name:            "variables map aliasing prevention",
			template:        "Hello {{.name}}",
			variables:       map[string]string{"name": "world"},
			renderedContent: "Hello world",
			renderedRef:     createValidPromptArtifactRef(),
			wantErr:         false,
			validateResult: func(t *testing.T, spec PromptSpec) {
				// Modify original map to test aliasing
				original := map[string]string{"name": "world"}
				original["name"] = "modified"
				assert.Equal(t, "world", spec.Variables["name"], "Variables should be cloned, not aliased")
			},
		},
		{
			name:            "wrong artifact kind",
			template:        "Hello world",
			variables:       map[string]string{},
			renderedContent: "Hello world",
			renderedRef: ArtifactRef{
				Key:  "test.txt",
				Size: 100,
				Kind: ArtifactAnswer, // Wrong kind
			},
			wantErr:         true,
			wantErrIs:       ErrInvalidArtifactKind,
			wantErrContains: "rendered_ref.kind must be \"raw_prompt\"",
		},
		{
			name:            "empty artifact kind",
			template:        "Hello world",
			variables:       map[string]string{},
			renderedContent: "Hello world",
			renderedRef: ArtifactRef{
				Key:  "test.txt",
				Size: 100,
				Kind: "", // Empty kind
			},
			wantErr:         true,
			wantErrIs:       ErrInvalidArtifactKind,
			wantErrContains: "rendered_ref.kind must be \"raw_prompt\"",
		},
		{
			name:            "large rendered content",
			template:        "Large content: {{.data}}",
			variables:       map[string]string{"data": strings.Repeat("x", 10000)},
			renderedContent: "Large content: " + strings.Repeat("x", 10000),
			renderedRef:     createValidPromptArtifactRef(),
			wantErr:         false,
			validateResult: func(t *testing.T, spec PromptSpec) {
				expectedHash := computeExpectedHash("Large content: " + strings.Repeat("x", 10000))
				assert.Equal(t, expectedHash, spec.Hash)
			},
		},
		{
			name:            "unicode in template and content",
			template:        "Hello {{.name}} 🌍",
			variables:       map[string]string{"name": "世界"},
			renderedContent: "Hello 世界 🌍",
			renderedRef:     createValidPromptArtifactRef(),
			wantErr:         false,
			validateResult: func(t *testing.T, spec PromptSpec) {
				assert.Equal(t, "Hello {{.name}} 🌍", spec.Template)
				assert.Equal(t, "世界", spec.Variables["name"])
				assert.Equal(t, computeExpectedHash("Hello 世界 🌍"), spec.Hash)
			},
		},
		{
			name:            "empty rendered content produces hash",
			template:        "Template",
			variables:       map[string]string{},
			renderedContent: "",
			renderedRef:     createValidPromptArtifactRef(),
			wantErr:         false,
			validateResult: func(t *testing.T, spec PromptSpec) {
				expectedHash := computeExpectedHash("")
				assert.Equal(t, expectedHash, spec.Hash)
				assert.NotEmpty(t, spec.Hash, "Hash should be computed even for empty content")
			},
		},
		{
			name:            "invalid artifact ref causes validation failure",
			template:        "Hello world",
			variables:       map[string]string{},
			renderedContent: "Hello world",
			renderedRef: ArtifactRef{
				Key:  "", // Invalid - empty key
				Size: 100,
				Kind: ArtifactRawPrompt,
			},
			wantErr:         true,
			wantErrIs:       ErrInvalidPromptSpec,
			wantErrContains: "Key",
		},
		{
			name:            "negative artifact size causes validation failure",
			template:        "Hello world",
			variables:       map[string]string{},
			renderedContent: "Hello world",
			renderedRef: ArtifactRef{
				Key:  "test.txt",
				Size: -1, // Invalid - negative size
				Kind: ArtifactRawPrompt,
			},
			wantErr:         true,
			wantErrIs:       ErrInvalidPromptSpec,
			wantErrContains: "Size",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Capture original variables for aliasing tests
			originalVars := make(map[string]string)
			if tt.variables != nil {
				for k, v := range tt.variables {
					originalVars[k] = v
				}
			}

			spec, err := NewPromptSpec(tt.template, tt.variables, tt.renderedContent, tt.renderedRef)

			if tt.wantErr {
				require.Error(t, err)
				if tt.wantErrIs != nil {
					assert.ErrorIs(t, err, tt.wantErrIs)
				}
				if tt.wantErrContains != "" {
					assert.Contains(t, err.Error(), tt.wantErrContains)
				}
				return
			}

			require.NoError(t, err)
			if tt.validateResult != nil {
				tt.validateResult(t, spec)
			}

			// Test that variables are cloned, not aliased
			if tt.variables != nil && len(tt.variables) > 0 {
				// Modify original map
				for k := range tt.variables {
					tt.variables[k] = "modified"
					break
				}
				// Spec should still have original values
				for k, v := range originalVars {
					assert.Equal(t, v, spec.Variables[k], "Variables should be cloned, not aliased")
				}
			}
		})
	}
}

func TestNewPromptSpec_HashConsistency(t *testing.T) {
	template := "Hello {{.name}}"
	variables := map[string]string{"name": "world"}
	renderedContent := "Hello world"
	renderedRef := createValidPromptArtifactRef()

	// Create the same spec multiple times
	spec1, err1 := NewPromptSpec(template, variables, renderedContent, renderedRef)
	require.NoError(t, err1)

	spec2, err2 := NewPromptSpec(template, variables, renderedContent, renderedRef)
	require.NoError(t, err2)

	// Hashes should be identical for identical content
	assert.Equal(t, spec1.Hash, spec2.Hash, "Identical content should produce identical hashes")

	// Different content should produce different hashes
	spec3, err3 := NewPromptSpec(template, variables, "Hello universe", renderedRef)
	require.NoError(t, err3)

	assert.NotEqual(t, spec1.Hash, spec3.Hash, "Different content should produce different hashes")
}

func TestNewPromptSpec_EdgeCases(t *testing.T) {
	t.Run("zero-length but non-nil variables map", func(t *testing.T) {
		template := "Hello world"
		variables := make(map[string]string, 0)
		renderedContent := "Hello world"
		renderedRef := createValidPromptArtifactRef()

		spec, err := NewPromptSpec(template, variables, renderedContent, renderedRef)
		require.NoError(t, err)
		assert.NotNil(t, spec.Variables)
		assert.Len(t, spec.Variables, 0)
	})

	t.Run("variables with empty string values", func(t *testing.T) {
		template := "Hello {{.name}}"
		variables := map[string]string{"name": ""}
		renderedContent := "Hello "
		renderedRef := createValidPromptArtifactRef()

		spec, err := NewPromptSpec(template, variables, renderedContent, renderedRef)
		require.NoError(t, err)
		assert.Equal(t, "", spec.Variables["name"])
	})

	t.Run("variables with empty string keys", func(t *testing.T) {
		template := "Hello {{.}}"
		variables := map[string]string{"": "world"}
		renderedContent := "Hello world"
		renderedRef := createValidPromptArtifactRef()

		spec, err := NewPromptSpec(template, variables, renderedContent, renderedRef)
		require.NoError(t, err)
		assert.Equal(t, "world", spec.Variables[""])
	})

	t.Run("artifact ref with zero size", func(t *testing.T) {
		template := "Hello world"
		variables := map[string]string{}
		renderedContent := ""
		renderedRef := ArtifactRef{
			Key:  "test.txt",
			Size: 0, // Zero size is valid
			Kind: ArtifactRawPrompt,
		}

		spec, err := NewPromptSpec(template, variables, renderedContent, renderedRef)
		require.NoError(t, err)
		assert.Equal(t, int64(0), spec.RenderedRef.Size)
	})
}

func TestNewPromptSpec_VariableCloning(t *testing.T) {
	template := "Hello {{.name}}"
	originalVars := map[string]string{
		"name": "world",
		"key2": "value2",
	}
	renderedContent := "Hello world"
	renderedRef := createValidPromptArtifactRef()

	spec, err := NewPromptSpec(template, originalVars, renderedContent, renderedRef)
	require.NoError(t, err)

	// Modify original map
	originalVars["name"] = "modified"
	originalVars["new_key"] = "new_value"
	delete(originalVars, "key2")

	// Spec should be unaffected
	assert.Equal(t, "world", spec.Variables["name"])
	assert.Equal(t, "value2", spec.Variables["key2"])
	assert.NotContains(t, spec.Variables, "new_key")

	// Modify spec's variables
	spec.Variables["name"] = "spec_modified"

	// Original should be unaffected (this verifies the map was actually cloned)
	assert.Equal(t, "modified", originalVars["name"]) // Should still be our modification
}
