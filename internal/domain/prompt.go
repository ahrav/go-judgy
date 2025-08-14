package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// PromptSpec defines a reproducible prompt configuration for auditing and debugging.
// It ensures prompt reproducibility by capturing both the template and rendered output,
// enabling audit trails and debugging of evaluation results.
type PromptSpec struct {
	// Template is the base prompt template with placeholders.
	// Should use standard templating syntax for variable substitution.
	Template string `json:"template" validate:"required"`

	// Variables contains the values used to fill template placeholders.
	// Keys must match placeholder names in the template.
	Variables map[string]string `json:"variables"`

	// Hash is a deterministic hash of the final rendered prompt.
	// Used to ensure reproducibility and detect template changes.
	Hash string `json:"hash" validate:"required"`

	// RenderedRef references the final rendered prompt stored as an artifact.
	// The artifact Kind must be "raw_prompt".
	RenderedRef ArtifactRef `json:"rendered_ref" validate:"required"`
}

// Validate checks if the prompt specification meets all requirements.
// Returns nil if valid, or a validation error describing the first constraint violation.
func (p *PromptSpec) Validate() error { return validate.Struct(p) }

// NewPromptSpec creates a new PromptSpec with computed hash and validation.
// It ensures integrity by computing the hash from the rendered content and
// validates that the RenderedRef.Kind is ArtifactRawPrompt.
//
// The hash is computed as SHA-256 of the rendered prompt content for reproducibility.
// Variables map is cloned to prevent aliasing and mutation issues.
//
// Returns an error if:
//   - RenderedRef.Kind is not ArtifactRawPrompt
//   - Rendered content cannot be accessed (future implementation)
//   - Validation constraints are violated
func NewPromptSpec(template string, variables map[string]string, renderedContent string, renderedRef ArtifactRef) (PromptSpec, error) {
	// Validate that the artifact reference is for a raw prompt
	if renderedRef.Kind != ArtifactRawPrompt {
		return PromptSpec{}, fmt.Errorf("rendered_ref.kind must be %q, got %q: %w", ArtifactRawPrompt, renderedRef.Kind, ErrInvalidArtifactKind)
	}

	hasher := sha256.New()
	hasher.Write([]byte(renderedContent))
	hash := hex.EncodeToString(hasher.Sum(nil))

	// Clone variables to prevent aliasing issues.
	varsCopy := cloneStringMap(variables)

	spec := PromptSpec{
		Template:    template,
		Variables:   varsCopy,
		Hash:        hash,
		RenderedRef: renderedRef,
	}

	if err := spec.Validate(); err != nil {
		return PromptSpec{}, fmt.Errorf("%w: %w", ErrInvalidPromptSpec, err)
	}

	return spec, nil
}
