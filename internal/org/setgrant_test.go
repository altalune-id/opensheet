package org_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/tenant"
)

func (f *removalFixture) grantOf(t *testing.T, userID uuid.UUID) org.ProjectGrant {
	t.Helper()
	m, err := f.store.MembershipOf(tenant.WithOrg(t.Context(), f.orgID), f.orgID, userID)
	require.NoError(t, err)
	return org.ProjectGrant{All: m.AllProjects, ProjectIDs: m.ProjectIDs}
}

func (f *removalFixture) project(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.New()
	f.projects.Add(f.orgID, id)
	return id
}

func TestOrg_SetProjectGrant_StoresTheGrant(t *testing.T) {
	f, ids := newRemovalFixture(t, org.RoleOwner, org.RoleMember)
	owner, member := ids[0], ids[1]
	p1 := f.project(t)

	require.NoError(t, f.svc.SetProjectGrant(f.as(t, owner), f.orgID, member, org.ProjectGrant{ProjectIDs: []uuid.UUID{p1}}))
	require.Equal(t, org.ProjectGrant{ProjectIDs: []uuid.UUID{p1}}, f.grantOf(t, member))
	require.NoError(t, f.svc.SetProjectGrant(f.as(t, owner), f.orgID, member, org.ProjectGrant{All: true}))
	require.Equal(t, org.ProjectGrant{All: true}, f.grantOf(t, member))
}

func TestOrg_SetProjectGrant_InvalidGrant(t *testing.T) {
	f, ids := newRemovalFixture(t, org.RoleOwner, org.RoleMember)
	owner, member := ids[0], ids[1]

	err := f.svc.SetProjectGrant(f.as(t, owner), f.orgID, member, org.ProjectGrant{})
	require.True(t, org.IsProjectGrantEmptyError(err), "%T %v", err, err)
	requireCode(t, err, apperror.CodeProjectGrantEmpty)
	err = f.svc.SetProjectGrant(f.as(t, owner), f.orgID, member, org.ProjectGrant{All: true, ProjectIDs: []uuid.UUID{uuid.New()}})
	require.True(t, org.IsProjectGrantConflictError(err), "%T %v", err, err)
	requireCode(t, err, apperror.CodeProjectGrantConflict)
	require.Equal(t, org.ProjectGrant{All: true}, f.grantOf(t, member))
}

func TestOrg_SetProjectGrant_OwnerIsConflict(t *testing.T) {
	f, ids := newRemovalFixture(t, org.RoleOwner, org.RoleOwner)
	owner, other := ids[0], ids[1]

	err := f.svc.SetProjectGrant(f.as(t, owner), f.orgID, other, org.ProjectGrant{ProjectIDs: []uuid.UUID{uuid.New()}})
	require.True(t, org.IsProjectGrantConflictError(err), "an owner always reaches every project: %T %v", err, err)
	requireCode(t, err, apperror.CodeProjectGrantConflict)
	require.Equal(t, org.ProjectGrant{All: true}, f.grantOf(t, other))
}

func TestOrg_SetProjectGrant_EscalationIsROL012(t *testing.T) {
	f, ids := newRemovalFixture(t, org.RoleOwner, org.RoleAdmin, org.RoleMember)
	admin, member := ids[1], ids[2]
	p1, p2 := f.project(t), f.project(t)
	ctx := f.as(t, admin)
	require.NoError(t, f.store.SetProjectGrant(tenant.WithOrg(t.Context(), f.orgID), f.orgID, admin, org.ProjectGrant{ProjectIDs: []uuid.UUID{p1}}),
		"the admin was bound with every project, then lost all but p1")

	err := f.svc.SetProjectGrant(ctx, f.orgID, member, org.ProjectGrant{ProjectIDs: []uuid.UUID{p1, p2}})
	require.True(t, org.IsProjectGrantEscalationError(err), "%T %v", err, err)
	requireCode(t, err, apperror.CodeProjectGrantEscalation)
	err = f.svc.SetProjectGrant(ctx, f.orgID, member, org.ProjectGrant{All: true})
	require.True(t, org.IsProjectGrantEscalationError(err), "a restricted actor cannot give every project: %T %v", err, err)
	require.Equal(t, org.ProjectGrant{All: true}, f.grantOf(t, member), "a refused grant changes nothing")

	require.NoError(t, f.svc.SetProjectGrant(ctx, f.orgID, member, org.ProjectGrant{ProjectIDs: []uuid.UUID{p1}}))
	require.Equal(t, org.ProjectGrant{ProjectIDs: []uuid.UUID{p1}}, f.grantOf(t, member))
}

func TestOrg_SetProjectGrant_Gates(t *testing.T) {
	f, ids := newRemovalFixture(t, org.RoleOwner, org.RoleAdmin, org.RoleMember)
	owner, admin, member := ids[0], ids[1], ids[2]
	g := org.ProjectGrant{ProjectIDs: []uuid.UUID{f.project(t)}}

	err := f.svc.SetProjectGrant(f.as(t, member), f.orgID, admin, g)
	require.True(t, authz.IsDeniedError(err), "a member holds no members:write: %T %v", err, err)

	err = f.svc.SetProjectGrant(f.as(t, admin), f.orgID, uuid.New(), g)
	require.True(t, org.IsMembershipMissingError(err), "%T %v", err, err)

	m, err := f.store.MembershipOf(tenant.WithOrg(t.Context(), f.orgID), f.orgID, admin)
	require.NoError(t, err)
	m.System = true
	require.NoError(t, f.store.SaveMembership(tenant.WithOrg(t.Context(), f.orgID), m))
	err = f.svc.SetProjectGrant(f.as(t, owner), f.orgID, admin, g)
	require.True(t, org.IsSystemProtectedError(err), "%T %v", err, err)
}

func TestOrg_SetProjectGrant_ForeignProjectIsROL012(t *testing.T) {
	f, ids := newRemovalFixture(t, org.RoleOwner, org.RoleMember)
	owner, member := ids[0], ids[1]
	own, foreign := f.project(t), uuid.New()
	f.projects.Add(uuid.New(), foreign)

	for name, id := range map[string]uuid.UUID{"another org's project": foreign, "a project that does not exist": uuid.New()} {
		err := f.svc.SetProjectGrant(f.as(t, owner), f.orgID, member, org.ProjectGrant{ProjectIDs: []uuid.UUID{own, id}})
		require.True(t, org.IsProjectGrantEscalationError(err), "%s, from an all-projects owner: %T %v", name, err, err)
		requireCode(t, err, apperror.CodeProjectGrantEscalation)
	}
	require.Equal(t, org.ProjectGrant{All: true}, f.grantOf(t, member))
}

func TestOrg_SetRole_PromoteRestrictedAdminToOwnerClearsGrant(t *testing.T) {
	f, ids := newRemovalFixture(t, org.RoleOwner, org.RoleAdmin)
	owner, admin := ids[0], ids[1]
	p1 := f.project(t)
	require.NoError(t, f.svc.SetProjectGrant(f.as(t, owner), f.orgID, admin, org.ProjectGrant{ProjectIDs: []uuid.UUID{p1}}))

	require.NoError(t, f.svc.SetRole(f.as(t, owner), f.orgID, admin, org.RoleOwner, uuid.Nil))
	require.Equal(t, org.RoleOwner, f.roleOf(t, admin))
	require.Equal(t, org.ProjectGrant{All: true}, f.grantOf(t, admin), "an owner always reaches every project")
}
