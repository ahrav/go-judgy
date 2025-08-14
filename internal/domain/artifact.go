package domain

import "fmt"

// ArtifactKind represents the type of content stored in an artifact.
// Using typed constants instead of raw strings provides compile-time safety
// and prevents typos that could bypass validation.
type ArtifactKind string

const (
	// ArtifactAnswer represents LLM-generated answer content.
	ArtifactAnswer ArtifactKind = "answer"

	// ArtifactJudgeRationale represents scoring rationale from evaluation judges.
	ArtifactJudgeRationale ArtifactKind = "judge_rationale"

	// ArtifactRawPrompt represents rendered prompt text sent to LLMs.
	ArtifactRawPrompt ArtifactKind = "raw_prompt"
)

// ArtifactRef represents a reference to content stored in the blob/artifact store.
// This design keeps evaluation process audit trail lightweight by storing large text content
// externally while maintaining references for audit trails and content retrieval.
type ArtifactRef struct {
	// Key is the unique identifier for the stored artifact (e.g., "answers/2025/08/<id>.txt").
	// Must be a valid storage key path for the configured blob store.
	Key string `json:"key" validate:"required"`

	// Size is the size of the stored content in bytes.
	// Used for storage accounting and retrieval optimization.
	Size int64 `json:"size" validate:"min=0"`

	// Kind categorizes the type of content stored.
	// Must be one of the defined ArtifactKind constants.
	Kind ArtifactKind `json:"kind" validate:"required,oneof=answer judge_rationale raw_prompt"`
}

// Validate checks if the artifact reference meets all requirements.
// Returns nil if valid, or a validation error describing the first constraint violation.
func (a ArtifactRef) Validate() error {
	// For explicit validation, require all essential fields or none (zero struct not allowed).
	if a.Key == "" {
		return fmt.Errorf("key: 'ArtifactRef.Key' Error:Field validation for 'Key' failed on the 'required' tag")
	}
	if a.Kind == "" {
		return fmt.Errorf("key: 'ArtifactRef.Kind' Error:Field validation for 'Kind' failed on the 'required' tag")
	}

	return validate.Struct(a)
}

// IsZero reports whether the artifact reference has no meaningful value set.
// This enables value semantics while preserving JSON omitempty behavior on
// embedding structs, as encoding/json treats types with IsZero as empty.
func (a ArtifactRef) IsZero() bool { return a.Key == "" && a.Size == 0 && a.Kind == "" }
