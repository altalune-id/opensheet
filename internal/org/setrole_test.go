package org_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
)

func boundAs(t *testing.T, orgID, userID uuid.UUID, role org.Role) context.Context {
	t.Helper()
	roles := fakes.NewRoles().Set(orgID, userID, string(role))
	ctx, err := authz.NewResolver(roles).Bind(t.Context(),
		session.Principal{UserID: userID, Source: session.SourceOIDC},
		tenant.Context{OrgID: orgID, UserID: userID})
	require.NoError(t, err)
	return ctx
}

func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	ae, ok := apperror.AsAppError(err)
	require.True(t, ok, "want an AppError, got %T: %v", err, err)
	require.Equal(t, code, ae.Code())
}

func (f *removalFixture) roleOf(t *testing.T, userID uuid.UUID) org.Role {
	t.Helper()
	m, err := f.store.MembershipOf(tenant.WithOrg(t.Context(), f.orgID), f.orgID, userID)
	require.NoError(t, err)
	return m.Role
}

func TestOrg_SetRole_OwnerPromotesMemberToAdmin(t *testing.T) {
	f, ids := newRemovalFixture(t, org.RoleOwner, org.RoleMember)
	owner, member := ids[0], ids[1]

	require.NoError(t, f.svc.SetRole(f.as(t, owner), f.orgID, member, org.RoleAdmin, uuid.Nil))
	require.Equal(t, org.RoleAdmin, f.roleOf(t, member))
}

func TestOrg_SetRole_NoEscalation(t *testing.T) {
	f, ids := newRemovalFixture(t, org.RoleOwner, org.RoleAdmin, org.RoleMember)
	owner, admin, member := ids[0], ids[1], ids[2]

	err := f.svc.SetRole(f.as(t, admin), f.orgID, owner, org.RoleMember, uuid.Nil)
	require.True(t, authz.IsEscalationError(err), "want *EscalationError, got %T: %v", err, err)
	requireCode(t, err, apperror.CodeRoleEscalation)
	require.Equal(t, org.RoleOwner, f.roleOf(t, owner))

	err = f.svc.SetRole(f.as(t, member), f.orgID, admin, org.RoleMember, uuid.Nil)
	require.True(t, authz.IsDeniedError(err), "a member holds no members:write, got %T: %v", err, err)
	require.Equal(t, org.RoleAdmin, f.roleOf(t, admin))
}

func TestOrg_SetRole_AdminCannotGrantOwner(t *testing.T) {
	f, ids := newRemovalFixture(t, org.RoleOwner, org.RoleAdmin, org.RoleMember)
	admin, member := ids[1], ids[2]

	err := f.svc.SetRole(f.as(t, admin), f.orgID, member, org.RoleOwner, uuid.Nil)
	require.True(t, authz.IsEscalationError(err), "want *EscalationError, got %T: %v", err, err)
	requireCode(t, err, apperror.CodeRoleEscalation)
	require.Equal(t, org.RoleMember, f.roleOf(t, member))
}

func TestOrg_SetRole_LastOwnerKept(t *testing.T) {
	f, ids := newRemovalFixture(t, org.RoleOwner, org.RoleMember)
	owner := ids[0]

	err := f.svc.SetRole(f.as(t, owner), f.orgID, owner, org.RoleAdmin, uuid.Nil)
	require.True(t, authz.IsLastOwnerError(err), "want *LastOwnerError, got %T: %v", err, err)
	requireCode(t, err, apperror.CodeRoleLastOwner)
	require.Equal(t, org.RoleOwner, f.roleOf(t, owner))
}

func TestOrg_SetRole_RefusesARoleID(t *testing.T) {
	f, ids := newRemovalFixture(t, org.RoleOwner, org.RoleMember)
	owner, member := ids[0], ids[1]

	err := f.svc.SetRole(f.as(t, owner), f.orgID, member, org.RoleAdmin, uuid.New())
	require.True(t, org.IsInvalidRoleError(err), "want *InvalidRoleError, got %T: %v", err, err)
	require.Equal(t, org.RoleMember, f.roleOf(t, member))
}

func TestOrg_RenameRemoveNeedMembersWrite(t *testing.T) {
	f, ids := newRemovalFixture(t, org.RoleOwner, org.RoleMember, org.RoleMember)
	owner, member, other := ids[0], ids[1], ids[2]
	require.NoError(t, f.store.Save(tenant.WithOrg(t.Context(), f.orgID), &org.Org{ID: f.orgID, Slug: "acme", Name: "Acme", OwnerID: owner}))

	_, err := f.svc.Rename(f.as(t, member), f.orgID, "Renamed")
	require.True(t, authz.IsDeniedError(err), "want *DeniedError, got %T: %v", err, err)
	o, err := f.store.ByID(tenant.WithOrg(t.Context(), f.orgID), f.orgID)
	require.NoError(t, err)
	require.Equal(t, "Acme", o.Name)

	err = f.svc.RemoveMember(f.as(t, member), f.orgID, other)
	require.True(t, authz.IsDeniedError(err), "want *DeniedError, got %T: %v", err, err)
	require.True(t, f.stillAMember(t, owner, other))
}

func TestOrg_ManagerRefusalKeepsORG011(t *testing.T) {
	f, ids := newRemovalFixture(t, org.RoleOwner, org.RoleMember, org.RoleMember)
	owner, member, other := ids[0], ids[1], ids[2]
	require.NoError(t, f.store.Save(tenant.WithOrg(t.Context(), f.orgID), &org.Org{ID: f.orgID, Slug: "acme", Name: "Acme", OwnerID: owner}))

	_, err := f.svc.Rename(f.as(t, member), f.orgID, "Renamed")
	requireCode(t, err, apperror.CodeOrgManagerRequired)
	requireCode(t, f.svc.RemoveMember(f.as(t, member), f.orgID, other), apperror.CodeOrgManagerRequired)
	requireCode(t, f.svc.SetRole(f.as(t, member), f.orgID, other, org.RoleAdmin, uuid.Nil), apperror.CodeOrgManagerRequired)
}

func TestRoleChangeRefusal(t *testing.T) {
	owner, admin := authz.Builtin("owner"), authz.Builtin("admin")
	cases := []struct {
		name          string
		held          []authz.Permission
		actor, cur, n org.Role
		refused       bool
	}{
		{"owner promotes member to admin", owner, org.RoleOwner, org.RoleMember, org.RoleAdmin, false},
		{"owner makes an owner", owner, org.RoleOwner, org.RoleMember, org.RoleOwner, false},
		{"admin promotes member to admin", admin, org.RoleAdmin, org.RoleMember, org.RoleAdmin, false},
		{"admin makes an owner", admin, org.RoleAdmin, org.RoleMember, org.RoleOwner, true},
		{"admin demotes an owner", admin, org.RoleAdmin, org.RoleOwner, org.RoleMember, true},
		{"owner role without the owner's permissions", admin, org.RoleOwner, org.RoleMember, org.RoleOwner, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := org.RoleChangeRefusal(c.held, c.actor, c.cur, c.n)
			require.Equal(t, c.refused, authz.IsEscalationError(err), "got %v", err)
		})
	}
}

func TestOrg_SetRole_StaleOwnerCannotGrantOwner(t *testing.T) {
	f, ids := newRemovalFixture(t, org.RoleOwner, org.RoleOwner, org.RoleMember)
	actor, member := ids[0], ids[2]
	stale := f.as(t, actor)
	m, err := org.NewMembership(f.orgID, actor, org.RoleAdmin)
	require.NoError(t, err)
	require.NoError(t, f.store.SaveMembership(tenant.WithOrg(t.Context(), f.orgID), m))

	err = f.svc.SetRole(stale, f.orgID, member, org.RoleOwner, uuid.Nil)
	require.True(t, authz.IsEscalationError(err), "want *EscalationError, got %T: %v", err, err)
	requireCode(t, err, apperror.CodeRoleEscalation)
	require.Equal(t, org.RoleMember, f.roleOf(t, member))
}
