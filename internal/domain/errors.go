package domain

import "errors"

// ErrInvalidRequest indicates that an evaluation request contains invalid data.
var ErrInvalidRequest = errors.New("invalid evaluation request")

// ErrInvalidConfig indicates that the evaluation configuration is invalid.
var ErrInvalidConfig = errors.New("invalid evaluation configuration")

// ErrInvalidBudget indicates that budget limits are invalid or insufficient.
var ErrInvalidBudget = errors.New("invalid budget limits")

// ErrInvalidArtifactKind indicates that the artifact kind is not valid for the operation.
var ErrInvalidArtifactKind = errors.New("invalid artifact kind")

// ErrInvalidPromptSpec indicates that the prompt specification validation failed.
var ErrInvalidPromptSpec = errors.New("prompt spec validation failed")
