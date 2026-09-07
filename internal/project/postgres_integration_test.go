//go:build integration

package project_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/db"
	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/internal/project"
	"altalune.id/template/internal/testutil/pgtest"
	"altalune.id/template/schema"
)

func newProjectStoreForTest(t *testing.T) (project.Store, uuid.UUID, uuid.UUID) { //nolint:nonamedreturns // three heterogeneous returns
	t.Helper()
	h := pgtest.New(t)
	sqlDB := h.OpenDB(t)

	cfg := config.Defaults()
	cfg.DB.Driver = "postgres"
	cfg.DB.DSN = h.DSN
	cfg.DB.Schema = h.Schema
	cfg.DB.AllowBypassRLS = true

	require.NoError(t, schema.MigrateUp(t.Context(), sqlDB, cfg))

	prefix := cfg.DB.TablePrefix
	userID, orgID := seedUserAndOrg(t, sqlDB, prefix)

	pc := tenant.NewPgConn(sqlDB)
	store := project.NewStore(db.DBConfig{Driver: db.DriverPostgres, Schema: h.Schema, TablePrefix: prefix}, db.Pool{W: sqlDB, R: sqlDB}, pc)
	return store, orgID, userID
}

func seedUserAndOrg(t *testing.T, sqlDB *sql.DB, prefix string) (uuid.UUID, uuid.UUID) { //nolint:nonamedreturns // pair
	t.Helper()
	userID := uuid.New()
	orgID := uuid.New()
	now := time.Now().UTC()
	_, err := sqlDB.ExecContext(t.Context(),
		"INSERT INTO "+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ($1, $2, '', '', false, $3, $3)",
		userID, userID.String()+"@x.co", now,
	)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(t.Context(),
		"INSERT INTO "+prefix+"orgs (id, slug, name, created_by, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $5)",
		orgID, "acme", "Acme", userID, now,
	)
	require.NoError(t, err)
	return userID, orgID
}

func TestPostgres_Project_SaveAndLookup(t *testing.T) {
	store, orgID, userID := newProjectStoreForTest(t)
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: orgID, UserID: userID})

	p, err := project.New(orgID, "web", "Web")
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, p))

	byID, err := store.ByID(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, "web", byID.Slug)

	bySlug, err := store.BySlug(ctx, orgID, "web")
	require.NoError(t, err)
	assert.Equal(t, p.ID, bySlug.ID)
}

func TestPostgres_Project_NotFound(t *testing.T) {
	store, orgID, userID := newProjectStoreForTest(t)
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: orgID, UserID: userID})

	_, err := store.ByID(ctx, uuid.New())
	assert.True(t, project.IsNotFoundError(err), "ByID: want NotFoundError, got %T: %v", err, err)

	_, err = store.BySlug(ctx, orgID, "missing")
	assert.True(t, project.IsNotFoundError(err), "BySlug: want NotFoundError, got %T: %v", err, err)
}

func TestPostgres_Project_List(t *testing.T) {
	store, orgID, userID := newProjectStoreForTest(t)
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: orgID, UserID: userID})

	for _, s := range []string{"a", "b", "c"} {
		p, err := project.New(orgID, s, s)
		require.NoError(t, err)
		require.NoError(t, store.Save(ctx, p))
	}

	got, err := store.List(ctx, orgID)
	require.NoError(t, err)
	assert.Len(t, got, 3)
}

func TestPostgres_Project_SaveIsUpsert(t *testing.T) {
	store, orgID, userID := newProjectStoreForTest(t)
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: orgID, UserID: userID})

	p, err := project.New(orgID, "web", "Web")
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, p))

	p.Name = "Web v2"
	require.NoError(t, store.Save(ctx, p))

	got, err := store.ByID(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, "Web v2", got.Name)
}
