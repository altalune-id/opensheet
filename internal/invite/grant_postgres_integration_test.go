//go:build integration

package invite_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/invite"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/pgtest"
	"altalune.id/opensheet/schema"
)

func TestPostgres_Invite_GrantRoundTrip(t *testing.T) {
	h := pgtest.New(t)
	sqlDB := h.OpenDB(t)
	cfg := config.Defaults()
	cfg.DB.Driver = "postgres"
	cfg.DB.DSN = h.DSN
	cfg.DB.Schema = h.Schema
	cfg.DB.AllowBypassRLS = true
	require.NoError(t, schema.MigrateUp(t.Context(), sqlDB, cfg))
	prefix := cfg.DB.TablePrefix
	userID, orgID := seedInviteUserAndOrg(t, sqlDB, prefix)
	p1, p2 := uuid.New(), uuid.New()
	_, err := sqlDB.ExecContext(t.Context(), "INSERT INTO "+prefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES ($1, $3, 'p1', 'P', $4, now(), now()), ($2, $3, 'p2', 'P', $4, now(), now())", p1, p2, orgID, userID)
	require.NoError(t, err)
	store := invite.NewStore(db.DBConfig{Driver: db.DriverPostgres, Schema: h.Schema, TablePrefix: prefix}, db.Pool{W: sqlDB, R: sqlDB}, tenant.NewPgConn(sqlDB))
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: orgID, UserID: userID})

	inv := newInvite(t, orgID, "g@x.co", "raw-grant")
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
	got, err = store.ByTokenHash(t.Context(), inv.TokenHash)
	require.NoError(t, err)
	check(got, []uuid.UUID{p1, p2})
	pending, err := store.ListPending(ctx, orgID)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	check(pending[0], []uuid.UUID{p1, p2})
	pending, err = store.FindPendingForEmail(t.Context(), "g@x.co")
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
