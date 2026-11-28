package org_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
)

type removalFixture struct {
	svc      *org.Service
	store    *fakes.Org
	projects *fakes.OrgProjects
	orgID    uuid.UUID
}

func newRemovalFixture(t *testing.T, roles ...org.Role) (*removalFixture, []uuid.UUID) {
	t.Helper()
	store, projects := fakes.NewOrg(), fakes.NewOrgProjects()
	f := &removalFixture{svc: newServiceWithStore(t, store, org.WithProjects(projects)), store: store, projects: projects, orgID: uuid.New()}
	ids := make([]uuid.UUID, 0, len(roles))
	scoped := tenant.WithOrg(t.Context(), f.orgID)
	for _, role := range roles {
		id := uuid.New()
		m, err := org.NewMembership(f.orgID, id, role)
		require.NoError(t, err)
		require.NoError(t, store.SaveMembership(scoped, m))
		ids = append(ids, id)
	}
	return f, ids
}

func (f *removalFixture) as(t *testing.T, actor uuid.UUID) context.Context {
	t.Helper()
	return boundAs(t, f.orgID, actor, f.roleOf(t, actor))
}

func (f *removalFixture) stillAMember(t *testing.T, _, target uuid.UUID) bool {
	t.Helper()
	_, err := f.store.MembershipOf(tenant.WithOrg(t.Context(), f.orgID), f.orgID, target)
	return err == nil
}

func TestRemoveMember_RefusesRemovingYourself(t *testing.T) {
	f, ids := newRemovalFixture(t, org.RoleOwner)
	self := ids[0]

	err := f.svc.RemoveMember(f.as(t, self), f.orgID, self)
	require.Error(t, err)
	require.True(t, org.IsSelfRemovalError(err), "want *SelfRemovalError, got %T: %v", err, err)
	require.True(t, f.stillAMember(t, self, self), "a refused removal must not delete the row")
}

func TestRemoveMember_PlainMemberIsRefusedBeforeTheSelfCheck(t *testing.T) {
	f, ids := newRemovalFixture(t, org.RoleMember)
	self := ids[0]

	err := f.svc.RemoveMember(f.as(t, self), f.orgID, self)
	require.True(t, authz.IsDeniedError(err), "want *DeniedError, got %T: %v", err, err)
	require.True(t, f.stillAMember(t, self, self))
}

func TestRemoveMember_NonOwnerCannotRemoveAnOwner(t *testing.T) {
	f, ids := newRemovalFixture(t, org.RoleAdmin, org.RoleOwner)
	admin, owner := ids[0], ids[1]

	err := f.svc.RemoveMember(f.as(t, admin), f.orgID, owner)
	require.Error(t, err)
	require.True(t, org.IsOwnerRemovalError(err), "want *OwnerRemovalError, got %T: %v", err, err)
	require.True(t, f.stillAMember(t, admin, owner), "the owner membership must survive")
}

func TestRemoveMember_OwnerCanRemoveAnotherOwner(t *testing.T) {
	f, ids := newRemovalFixture(t, org.RoleOwner, org.RoleOwner)
	first, second := ids[0], ids[1]

	require.NoError(t, f.svc.RemoveMember(f.as(t, first), f.orgID, second),
		"an owner must be able to remove a co-owner")
	require.False(t, f.stillAMember(t, first, second))
	require.True(t, f.stillAMember(t, first, first), "the acting owner stays")
}

func TestRemoveMember_OwnerCanRemoveAPlainMember(t *testing.T) {
	f, ids := newRemovalFixture(t, org.RoleOwner, org.RoleMember)
	owner, member := ids[0], ids[1]

	require.NoError(t, f.svc.RemoveMember(f.as(t, owner), f.orgID, member))
	require.False(t, f.stillAMember(t, owner, member))
}

// TestRemoveMember_LastOwnerCannotBeRemoved is the invariant the two rules produce together: only an owner removes an owner, and nobody removes themselves — so an org always keeps one owner.
func TestRemoveMember_LastOwnerCannotBeRemoved(t *testing.T) {
	f, ids := newRemovalFixture(t, org.RoleOwner, org.RoleAdmin, org.RoleMember)
	owner, admin, member := ids[0], ids[1], ids[2]

	for _, actor := range []uuid.UUID{owner, admin, member} {
		err := f.svc.RemoveMember(f.as(t, actor), f.orgID, owner)
		require.Error(t, err, "actor %s must not be able to remove the last owner", actor)
		require.True(t, f.stillAMember(t, owner, owner), "the last owner must survive every attempt")
	}
}

func TestRemoveMember_RefusedUnderTheLockRunsNoHook(t *testing.T) {
	store := fakes.NewOrg()
	f := &removalFixture{svc: newServiceWithStore(t, store), store: store, orgID: uuid.New()}
	scoped := tenant.WithOrg(t.Context(), f.orgID)
	actor, target := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{actor, target} {
		m, err := org.NewMembership(f.orgID, id, org.RoleOwner)
		require.NoError(t, err)
		require.NoError(t, store.SaveMembership(scoped, m))
	}
	ctx := f.as(t, actor)
	store.OnLock = func() {
		m, err := org.NewMembership(f.orgID, actor, org.RoleAdmin)
		require.NoError(t, err)
		require.NoError(t, store.SaveMembership(scoped, m))
	}
	ran := false
	f.svc.OnMemberRemoved(func(context.Context, uuid.UUID, uuid.UUID) error {
		ran = true
		return nil
	})

	err := f.svc.RemoveMember(ctx, f.orgID, target)
	require.True(t, org.IsOwnerRemovalError(err), "want *OwnerRemovalError, got %T: %v", err, err)
	require.False(t, ran, "SECURITY: a removal refused under the lock must not revoke the member's tokens")
	require.True(t, f.stillAMember(t, actor, target))
}

func TestRemoveMember_RemovedActorIsRefused(t *testing.T) {
	f, ids := newRemovalFixture(t, org.RoleOwner, org.RoleOwner, org.RoleMember)
	actor, other, member := ids[0], ids[1], ids[2]
	stale := f.as(t, actor)
	require.NoError(t, f.svc.RemoveMember(f.as(t, other), f.orgID, actor))

	err := f.svc.RemoveMember(stale, f.orgID, member)
	require.True(t, authz.IsDeniedError(err), "want *DeniedError, got %T: %v", err, err)
	require.True(t, f.stillAMember(t, actor, member))
}
