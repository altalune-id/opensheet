//go:build integration

package user_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/db"
	"altalune.id/template/internal/testutil/pgtest"
	"altalune.id/template/internal/user"
	"altalune.id/template/schema"
)

func newPostgresStoreForTest(t *testing.T) user.Store {
	t.Helper()
	h := pgtest.New(t)
	sqlDB := h.OpenDB(t)

	cfg := config.Defaults()
	cfg.DB.Driver = "postgres"
	cfg.DB.DSN = h.DSN
	cfg.DB.Schema = h.Schema
	cfg.DB.AllowBypassRLS = true

	require.NoError(t, schema.MigrateUp(t.Context(), sqlDB, cfg))

	return user.NewStore(db.DBConfig{Driver: db.DriverPostgres, Schema: h.Schema, TablePrefix: cfg.DB.TablePrefix}, db.Pool{W: sqlDB, R: sqlDB})
}

func TestPostgres_User_SaveAndLookup(t *testing.T) {
	store := newPostgresStoreForTest(t)
	ctx := t.Context()

	u, err := user.New("alice@example.com", "Alice", user.SourceLocal)
	require.NoError(t, err)
	u.PasswordHash = "$argon2id$stub"
	require.NoError(t, store.Save(ctx, u))

	byID, err := store.ByID(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, "alice@example.com", byID.Email)
	assert.Equal(t, "Alice", byID.Name)

	byEmail, err := store.ByEmail(ctx, "alice@example.com")
	require.NoError(t, err)
	assert.Equal(t, u.ID, byEmail.ID)
}

func TestPostgres_User_NotFound(t *testing.T) {
	store := newPostgresStoreForTest(t)
	ctx := t.Context()

	u, err := user.New("solo@example.com", "Solo", user.SourceLocal)
	require.NoError(t, err)

	_, err = store.ByID(ctx, u.ID)
	assert.True(t, user.IsNotFoundError(err), "want NotFoundError, got %T: %v", err, err)

	_, err = store.ByEmail(ctx, "missing@example.com")
	assert.True(t, user.IsNotFoundError(err), "want NotFoundError, got %T: %v", err, err)
}

func TestPostgres_User_HasLocalUsers(t *testing.T) {
	store := newPostgresStoreForTest(t)
	ctx := t.Context()

	ok, err := store.HasLocalUsers(ctx)
	require.NoError(t, err)
	assert.False(t, ok)

	u, err := user.New("local@example.com", "Local", user.SourceLocal)
	require.NoError(t, err)
	u.PasswordHash = "hash"
	require.NoError(t, store.Save(ctx, u))

	ok, err = store.HasLocalUsers(ctx)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestPostgres_User_UpdateLocale(t *testing.T) {
	store := newPostgresStoreForTest(t)
	ctx := t.Context()

	u, err := user.New("loc@example.com", "Loc", user.SourceLocal)
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, u))

	require.NoError(t, store.UpdateLocale(ctx, u.ID, "id-ID"))

	got, err := store.ByID(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, "id-ID", got.Locale)
}

func TestPostgres_User_SaveIsUpsert(t *testing.T) {
	store := newPostgresStoreForTest(t)
	ctx := t.Context()

	u, err := user.New("up@example.com", "First", user.SourceLocal)
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, u))

	u.Name = "Renamed"
	u.TermsAcceptedAt = ptrTime(time.Now().UTC())
	require.NoError(t, store.Save(ctx, u))

	got, err := store.ByID(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, "Renamed", got.Name)
	require.NotNil(t, got.TermsAcceptedAt)
}

func ptrTime(t time.Time) *time.Time { return &t }
