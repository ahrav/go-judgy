package domain

import "fmt"

// PrincipalType represents the type of entity that can initiate evaluation requests.
// This supports both human users and automated service principals.
type PrincipalType string

const (
	// PrincipalUser represents human users who initiate evaluations.
	PrincipalUser PrincipalType = "user"

	// PrincipalService represents automated services or systems.
	PrincipalService PrincipalType = "service"
)

// Principal represents an entity (user or service) that can request evaluations.
// This flexible design supports both human users and service principals,
// avoiding the limitation of email-only identification.
type Principal struct {
	// Type indicates whether this is a user or service principal.
	Type PrincipalType `json:"type" validate:"required,oneof=user service"`

	// ID uniquely identifies the principal.
	// For users: email address, username, or user ID
	// For services: service name, URN, or service account identifier
	ID string `json:"id" validate:"required,min=1"`
}

// String returns a human-readable representation of the principal.
func (p Principal) String() string { return fmt.Sprintf("%s:%s", p.Type, p.ID) }
