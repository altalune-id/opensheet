// Package org models organizations and their memberships.
package org

import (
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	slugMinLen = 3
	slugMaxLen = 64
	nameMinLen = 1
	nameMaxLen = 200
)

var slugRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)

// Role names a fixed set of org-scoped roles.
type Role string

const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

// IsValid reports whether r is one of the enumerated roles.
func (r Role) IsValid() bool {
	switch r {
	case RoleOwner, RoleAdmin, RoleMember:
		return true
	}
	return false
}

// Org is the tenant boundary aggregate.
type Org struct {
	ID        uuid.UUID
	Slug      string
	Name      string
	OwnerID   uuid.UUID
	CreatedAt time.Time
	// System marks a bootstrap-owned org that cannot be renamed or deleted.
	System bool
}

// CreateRequest is the input to Service.Create.
type CreateRequest struct {
	Slug    string
	Name    string
	OwnerID uuid.UUID
}

// NewOrg constructs an Org after validating slug and name.
func NewOrg(slug, name string, ownerID uuid.UUID) (*Org, error) {
	s := strings.TrimSpace(slug)
	if err := validateSlug(s); err != nil {
		return nil, err
	}
	n := strings.TrimSpace(name)
	if err := validateName(n); err != nil {
		return nil, err
	}
	return &Org{
		ID:        uuid.New(),
		Slug:      s,
		Name:      n,
		OwnerID:   ownerID,
		CreatedAt: time.Now().UTC(),
	}, nil
}

// Rename applies a new display name; slug is immutable.
func (o *Org) Rename(name string) error {
	n := strings.TrimSpace(name)
	if err := validateName(n); err != nil {
		return err
	}
	o.Name = n
	return nil
}

// Membership binds a user to an org at a given role.
type Membership struct {
	OrgID     uuid.UUID
	UserID    uuid.UUID
	Role      Role
	CreatedAt time.Time
	// System marks a bootstrap-owned membership that cannot be removed.
	System bool
}

// MemberProfile is a Membership joined with the member's User identity fields.
type MemberProfile struct {
	UserID    uuid.UUID
	Email     string
	Name      string
	Role      Role
	CreatedAt time.Time
	System    bool
}

// NewMembership constructs a Membership after validating the role.
func NewMembership(orgID, userID uuid.UUID, role Role) (*Membership, error) {
	if !role.IsValid() {
		return nil, &InvalidRoleError{Role: string(role)}
	}
	return &Membership{
		OrgID:     orgID,
		UserID:    userID,
		Role:      role,
		CreatedAt: time.Now().UTC(),
	}, nil
}

func validateSlug(s string) error {
	if len(s) < slugMinLen || len(s) > slugMaxLen {
		return &InvalidSlugError{Slug: s, Reason: "length out of range"}
	}
	if !slugRe.MatchString(s) {
		return &InvalidSlugError{Slug: s, Reason: "must be lowercase alphanumeric with dashes"}
	}
	if reservedSlug(s) {
		return &InvalidSlugError{Slug: s, Reason: "reserved word"}
	}
	return nil
}

// NOTE: ServeMux prefers a literal pattern over the wildcard beside it, so a row slugged like a
// literal segment under /orgs/ would be unreachable.
func reservedSlug(s string) bool {
	return s == "new"
}

func validateName(n string) error {
	if len(n) < nameMinLen || len(n) > nameMaxLen {
		return &InvalidNameError{Reason: "length out of range"}
	}
	return nil
}
