// Package role holds an org's custom roles: named sets of catalogue permissions.
package role

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/authz"
)

// MaxNameLen is the longest role name, in characters.
const MaxNameLen = 40

// Permission is a catalogue permission a custom role holds.
type Permission = authz.Permission

// Role is a custom role: a named set of catalogue permissions inside one org.
type Role struct {
	ID          uuid.UUID
	OrgID       uuid.UUID
	Name        string
	Description string
	Permissions []Permission
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Version     int64
}

// New validates a custom role for orgID.
func New(orgID uuid.UUID, name, description string, perms []Permission, now time.Time) (*Role, error) {
	r := &Role{ID: uuid.New(), OrgID: orgID, CreatedAt: now.UTC(), Version: 1}
	if err := r.Revise(name, description, perms, now); err != nil {
		return nil, err
	}
	return r, nil
}

// Revise replaces the role's name, description and permissions after validating them.
func (r *Role) Revise(name, description string, perms []Permission, now time.Time) error {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > MaxNameLen {
		return &InvalidNameError{Name: name}
	}
	normalized, err := normalize(perms)
	if err != nil {
		return err
	}
	r.Name, r.Description, r.Permissions, r.UpdatedAt = name, strings.TrimSpace(description), normalized, now.UTC()
	return nil
}

// SECURITY: only exact catalogue entries pass, so a wildcard or unknown name never reaches a held set.
func normalize(perms []Permission) ([]Permission, error) {
	out := make([]Permission, 0, len(perms))
	for _, d := range authz.Catalogue() {
		if slices.Contains(perms, d.Permission) {
			out = append(out, d.Permission)
		}
	}
	for _, p := range perms {
		if !slices.Contains(out, p) {
			return nil, &InvalidPermissionError{Permission: string(p)}
		}
	}
	return out, nil
}
