package org_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/org"
	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
)

func TestSQLite_MembershipRoleIDRoundTrip(t *testing.T) {
	store, sqlDB, prefix := newSQLiteStoreForTest(t)
	ctx := context.Background()
	owner := seedUser(t, sqlDB, prefix)
	o, err := org.NewOrg("acme", "Acme", owner)
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, o))
	roleID := uuid.New()
	now := sqliteent.SQLiteTime(time.Now())
	_, err = sqlDB.Exec("INSERT INTO "+prefix+"roles (id, org_id, name, permissions, created_at, updated_at) VALUES (?, ?, 'Clerk', '[]', ?, ?)",
		roleID.String(), o.ID.String(), now, now)
	require.NoError(t, err)

	m := &org.Membership{OrgID: o.ID, UserID: owner, Role: org.RoleCustom, RoleID: roleID, CreatedAt: time.Now().UTC()}
	require.NoError(t, store.SaveMembership(ctx, m))

	got, err := store.MembershipOf(ctx, o.ID, owner)
	require.NoError(t, err)
	assert.Equal(t, org.RoleCustom, got.Role)
	assert.Equal(t, roleID, got.RoleID)
	members, err := store.ListMembers(ctx, o.ID)
	require.NoError(t, err)
	require.Len(t, members, 1)
	assert.Equal(t, roleID, members[0].RoleID)
	profiles, err := store.ListMemberProfiles(ctx, o.ID)
	require.NoError(t, err)
	require.Len(t, profiles, 1)
	assert.Equal(t, roleID, profiles[0].RoleID)

	m.Role, m.RoleID = org.RoleMember, uuid.Nil
	require.NoError(t, store.SaveMembership(ctx, m))
	got, err = store.MembershipOf(ctx, o.ID, owner)
	require.NoError(t, err)
	assert.Equal(t, org.RoleMember, got.Role)
	assert.Equal(t, uuid.Nil, got.RoleID, "a built-in role clears role_id")
}
