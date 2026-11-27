package fakes

import (
	"context"
	"slices"
	"sync"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	customrole "altalune.id/opensheet/internal/role"
)

// Roles is an in-memory role source for authz.NewResolver: every member's grant covers every project.
type Roles struct {
	mu        sync.Mutex
	byOrg     map[uuid.UUID]map[uuid.UUID]held
	custom    map[uuid.UUID][]authz.Permission
	calls     int
	OrgName   string
	Err       error
	CustomErr error
}

type held struct {
	role   string
	roleID uuid.UUID
}

// NewRoles returns a role source with no members, naming every org "Acme".
func NewRoles() *Roles {
	return &Roles{byOrg: map[uuid.UUID]map[uuid.UUID]held{}, custom: map[uuid.UUID][]authz.Permission{}, OrgName: "Acme"}
}

// Set makes userID hold role in orgID; an empty role removes the membership.
func (r *Roles) Set(orgID, userID uuid.UUID, role string) *Roles {
	return r.set(orgID, userID, held{role: role})
}

// SetCustom makes userID hold the custom role roleID in orgID, and makes that role hold perms.
func (r *Roles) SetCustom(orgID, userID, roleID uuid.UUID, perms ...authz.Permission) *Roles {
	r.mu.Lock()
	r.custom[roleID] = slices.Clone(perms)
	r.mu.Unlock()
	return r.set(orgID, userID, held{role: "custom", roleID: roleID})
}

// Revise replaces the permissions of the custom role roleID.
func (r *Roles) Revise(roleID uuid.UUID, perms ...authz.Permission) *Roles {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.custom[roleID] = slices.Clone(perms)
	return r
}

func (r *Roles) set(orgID, userID uuid.UUID, h held) *Roles {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byOrg[orgID] == nil {
		r.byOrg[orgID] = map[uuid.UUID]held{}
	}
	r.byOrg[orgID][userID] = h
	return r
}

// RoleOf returns userID's role and custom role id in orgID, or "" for a non-member.
func (r *Roles) RoleOf(_ context.Context, orgID, userID uuid.UUID) (role string, roleID uuid.UUID, orgName string, grant session.Grant, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.Err != nil {
		return "", uuid.Nil, "", session.Grant{}, r.Err
	}
	h := r.byOrg[orgID][userID]
	return h.role, h.roleID, r.OrgName, session.Grant{OrgID: orgID, All: true}, nil
}

// CustomPermissions returns the permissions SetCustom or Revise gave roleID, or *customrole.NotFoundError.
func (r *Roles) CustomPermissions(_ context.Context, roleID uuid.UUID) ([]authz.Permission, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.CustomErr != nil {
		return nil, r.CustomErr
	}
	perms, ok := r.custom[roleID]
	if !ok {
		return nil, &customrole.NotFoundError{ID: roleID.String()}
	}
	return slices.Clone(perms), nil
}

// Calls reports how many times RoleOf ran.
func (r *Roles) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}
