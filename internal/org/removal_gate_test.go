package org_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/org"
	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/internal/testutil/fakes"
)

type removalFixture struct {
	svc   *org.Service
	store *fakes.Org
	orgID uuid.UUID
}

// newRemovalFixture seeds one membership per role given and returns the ids in the same order.
func newRemovalFixture(t *testing.T, roles ...org.Role) (*removalFixture, []uuid.UUID) {
	t.Helper()
	store := fakes.NewOrg()
	f := &removalFixture{svc: newServiceWithStore(t, store), store: store, orgID: uuid.New()}
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

// as returns a context carrying actor as the caller, the way every transport supplies it.
func (f *removalFixture) as(t *testing.T, actor uuid.UUID) context.Context {
	t.Helper()
	return tenant.Into(t.Context(), tenant.Context{OrgID: f.orgID, UserID: actor})
}

func (f *removalFixture) stillAMember(t *testing.T, actor, target uuid.UUID) bool {
	t.Helper()
	_, err := f.store.MembershipOf(f.as(t, actor), f.orgID, target)
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

func TestRemoveMember_RefusesRemovingYourselfAsThePlainMember(t *testing.T) {
	f, ids := newRemovalFixture(t, org.RoleMember)
	self := ids[0]

	err := f.svc.RemoveMember(f.as(t, self), f.orgID, self)
	require.True(t, org.IsSelfRemovalError(err), "want *SelfRemovalError, got %T: %v", err, err)
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

// TestRemoveMember_LastOwnerCannotBeRemoved is the invariant the two rules produce together:
// only an owner removes an owner, and nobody removes themselves — so an org always keeps one owner.
func TestRemoveMember_LastOwnerCannotBeRemoved(t *testing.T) {
	f, ids := newRemovalFixture(t, org.RoleOwner, org.RoleAdmin, org.RoleMember)
	owner, admin, member := ids[0], ids[1], ids[2]

	for _, actor := range []uuid.UUID{owner, admin, member} {
		err := f.svc.RemoveMember(f.as(t, actor), f.orgID, owner)
		require.Error(t, err, "actor %s must not be able to remove the last owner", actor)
		require.True(t, f.stillAMember(t, owner, owner), "the last owner must survive every attempt")
	}
}
