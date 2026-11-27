package boot

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/capabilities"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/role"
)

func (f *joinFixture) roleService() (*role.Service, role.Store) {
	store := role.NewStore(f.cfg.DB, db.Pool{W: f.sqlDB, R: f.sqlDB}, nil)
	return newRoleService(f.cfg, store, f.orgs, f.log, f.unex, f.uow), store
}

func (f *joinFixture) asOwner(t *testing.T, store role.Store) context.Context {
	t.Helper()
	ctx, err := newAuthzResolver(f.orgs, store).Bind(t.Context(),
		session.Principal{UserID: f.ownerID, Source: session.SourceOIDC},
		tenant.Context{OrgID: f.orgID, UserID: f.ownerID})
	require.NoError(t, err)
	return ctx
}

func TestRoles_SelfhostedSystemOrgCreatesAndAssignsCustomRole(t *testing.T) {
	f := newJoinFixture(t, config.ModeSelfhosted)
	o, err := f.orgs.ByID(t.Context(), f.orgID)
	require.NoError(t, err)
	require.True(t, o.System, "self-hosted runs in the system org")
	memberID := uuid.New()
	f.exec(t, "INSERT INTO "+f.prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES (?, 'mia@example.com', 'Mia', '', 0, ?, ?)", memberID.String(), f.now, f.now)
	f.exec(t, "INSERT INTO "+f.prefix+"memberships (id, org_id, user_id, role, role_id, system, created_at) VALUES (?, ?, ?, 'member', NULL, 0, ?)",
		uuid.NewString(), f.orgID.String(), memberID.String(), f.now)
	svc, store := f.roleService()
	ctx := f.asOwner(t, store)

	allowed, err := svc.Allowed(ctx)
	require.NoError(t, err)
	require.True(t, allowed)
	r, err := svc.Create(ctx, "Clerk", "", []authz.Permission{authz.SheetsRead})
	require.NoError(t, err)
	orgs := org.NewService(f.orgs, capabilities.Capabilities{}, f.log, f.unex, f.uow, org.WithPermissions(newAuthzResolver(f.orgs, store)))
	require.NoError(t, orgs.SetRole(ctx, f.orgID, memberID, org.RoleCustom, r.ID))
	m, ok := f.member(t, "mia@example.com")
	require.True(t, ok)
	require.Equal(t, "custom", m.role)
	require.Equal(t, r.ID.String(), m.roleID.String)
}

func TestRoles_CloudSystemOrgRefusesCustomRoles(t *testing.T) {
	f := newJoinFixture(t, config.ModeCloud)
	f.exec(t, "UPDATE "+f.prefix+"orgs SET system = 1 WHERE id = ?", f.orgID.String())
	f.role(t)
	svc, store := f.roleService()
	ctx := f.asOwner(t, store)

	allowed, err := svc.Allowed(ctx)
	require.NoError(t, err)
	require.False(t, allowed)
	_, err = svc.Create(ctx, "Auditor", "", []authz.Permission{authz.SheetsRead})
	ae, ok := apperror.AsAppError(err)
	require.True(t, ok, "got %T: %v", err, err)
	require.Equal(t, apperror.CodeRoleSystemOrg, ae.Code())
	items, err := svc.List(ctx)
	require.NoError(t, err)
	require.Empty(t, items, "the staff org's pickers offer no custom role")
}

func TestRoles_CloudCustomerOrgAllowsCustomRoles(t *testing.T) {
	f := newJoinFixture(t, config.ModeCloud)
	roleID := f.role(t)
	svc, store := f.roleService()
	ctx := f.asOwner(t, store)

	allowed, err := svc.Allowed(ctx)
	require.NoError(t, err)
	require.True(t, allowed)
	_, err = svc.Create(ctx, "Auditor", "", []authz.Permission{authz.SheetsRead})
	require.NoError(t, err)
	items, err := svc.List(ctx)
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Contains(t, []uuid.UUID{items[0].ID, items[1].ID}, roleID)
}
