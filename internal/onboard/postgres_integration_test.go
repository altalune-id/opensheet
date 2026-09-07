//go:build integration

package onboard_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/onboard"
	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/db"
	"altalune.id/template/internal/testutil/pgtest"
	"altalune.id/template/schema"
)

func newOnboardStoreForTest(t *testing.T) (onboard.Store, uuid.UUID) { //nolint:nonamedreturns // pair
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
	userID := seedOnboardUser(t, sqlDB, prefix)

	store := onboard.NewStore(db.DBConfig{Driver: db.DriverPostgres, Schema: h.Schema, TablePrefix: prefix}, db.Pool{W: sqlDB, R: sqlDB})
	return store, userID
}

func seedOnboardUser(t *testing.T, sqlDB *sql.DB, prefix string) uuid.UUID {
	t.Helper()
	userID := uuid.New()
	now := time.Now().UTC()
	_, err := sqlDB.ExecContext(t.Context(),
		"INSERT INTO "+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ($1, $2, '', '', false, $3, $3)",
		userID, userID.String()+"@x.co", now,
	)
	require.NoError(t, err)
	return userID
}

func TestPostgres_Onboard_GetEmpty(t *testing.T) {
	store, _ := newOnboardStoreForTest(t)
	_, err := store.Get(t.Context())
	assert.True(t, onboard.IsNotOnboardedError(err), "want NotOnboardedError, got %T: %v", err, err)
}

func TestPostgres_Onboard_SaveAndGet(t *testing.T) {
	store, userID := newOnboardStoreForTest(t)
	ctx := t.Context()

	b, err := onboard.New(userID, onboard.MethodWebOnboard, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, b))

	got, err := store.Get(ctx)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, userID, got.OnboardedBy)
	assert.Equal(t, onboard.MethodWebOnboard, got.Method)
}

func TestPostgres_Onboard_SaveIsSingleton(t *testing.T) {
	store, userID := newOnboardStoreForTest(t)
	ctx := t.Context()

	b1, err := onboard.New(userID, onboard.MethodEnvGenesis, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, b1))

	b2, err := onboard.New(userID, onboard.MethodCLIInit, time.Now().UTC())
	require.NoError(t, err)
	err = store.Save(ctx, b2)
	assert.True(t, onboard.IsAlreadyOnboardedError(err), "want AlreadyOnboardedError, got %T: %v", err, err)

	got, err := store.Get(ctx)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, onboard.MethodEnvGenesis, got.Method, "singleton row should not be overwritten")
}
