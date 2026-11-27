package fakes

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
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

// BindRole returns ctx scoped to tc, carrying tc's person bound in tc's org with role's built-in permissions; an empty role holds nothing.
func BindRole(ctx context.Context, tc tenant.Context, role string) context.Context {
	perms := authz.Builtin(role)
	held := make([]string, 0, len(perms))
	for _, p := range perms {
		held = append(held, string(p))
	}
	return session.PrincipalInto(tenant.Into(ctx, tc), session.Principal{
		UserID:          tc.UserID,
		Source:          session.SourceOIDC,
		ActiveOrgID:     tc.OrgID,
		ActiveProjectID: tc.ProjectID,
		Held:            held,
		HeldOrg:         "Acme",
		HeldOrgID:       tc.OrgID,
		HeldRole:        role,
	})
}
