package fakes

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/org"
)

// Members is an in-memory membership gate for tests: seated users are members, and seated managers also manage.
type Members struct {
	mu       sync.Mutex
	roles    map[[2]uuid.UUID]org.Role
	everyone bool
}

// NewMembers returns a gate with nobody seated.
func NewMembers() *Members { return &Members{roles: map[[2]uuid.UUID]org.Role{}} }

// PermissiveMembers returns a gate that treats every person as a manager, for tests that are not about the gate; a machine principal is still refused.
func PermissiveMembers() *Members {
	m := NewMembers()
	m.everyone = true
	return m
}

// SeatManager makes userID an admin of orgID.
func (m *Members) SeatManager(orgID, userID uuid.UUID) { m.seat(orgID, userID, org.RoleAdmin) }

// SeatMember makes userID a plain member of orgID.
func (m *Members) SeatMember(orgID, userID uuid.UUID) { m.seat(orgID, userID, org.RoleMember) }

// Remove takes userID out of orgID.
func (m *Members) Remove(orgID, userID uuid.UUID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.roles, [2]uuid.UUID{orgID, userID})
}

func (m *Members) seat(orgID, userID uuid.UUID, role org.Role) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.roles[[2]uuid.UUID{orgID, userID}] = role
}

func (m *Members) role(orgID, userID uuid.UUID) (org.Role, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if userID == uuid.Nil {
		return "", false
	}
	if m.everyone {
		return org.RoleAdmin, true
	}
	r, ok := m.roles[[2]uuid.UUID{orgID, userID}]
	return r, ok
}

// RequireManager refuses with *org.NotManagerError unless userID is a seated manager of orgID.
func (m *Members) RequireManager(_ context.Context, orgID, userID uuid.UUID) error {
	if r, ok := m.role(orgID, userID); !ok || !r.CanManage() {
		return &org.NotManagerError{OrgID: orgID.String(), UserID: userID.String()}
	}
	return nil
}

// RequireMember refuses with *org.MembershipMissingError unless userID is seated in orgID.
func (m *Members) RequireMember(_ context.Context, orgID, userID uuid.UUID) error {
	if _, ok := m.role(orgID, userID); !ok {
		return &org.MembershipMissingError{OrgID: orgID.String(), UserID: userID.String()}
	}
	return nil
}

// OrgProjects is an in-memory apikey.Projects for tests.
type OrgProjects struct {
	mu    sync.Mutex
	byOrg map[uuid.UUID][]uuid.UUID
}

// NewOrgProjects returns a catalog with no projects.
func NewOrgProjects() *OrgProjects { return &OrgProjects{byOrg: map[uuid.UUID][]uuid.UUID{}} }

// Add records projectID as a project of orgID.
func (p *OrgProjects) Add(orgID, projectID uuid.UUID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.byOrg[orgID] = append(p.byOrg[orgID], projectID)
}

// ProjectIDs returns the projects recorded for orgID.
func (p *OrgProjects) ProjectIDs(_ context.Context, orgID uuid.UUID) ([]uuid.UUID, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]uuid.UUID(nil), p.byOrg[orgID]...), nil
}
