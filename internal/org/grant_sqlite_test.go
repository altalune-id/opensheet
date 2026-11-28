package org_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/org"
	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
)

func seedProject(t *testing.T, sqlDB *sql.DB, prefix string, orgID, createdBy uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	now := sqliteent.SQLiteTime(time.Now())
	_, err := sqlDB.Exec("INSERT INTO "+prefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES (?, ?, ?, 'P', ?, ?, ?)",
		id.String(), orgID.String(), "p-"+id.String()[:8], createdBy.String(), now, now)
	require.NoError(t, err)
	return id
}

func TestOrgStore_GrantRoundTrip(t *testing.T) {
	store, sqlDB, prefix := newSQLiteStoreForTest(t)
	ctx := context.Background()
	owner := seedUser(t, sqlDB, prefix)
	member := seedUser(t, sqlDB, prefix)
	o, err := org.NewOrg("acme", "Acme", owner)
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, o))
	p1 := seedProject(t, sqlDB, prefix, o.ID, owner)
	p2 := seedProject(t, sqlDB, prefix, o.ID, owner)

	m, err := org.NewMembership(o.ID, member, org.RoleMember)
	require.NoError(t, err)
	require.NoError(t, store.SaveMembership(ctx, m))
	require.NoError(t, store.SetProjectGrant(ctx, o.ID, member, org.ProjectGrant{ProjectIDs: []uuid.UUID{p1, p2}}))

	got, err := store.MembershipOf(ctx, o.ID, member)
	require.NoError(t, err)
	assert.False(t, got.AllProjects)
	assert.ElementsMatch(t, []uuid.UUID{p1, p2}, got.ProjectIDs)
	members, err := store.ListMembers(ctx, o.ID)
	require.NoError(t, err)
	require.Len(t, members, 1)
	assert.ElementsMatch(t, []uuid.UUID{p1, p2}, members[0].ProjectIDs)
	profiles, err := store.ListMemberProfiles(ctx, o.ID)
	require.NoError(t, err)
	require.Len(t, profiles, 1)
	assert.False(t, profiles[0].AllProjects)
	assert.ElementsMatch(t, []uuid.UUID{p1, p2}, profiles[0].ProjectIDs)

	require.NoError(t, store.SetProjectGrant(ctx, o.ID, member, org.ProjectGrant{ProjectIDs: []uuid.UUID{p2}}))
	got, err = store.MembershipOf(ctx, o.ID, member)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{p2}, got.ProjectIDs, "a narrowed grant drops the removed project")

	_, err = sqlDB.Exec("DELETE FROM "+prefix+"projects WHERE id = ?", p2.String())
	require.NoError(t, err)
	got, err = store.MembershipOf(ctx, o.ID, member)
	require.NoError(t, err, "a restricted membership whose last project is gone still reads")
	assert.False(t, got.AllProjects)
	assert.Empty(t, got.ProjectIDs)

	require.NoError(t, store.SetProjectGrant(ctx, o.ID, member, org.ProjectGrant{All: true}))
	got, err = store.MembershipOf(ctx, o.ID, member)
	require.NoError(t, err)
	assert.True(t, got.AllProjects)
	assert.Empty(t, got.ProjectIDs)
	var rows int
	require.NoError(t, sqlDB.QueryRow("SELECT count(*) FROM "+prefix+"membership_projects").Scan(&rows))
	assert.Zero(t, rows, "an all-projects grant keeps no rows")

	err = store.SetProjectGrant(ctx, o.ID, uuid.New(), org.ProjectGrant{All: true})
	assert.True(t, org.IsMembershipMissingError(err), "%T %v", err, err)
}

func TestOrgStore_SaveMembershipNeverWritesTheGrant(t *testing.T) {
	store, sqlDB, prefix := newSQLiteStoreForTest(t)
	ctx := context.Background()
	owner := seedUser(t, sqlDB, prefix)
	member := seedUser(t, sqlDB, prefix)
	o, err := org.NewOrg("acme", "Acme", owner)
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, o))
	p1 := seedProject(t, sqlDB, prefix, o.ID, owner)

	require.NoError(t, store.SaveMembership(ctx, &org.Membership{OrgID: o.ID, UserID: owner, Role: org.RoleOwner, CreatedAt: time.Now().UTC()}),
		"a literal owner without AllProjects must not trip the owner CHECK")
	require.NoError(t, store.SaveMembership(ctx, &org.Membership{OrgID: o.ID, UserID: member, Role: org.RoleMember, CreatedAt: time.Now().UTC()}))
	got, err := store.MembershipOf(ctx, o.ID, member)
	require.NoError(t, err)
	assert.True(t, got.AllProjects, "a new membership takes the all-projects default")

	require.NoError(t, store.SetProjectGrant(ctx, o.ID, member, org.ProjectGrant{ProjectIDs: []uuid.UUID{p1}}))
	got.Role, got.AllProjects, got.ProjectIDs = org.RoleAdmin, true, nil
	require.NoError(t, store.SaveMembership(ctx, got))
	got, err = store.MembershipOf(ctx, o.ID, member)
	require.NoError(t, err)
	assert.Equal(t, org.RoleAdmin, got.Role)
	assert.False(t, got.AllProjects, "a role change keeps the grant")
	assert.Equal(t, []uuid.UUID{p1}, got.ProjectIDs)
}

func TestOrgStore_GrantFilteredByOrg(t *testing.T) {
	store, sqlDB, prefix := newSQLiteStoreForTest(t)
	ctx := context.Background()
	owner := seedUser(t, sqlDB, prefix)
	a, err := org.NewOrg("org-a", "A", owner)
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, a))
	b, err := org.NewOrg("org-b", "B", owner)
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, b))
	pa := seedProject(t, sqlDB, prefix, a.ID, owner)
	pb := seedProject(t, sqlDB, prefix, b.ID, owner)

	m, err := org.NewMembership(a.ID, owner, org.RoleMember)
	require.NoError(t, err)
	require.NoError(t, store.SaveMembership(ctx, m))
	require.NoError(t, store.SetProjectGrant(ctx, a.ID, owner, org.ProjectGrant{ProjectIDs: []uuid.UUID{pa}}))

	var membershipID string
	require.NoError(t, sqlDB.QueryRow("SELECT id FROM "+prefix+"memberships WHERE org_id = ?", a.ID.String()).Scan(&membershipID))
	_, err = sqlDB.Exec("PRAGMA foreign_keys = OFF")
	require.NoError(t, err)
	_, err = sqlDB.Exec("INSERT INTO "+prefix+"membership_projects (org_id, membership_id, project_id, created_at) VALUES (?, ?, ?, ?)",
		b.ID.String(), membershipID, pb.String(), sqliteent.SQLiteTime(time.Now()))
	require.NoError(t, err)
	_, err = sqlDB.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	got, err := store.MembershipOf(ctx, a.ID, owner)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{pa}, got.ProjectIDs, "org B's grant row must not reach org A's membership")
	members, err := store.ListMembers(ctx, a.ID)
	require.NoError(t, err)
	require.Len(t, members, 1)
	assert.Equal(t, []uuid.UUID{pa}, members[0].ProjectIDs)
	profiles, err := store.ListMemberProfiles(ctx, a.ID)
	require.NoError(t, err)
	require.Len(t, profiles, 1)
	assert.Equal(t, []uuid.UUID{pa}, profiles[0].ProjectIDs)

	require.NoError(t, store.SetProjectGrant(ctx, a.ID, owner, org.ProjectGrant{ProjectIDs: []uuid.UUID{pa}}))
	var n int
	require.NoError(t, sqlDB.QueryRow("SELECT count(*) FROM "+prefix+"membership_projects WHERE org_id = ?", b.ID.String()).Scan(&n))
	assert.Equal(t, 1, n, "setting org A's grant must not touch org B's rows")
}
