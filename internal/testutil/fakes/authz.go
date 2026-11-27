package fakes

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/session"
)

// Roles is an in-memory role source for authz.NewResolver: every member's grant covers every project.
type Roles struct {
	mu      sync.Mutex
	byOrg   map[uuid.UUID]map[uuid.UUID]string
	calls   int
	OrgName string
	Err     error
}

// NewRoles returns a role source with no members, naming every org "Acme".
func NewRoles() *Roles {
	return &Roles{byOrg: map[uuid.UUID]map[uuid.UUID]string{}, OrgName: "Acme"}
}

// Set makes userID hold role in orgID; an empty role removes the membership.
func (r *Roles) Set(orgID, userID uuid.UUID, role string) *Roles {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byOrg[orgID] == nil {
		r.byOrg[orgID] = map[uuid.UUID]string{}
	}
	r.byOrg[orgID][userID] = role
	return r
}

// RoleOf returns userID's role in orgID, or "" for a non-member.
func (r *Roles) RoleOf(_ context.Context, orgID, userID uuid.UUID) (role string, roleID uuid.UUID, orgName string, grant session.Grant, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.Err != nil {
		return "", uuid.Nil, "", session.Grant{}, r.Err
	}
	return r.byOrg[orgID][userID], uuid.Nil, r.OrgName, session.Grant{OrgID: orgID, All: true}, nil
}

// Calls reports how many times RoleOf ran.
func (r *Roles) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}
