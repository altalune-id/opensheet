package org_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/capabilities"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
)

type customFixture struct {
	svc   *org.Service
	store *fakes.Org
	roles *fakes.Roles
	orgID uuid.UUID
}

func newCustomFixture(t *testing.T) *customFixture {
	t.Helper()
	store := fakes.NewOrg()
	roles := fakes.NewRoles()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := org.NewService(store, capabilities.Capabilities{OrgCreation: true}, log, apperror.NewReporter(log, false).Unexpected,
		fakes.UnitOfWork, org.WithPermissions(authz.NewResolver(roles)))
	return &customFixture{svc: svc, store: store, roles: roles, orgID: uuid.New()}
}

func (f *customFixture) builtin(t *testing.T, role org.Role) uuid.UUID {
	t.Helper()
	id := uuid.New()
	m, err := org.NewMembership(f.orgID, id, role)
	require.NoError(t, err)
	require.NoError(t, f.store.SaveMembership(tenant.WithOrg(t.Context(), f.orgID), m))
	f.roles.Set(f.orgID, id, string(role))
	return id
}

func (f *customFixture) custom(t *testing.T, perms ...authz.Permission) (userID, roleID uuid.UUID) {
	t.Helper()
	userID, roleID = uuid.New(), f.role(perms...)
	require.NoError(t, f.store.SaveMembership(tenant.WithOrg(t.Context(), f.orgID),
		&org.Membership{OrgID: f.orgID, UserID: userID, Role: org.RoleCustom, RoleID: roleID}))
	f.roles.SetCustom(f.orgID, userID, roleID, perms...)
	return userID, roleID
}

func (f *customFixture) role(perms ...authz.Permission) uuid.UUID {
	id := uuid.New()
	f.roles.SetCustom(uuid.New(), uuid.New(), id, perms...)
	return id
}

func (f *customFixture) as(t *testing.T, userID uuid.UUID) context.Context {
	t.Helper()
	ctx, err := authz.NewResolver(f.roles).Bind(t.Context(), session.Principal{UserID: userID, Source: session.SourceOIDC},
		tenant.Context{OrgID: f.orgID, UserID: userID})
	require.NoError(t, err)
	return ctx
}

func (f *customFixture) membership(t *testing.T, userID uuid.UUID) *org.Membership {
	t.Helper()
	m, err := f.store.MembershipOf(tenant.WithOrg(t.Context(), f.orgID), f.orgID, userID)
	require.NoError(t, err)
	return m
}

func managerPerms() []authz.Permission {
	return append(authz.Builtin("member"), authz.MembersWrite)
}

func TestOrg_SetRole_CustomRoleBeyondCallerIsROL004(t *testing.T) {
	f := newCustomFixture(t)
	admin, member := f.builtin(t, org.RoleAdmin), f.builtin(t, org.RoleMember)
	rolesWriter := f.role(authz.SheetsRead, authz.RolesWrite)

	err := f.svc.SetRole(f.as(t, admin), f.orgID, member, org.RoleCustom, rolesWriter)
	require.True(t, authz.IsEscalationError(err), "got %T: %v", err, err)
	requireCode(t, err, apperror.CodeRoleEscalation)
	require.Equal(t, org.RoleMember, f.membership(t, member).Role)

	reader := f.role(authz.SheetsRead)
	require.NoError(t, f.svc.SetRole(f.as(t, admin), f.orgID, member, org.RoleCustom, reader))
	m := f.membership(t, member)
	require.Equal(t, org.RoleCustom, m.Role)
	require.Equal(t, reader, m.RoleID)

	require.NoError(t, f.svc.SetRole(f.as(t, admin), f.orgID, member, org.RoleMember, uuid.Nil))
	m = f.membership(t, member)
	require.Equal(t, org.RoleMember, m.Role)
	require.Equal(t, uuid.Nil, m.RoleID)
}

func TestOrg_SetRole_ChecksTheTargetsCurrentCustomRole(t *testing.T) {
	f := newCustomFixture(t)
	admin := f.builtin(t, org.RoleAdmin)
	target, roleID := f.custom(t, authz.SheetsRead, authz.RolesWrite)

	err := f.svc.SetRole(f.as(t, admin), f.orgID, target, org.RoleMember, uuid.Nil)
	require.True(t, authz.IsEscalationError(err), "an admin may not strip roles:write it does not hold, got %T: %v", err, err)
	require.Equal(t, roleID, f.membership(t, target).RoleID)
}

func TestOrg_SetRole_CustomActorWithMembersWrite(t *testing.T) {
	f := newCustomFixture(t)
	actor, _ := f.custom(t, managerPerms()...)
	member := f.builtin(t, org.RoleMember)

	reader := f.role(authz.SheetsRead)
	require.NoError(t, f.svc.SetRole(f.as(t, actor), f.orgID, member, org.RoleCustom, reader))
	require.Equal(t, reader, f.membership(t, member).RoleID)

	err := f.svc.SetRole(f.as(t, actor), f.orgID, member, org.RoleAdmin, uuid.Nil)
	requireCode(t, err, apperror.CodeRoleEscalation)
}

func TestOrg_SetRole_OnlyBuiltinOwnerGrantsOwner(t *testing.T) {
	f := newCustomFixture(t)
	f.builtin(t, org.RoleOwner)
	everything, _ := f.custom(t, authz.Builtin("owner")...)
	member := f.builtin(t, org.RoleMember)

	err := f.svc.SetRole(f.as(t, everything), f.orgID, member, org.RoleOwner, uuid.Nil)
	requireCode(t, err, apperror.CodeRoleEscalation)
	require.Equal(t, org.RoleMember, f.membership(t, member).Role)
}

func TestOrg_SetRole_RoleIDOnlyForCustom(t *testing.T) {
	f := newCustomFixture(t)
	owner, member := f.builtin(t, org.RoleOwner), f.builtin(t, org.RoleMember)

	require.True(t, org.IsInvalidRoleError(f.svc.SetRole(f.as(t, owner), f.orgID, member, org.RoleCustom, uuid.Nil)))
	require.True(t, org.IsInvalidRoleError(f.svc.SetRole(f.as(t, owner), f.orgID, member, org.RoleAdmin, uuid.New())))
	require.True(t, org.IsInvalidRoleError(f.svc.SetRole(f.as(t, owner), f.orgID, member, org.RoleCustom, uuid.New())),
		"an unknown custom role is refused, not granted")
}

func TestOrg_RemoveMember_CustomRoleBeyondCallerIsROL004(t *testing.T) {
	f := newCustomFixture(t)
	actor, _ := f.custom(t, managerPerms()...)
	admin := f.builtin(t, org.RoleAdmin)
	member := f.builtin(t, org.RoleMember)

	err := f.svc.RemoveMember(f.as(t, actor), f.orgID, admin)
	require.True(t, authz.IsEscalationError(err), "got %T: %v", err, err)
	requireCode(t, err, apperror.CodeRoleEscalation)
	f.membership(t, admin)

	require.NoError(t, f.svc.RemoveMember(f.as(t, actor), f.orgID, member))
}

func TestOrg_RemoveMember_AdminCannotRemoveRolesWriter(t *testing.T) {
	f := newCustomFixture(t)
	admin := f.builtin(t, org.RoleAdmin)
	target, _ := f.custom(t, authz.SheetsRead, authz.RolesWrite)

	err := f.svc.RemoveMember(f.as(t, admin), f.orgID, target)
	requireCode(t, err, apperror.CodeRoleEscalation)
}

func TestOrg_ListMembers_CustomRoleWithoutMembersReadIsRefused(t *testing.T) {
	f := newCustomFixture(t)
	reader, _ := f.custom(t, authz.SheetsRead)

	_, err := f.svc.ListMemberProfiles(f.as(t, reader), f.orgID)
	require.True(t, authz.IsDeniedError(err), "ListMemberProfiles: got %T: %v", err, err)
	_, err = f.svc.ListMembers(f.as(t, reader), f.orgID)
	require.True(t, authz.IsDeniedError(err), "ListMembers: got %T: %v", err, err)

	viewer, _ := f.custom(t, authz.SheetsRead, authz.MembersRead)
	ps, err := f.svc.ListMemberProfiles(f.as(t, viewer), f.orgID)
	require.NoError(t, err)
	require.Len(t, ps, 2)
	_, err = f.svc.ListMembers(f.as(t, viewer), f.orgID)
	require.NoError(t, err)
}

func TestOrg_ListMembers_BuiltinRolesHoldMembersRead(t *testing.T) {
	f := newCustomFixture(t)
	for _, r := range []org.Role{org.RoleOwner, org.RoleAdmin, org.RoleMember} {
		id := f.builtin(t, r)
		_, err := f.svc.ListMemberProfiles(f.as(t, id), f.orgID)
		require.NoError(t, err, string(r))
		_, err = f.svc.ListMembers(f.as(t, id), f.orgID)
		require.NoError(t, err, string(r))
	}
}

func TestOrg_ListMembers_ChecksTheNamedOrg(t *testing.T) {
	f := newCustomFixture(t)
	member := f.builtin(t, org.RoleMember)

	_, err := f.svc.ListMemberProfiles(f.as(t, member), uuid.New())
	require.True(t, authz.IsDeniedError(err), "a held set answers only for its own org, got %T: %v", err, err)
}
