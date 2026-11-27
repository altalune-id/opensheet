package invite_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/invite"
	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
	"altalune.id/opensheet/internal/platform/tenant"
)

func TestInvite_NewNeedsRoleIDExactlyForCustom(t *testing.T) {
	roleID := uuid.New()
	for _, c := range []struct {
		role   invite.Role
		roleID uuid.UUID
		ok     bool
	}{
		{invite.RoleCustom, roleID, true},
		{invite.RoleCustom, uuid.Nil, false},
		{invite.RoleMember, roleID, false},
		{invite.RoleMember, uuid.Nil, true},
		{"viewer", uuid.Nil, false},
	} {
		_, err := invite.New(invite.NewParams{OrgID: uuid.New(), Email: "a@x.co", Role: c.role, RoleID: c.roleID, TTL: time.Hour, Token: "t"})
		if c.ok {
			assert.NoError(t, err, "%s %s", c.role, c.roleID)
			continue
		}
		assert.True(t, invite.IsInvalidRoleError(err), "%s %s: %T %v", c.role, c.roleID, err, err)
	}
}

func TestSQLiteStore_RoleIDRoundTrip(t *testing.T) {
	store, sqlDB, tc := newSQLiteStoreForTest(t)
	ctx := tenant.Into(context.Background(), tc)
	roleID := uuid.New()
	now := sqliteent.SQLiteTime(time.Now())
	_, err := sqlDB.Exec("INSERT INTO opensheet_roles (id, org_id, name, permissions, created_at, updated_at) VALUES (?, ?, 'Clerk', '[]', ?, ?)",
		roleID.String(), tc.OrgID.String(), now, now)
	require.NoError(t, err)

	inv, err := invite.New(invite.NewParams{OrgID: tc.OrgID, Email: "c@x.co", Role: invite.RoleCustom, RoleID: roleID, TTL: time.Hour, Token: "raw-custom"})
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, inv))

	got, err := store.ByID(ctx, inv.ID)
	require.NoError(t, err)
	assert.Equal(t, roleID, got.RoleID)
	got, err = store.ByTokenHash(ctx, inv.TokenHash)
	require.NoError(t, err)
	assert.Equal(t, roleID, got.RoleID)
	pending, err := store.ListPending(ctx, tc.OrgID)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, roleID, pending[0].RoleID)
	pending, err = store.FindPendingForEmail(ctx, "c@x.co")
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, roleID, pending[0].RoleID)

	inv.Role, inv.RoleID = invite.RoleMember, uuid.Nil
	require.NoError(t, store.Save(ctx, inv))
	got, err = store.ByID(ctx, inv.ID)
	require.NoError(t, err)
	assert.Equal(t, uuid.Nil, got.RoleID, "a built-in role clears role_id")
}
