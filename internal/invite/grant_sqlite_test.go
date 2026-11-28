package invite_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/invite"
	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
	"altalune.id/opensheet/internal/platform/tenant"
)

func seedInviteProject(t *testing.T, sqlDB *sql.DB, orgID, createdBy uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	now := sqliteent.SQLiteTime(time.Now())
	_, err := sqlDB.Exec("INSERT INTO opensheet_projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES (?, ?, ?, 'P', ?, ?, ?)",
		id.String(), orgID.String(), "p-"+id.String()[:8], createdBy.String(), now, now)
	require.NoError(t, err)
	return id
}

func TestInviteStore_GrantRoundTrip(t *testing.T) {
	store, sqlDB, tc := newSQLiteStoreForTest(t)
	ctx := tenant.Into(context.Background(), tc)
	p1 := seedInviteProject(t, sqlDB, tc.OrgID, tc.UserID)
	p2 := seedInviteProject(t, sqlDB, tc.OrgID, tc.UserID)

	inv := newInviteForTest(t, tc.OrgID, "g@x.co", time.Hour)
	assert.True(t, inv.AllProjects, "a new invite reaches every project")
	inv.AllProjects, inv.ProjectIDs = false, []uuid.UUID{p1, p2}
	require.NoError(t, store.Save(ctx, inv))

	check := func(got *invite.Invite, want []uuid.UUID) {
		t.Helper()
		assert.False(t, got.AllProjects)
		assert.ElementsMatch(t, want, got.ProjectIDs)
	}
	got, err := store.ByID(ctx, inv.ID)
	require.NoError(t, err)
	check(got, []uuid.UUID{p1, p2})
	got, err = store.ByTokenHash(context.Background(), inv.TokenHash)
	require.NoError(t, err)
	check(got, []uuid.UUID{p1, p2})
	pending, err := store.ListPending(ctx, tc.OrgID)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	check(pending[0], []uuid.UUID{p1, p2})
	pending, err = store.FindPendingForEmail(context.Background(), "g@x.co")
	require.NoError(t, err)
	require.Len(t, pending, 1)
	check(pending[0], []uuid.UUID{p1, p2})

	inv.AllProjects, inv.ProjectIDs = true, nil
	now := time.Now().UTC()
	inv.UsedAt = &now
	require.NoError(t, store.Save(ctx, inv))
	got, err = store.ByID(ctx, inv.ID)
	require.NoError(t, err)
	require.NotNil(t, got.UsedAt)
	check(got, []uuid.UUID{p1, p2})
}

func TestInviteStore_GrantFilteredByOrg(t *testing.T) {
	store, sqlDB, tc := newSQLiteStoreForTest(t)
	ctx := tenant.Into(context.Background(), tc)
	_, otherOrg := seedTenant(t, sqlDB, "opensheet_")
	pa := seedInviteProject(t, sqlDB, tc.OrgID, tc.UserID)
	pb := seedInviteProject(t, sqlDB, otherOrg, tc.UserID)

	inv := newInviteForTest(t, tc.OrgID, "g@x.co", time.Hour)
	inv.AllProjects, inv.ProjectIDs = false, []uuid.UUID{pa}
	require.NoError(t, store.Save(ctx, inv))

	_, err := sqlDB.Exec("PRAGMA foreign_keys = OFF")
	require.NoError(t, err)
	_, err = sqlDB.Exec("INSERT INTO opensheet_invite_projects (org_id, invite_id, project_id, created_at) VALUES (?, ?, ?, ?)",
		otherOrg.String(), inv.ID.String(), pb.String(), sqliteent.SQLiteTime(time.Now()))
	require.NoError(t, err)
	_, err = sqlDB.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	got, err := store.ByID(ctx, inv.ID)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{pa}, got.ProjectIDs, "another org's grant row must not reach this invite")
	got, err = store.ByTokenHash(context.Background(), inv.TokenHash)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{pa}, got.ProjectIDs)

	require.NoError(t, store.Save(ctx, inv))
	var n int
	require.NoError(t, sqlDB.QueryRow("SELECT count(*) FROM opensheet_invite_projects WHERE org_id = ?", otherOrg.String()).Scan(&n))
	assert.Equal(t, 1, n, "saving this org's invite must not touch another org's rows")
}
