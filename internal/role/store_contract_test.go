package role_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/role"
)

type storeFixture struct {
	store      role.Store
	org, other tenant.Context
	hold       func(t *testing.T, orgID, roleID uuid.UUID)
	release    func(t *testing.T, orgID, roleID uuid.UUID)
	invite     func(t *testing.T, orgID, roleID uuid.UUID, accepted bool)
}

func roundTrip(t *testing.T, f storeFixture) {
	ctx := tenant.Into(t.Context(), f.org)
	b := mustRole(t, f.org.OrgID, "beta", authz.SheetsWrite, authz.SheetsRead)
	a := mustRole(t, f.org.OrgID, "Alpha")
	require.NoError(t, f.store.Save(ctx, b, 0))
	require.NoError(t, f.store.Save(ctx, a, 0))

	got, err := f.store.ByID(ctx, b.ID)
	require.NoError(t, err)
	assert.Equal(t, b.Name, got.Name)
	assert.Equal(t, b.Permissions, got.Permissions)
	assert.Equal(t, int64(1), got.Version)
	assert.Equal(t, f.org.OrgID, got.OrgID)

	perms, err := f.store.Permissions(ctx, b.ID)
	require.NoError(t, err)
	assert.Equal(t, []authz.Permission{authz.SheetsRead, authz.SheetsWrite}, perms)
	perms, err = f.store.Permissions(ctx, a.ID)
	require.NoError(t, err)
	assert.Empty(t, perms)

	list, err := f.store.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, []string{"Alpha", "beta"}, []string{list[0].Name, list[1].Name})
}

func nameUniqueIgnoringCase(t *testing.T, f storeFixture) {
	ctx := tenant.Into(t.Context(), f.org)
	require.NoError(t, f.store.Save(ctx, mustRole(t, f.org.OrgID, "Viewer"), 0))
	err := f.store.Save(ctx, mustRole(t, f.org.OrgID, "VIEWER"), 0)
	assert.True(t, role.IsAlreadyExistsError(err), "got %T: %v", err, err)
	require.NoError(t, f.store.Save(tenant.Into(t.Context(), f.other), mustRole(t, f.other.OrgID, "viewer"), 0), "another org may reuse the name")
}

func saveIfVersion(t *testing.T, f storeFixture) {
	ctx := tenant.Into(t.Context(), f.org)
	r := mustRole(t, f.org.OrgID, "Clerk")
	require.NoError(t, f.store.Save(ctx, r, 0))
	r.Name = "Clerk 2"
	require.NoError(t, f.store.Save(ctx, r, 1))
	err := f.store.Save(ctx, r, 1)
	stale, ok := errors.AsType[*role.StaleVersionError](err)
	require.True(t, ok, "got %T: %v", err, err)
	assert.Equal(t, int64(2), stale.Got)
	assert.Equal(t, "ROL009", stale.ToAppError().Code())
	require.NoError(t, f.store.Save(ctx, r, 0), "ifVersion 0 writes unconditionally")
	got, err := f.store.ByID(ctx, r.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(3), got.Version)
	assert.Equal(t, "Clerk 2", got.Name)
}

// SECURITY: every verb filters by the org on ctx, so another org's role id is absent.
func otherOrgInvisible(t *testing.T, f storeFixture) {
	r := mustRole(t, f.org.OrgID, "Mine", authz.SheetsRead)
	require.NoError(t, f.store.Save(tenant.Into(t.Context(), f.org), r, 0))
	theirs := tenant.Into(t.Context(), f.other)

	_, err := f.store.ByID(theirs, r.ID)
	assert.True(t, role.IsNotFoundError(err), "ByID: %T: %v", err, err)
	_, err = f.store.Permissions(theirs, r.ID)
	assert.True(t, role.IsNotFoundError(err), "Permissions: %T: %v", err, err)
	list, err := f.store.List(theirs)
	require.NoError(t, err)
	assert.Empty(t, list, "List")
	hijack := *r
	hijack.Name = "Hijacked"
	hijack.Permissions = []authz.Permission{authz.MembersWrite}
	err = f.store.Save(theirs, &hijack, 0)
	assert.True(t, role.IsNotFoundError(err), "Save: %T: %v", err, err)
	err = f.store.Delete(theirs, r.ID)
	assert.True(t, role.IsNotFoundError(err), "Delete: %T: %v", err, err)

	got, err := f.store.ByID(tenant.Into(t.Context(), f.org), r.ID)
	require.NoError(t, err)
	assert.Equal(t, "Mine", got.Name)
	assert.Equal(t, []authz.Permission{authz.SheetsRead}, got.Permissions)
}

func deleteHeldIsInUse(t *testing.T, f storeFixture) {
	ctx := tenant.Into(t.Context(), f.org)
	held := mustRole(t, f.org.OrgID, "Held")
	free := mustRole(t, f.org.OrgID, "Free")
	require.NoError(t, f.store.Save(ctx, held, 0))
	require.NoError(t, f.store.Save(ctx, free, 0))
	f.hold(t, f.org.OrgID, held.ID)

	counts, err := f.store.HeldBy(ctx)
	require.NoError(t, err)
	assert.Equal(t, map[uuid.UUID]int{held.ID: 1}, counts)

	err = f.store.Delete(ctx, held.ID)
	inUse, ok := errors.AsType[*role.InUseError](err)
	require.True(t, ok, "got %T: %v", err, err)
	assert.Equal(t, "ROL003", inUse.ToAppError().Code())
	require.NoError(t, f.store.Delete(ctx, free.ID))
	_, err = f.store.ByID(ctx, free.ID)
	assert.True(t, role.IsNotFoundError(err))
	assert.True(t, role.IsNotFoundError(f.store.Delete(ctx, free.ID)))
}

func deleteClearsAcceptedInvites(t *testing.T, f storeFixture) {
	ctx := tenant.Into(t.Context(), f.org)
	joined := mustRole(t, f.org.OrgID, "Joined")
	require.NoError(t, f.store.Save(ctx, joined, 0))
	f.hold(t, f.org.OrgID, joined.ID)
	f.invite(t, f.org.OrgID, joined.ID, true)
	f.release(t, f.org.OrgID, joined.ID)

	require.NoError(t, f.store.Delete(ctx, joined.ID), "an accepted invite never blocks a delete")
	_, err := f.store.ByID(ctx, joined.ID)
	assert.True(t, role.IsNotFoundError(err))
}

func deletePendingInviteIsInUse(t *testing.T, f storeFixture) {
	ctx := tenant.Into(t.Context(), f.org)
	pending := mustRole(t, f.org.OrgID, "Pending")
	require.NoError(t, f.store.Save(ctx, pending, 0))
	f.invite(t, f.org.OrgID, pending.ID, false)
	f.invite(t, f.org.OrgID, pending.ID, true)

	err := f.store.Delete(ctx, pending.ID)
	inUse, ok := errors.AsType[*role.InUseError](err)
	require.True(t, ok, "got %T: %v", err, err)
	assert.Equal(t, "ROL003", inUse.ToAppError().Code())
	_, err = f.store.ByID(ctx, pending.ID)
	require.NoError(t, err, "a refused delete keeps the role")
}

func heldByIsPerOrg(t *testing.T, f storeFixture) {
	ctx := tenant.Into(t.Context(), f.org)
	held := mustRole(t, f.org.OrgID, "Held")
	require.NoError(t, f.store.Save(ctx, held, 0))
	f.hold(t, f.org.OrgID, held.ID)
	counts, err := f.store.HeldBy(tenant.Into(t.Context(), f.other))
	require.NoError(t, err)
	assert.Empty(t, counts, "another org sees no holders")
}

func mustRole(t *testing.T, orgID uuid.UUID, name string, perms ...authz.Permission) *role.Role {
	t.Helper()
	r, err := role.New(orgID, name, "", perms, time.Now())
	require.NoError(t, err)
	return r
}
