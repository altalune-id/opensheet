package apikey_test

import (
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/schema"
)

const prefix = "opensheet_"

func newAPIKeyStoreForTest(t testing.TB) (apikey.Store, *sql.DB, tenant.Context) {
	t.Helper()

	cfg := config.Defaults()
	cfg.DB.Driver = db.DriverSQLite
	cfg.DB.DSN = filepath.Join(t.TempDir(), "apikey.db")

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	sqlDB, err := db.Open(t.Context(), cfg.DB, log)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, schema.MigrateUp(t.Context(), sqlDB, cfg))
	require.Equal(t, prefix, cfg.DB.TablePrefix)

	tc := seedTenant(t, sqlDB)
	return apikey.NewStore(
		db.DBConfig{Driver: db.DriverSQLite, TablePrefix: prefix},
		db.Pool{W: sqlDB, R: sqlDB},
		nil,
	), sqlDB, tc
}

func seedTenant(t testing.TB, sqlDB *sql.DB) tenant.Context {
	t.Helper()
	userID, orgID, projID := uuid.New(), uuid.New(), uuid.New()
	now := sqliteent.SQLiteTime(time.Now())

	_, err := sqlDB.Exec(
		"INSERT INTO "+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES (?, ?, '', '', 0, ?, ?)",
		userID.String(), userID.String()+"@x.com", now, now)
	require.NoError(t, err)

	_, err = sqlDB.Exec(
		"INSERT INTO "+prefix+"orgs (id, slug, name, created_by, created_at, updated_at) VALUES (?, ?, 'Org', ?, ?, ?)",
		orgID.String(), orgID.String()[:8], userID.String(), now, now)
	require.NoError(t, err)

	_, err = sqlDB.Exec(
		"INSERT INTO "+prefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES (?, ?, ?, 'Web', ?, ?, ?)",
		projID.String(), orgID.String(), projID.String()[:8], userID.String(), now, now)
	require.NoError(t, err)

	return tenant.Context{OrgID: orgID, ProjectID: projID, UserID: userID}
}

func seedProject(t *testing.T, sqlDB *sql.DB, tc tenant.Context) uuid.UUID {
	t.Helper()
	projID := uuid.New()
	now := sqliteent.SQLiteTime(time.Now())
	_, err := sqlDB.Exec(
		"INSERT INTO "+prefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES (?, ?, ?, 'Docs', ?, ?, ?)",
		projID.String(), tc.OrgID.String(), projID.String()[:8], tc.UserID.String(), now, now)
	require.NoError(t, err)
	return projID
}

func TestSQLiteStore_SaveAndByID(t *testing.T) {
	store, _, tc := newAPIKeyStoreForTest(t)
	ctx := tenant.Into(t.Context(), tc)

	k, plaintext, err := apikey.Scheme{}.Mint(tc.OrgID, tc.ProjectID, "CI key", []string{authn.ScopeSheetsRead}, nil, nil, time.Now())
	require.NoError(t, err)
	require.NotEmpty(t, plaintext)
	require.NoError(t, store.Save(ctx, k))

	got, err := store.ByID(ctx, k.ID)
	require.NoError(t, err)
	assert.Equal(t, k.ID, got.ID)
	assert.Equal(t, tc.OrgID, got.OrgID)
	assert.Equal(t, tc.ProjectID, got.ProjectID)
	assert.Equal(t, "CI key", got.Name)
	assert.Equal(t, []string{authn.ScopeSheetsRead}, got.Scopes)
	assert.Empty(t, got.ResourceIDs)
	assert.Equal(t, k.SecretHash, got.SecretHash)
	assert.Nil(t, got.ExpiresAt)
	assert.Nil(t, got.RevokedAt)
	assert.Nil(t, got.LastUsedAt)
	assert.True(t, got.CreatedAt.Equal(k.CreatedAt), "CreatedAt round-trip: got=%v want=%v", got.CreatedAt, k.CreatedAt)
	assert.True(t, got.Matches(plaintext))
}

func TestSQLiteStore_SaveRoundTripsResourceIDsAndExpiry(t *testing.T) {
	store, _, tc := newAPIKeyStoreForTest(t)
	ctx := tenant.Into(t.Context(), tc)

	resourceIDs := []uuid.UUID{uuid.New(), uuid.New()}
	expiresAt := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	k, _, err := apikey.Scheme{}.Mint(tc.OrgID, tc.ProjectID, "Scoped key", []string{authn.ScopeSheetsRead, authn.ScopeSheetsWrite}, resourceIDs, &expiresAt, time.Now())
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, k))

	got, err := store.ByID(ctx, k.ID)
	require.NoError(t, err)
	assert.ElementsMatch(t, resourceIDs, got.ResourceIDs)
	assert.ElementsMatch(t, []string{authn.ScopeSheetsRead, authn.ScopeSheetsWrite}, got.Scopes)
	require.NotNil(t, got.ExpiresAt)
	assert.True(t, got.ExpiresAt.Equal(expiresAt), "ExpiresAt round-trip: got=%v want=%v", got.ExpiresAt, expiresAt)
}

func TestSQLiteStore_ByID_NotFound(t *testing.T) {
	store, _, tc := newAPIKeyStoreForTest(t)
	ctx := tenant.Into(t.Context(), tc)

	_, err := store.ByID(ctx, uuid.New())
	assert.True(t, apikey.IsNotFoundError(err), "got %T: %v", err, err)
}

func TestSQLiteStore_BySecretHash(t *testing.T) {
	store, _, tc := newAPIKeyStoreForTest(t)
	ctx := tenant.Into(t.Context(), tc)

	k, plaintext, err := apikey.Scheme{}.Mint(tc.OrgID, tc.ProjectID, "Hash lookup", []string{authn.ScopeSheetsRead}, nil, nil, time.Now())
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, k))

	got, err := store.BySecretHash(ctx, k.SecretHash)
	require.NoError(t, err)
	assert.Equal(t, k.ID, got.ID)
	assert.True(t, got.Matches(plaintext))
}

func TestSQLiteStore_BySecretHash_Miss(t *testing.T) {
	store, _, tc := newAPIKeyStoreForTest(t)
	ctx := tenant.Into(t.Context(), tc)

	var missing [32]byte
	_, err := store.BySecretHash(ctx, missing)
	assert.True(t, apikey.IsNotFoundError(err), "got %T: %v", err, err)
}

func TestSQLiteStore_List_ScopesToTheProject(t *testing.T) {
	store, sqlDB, tc := newAPIKeyStoreForTest(t)
	ctx := tenant.Into(t.Context(), tc)
	otherProj := seedProject(t, sqlDB, tc)

	mine, _, err := apikey.Scheme{}.Mint(tc.OrgID, tc.ProjectID, "Mine", []string{authn.ScopeSheetsRead}, nil, nil, time.Now())
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, mine))

	theirs, _, err := apikey.Scheme{}.Mint(tc.OrgID, otherProj, "Theirs", []string{authn.ScopeSheetsRead}, nil, nil, time.Now())
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, theirs))

	got, err := store.List(ctx, tc.ProjectID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, mine.ID, got[0].ID)

	gotOther, err := store.List(ctx, otherProj)
	require.NoError(t, err)
	require.Len(t, gotOther, 1)
	assert.Equal(t, theirs.ID, gotOther[0].ID)
}

func TestSQLiteStore_TouchLastUsed(t *testing.T) {
	store, _, tc := newAPIKeyStoreForTest(t)
	ctx := tenant.Into(t.Context(), tc)

	k, _, err := apikey.Scheme{}.Mint(tc.OrgID, tc.ProjectID, "Touched", []string{authn.ScopeSheetsRead}, nil, nil, time.Now())
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, k))

	got, err := store.ByID(ctx, k.ID)
	require.NoError(t, err)
	assert.Nil(t, got.LastUsedAt)

	at := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, store.TouchLastUsed(ctx, k.ID, at))

	got, err = store.ByID(ctx, k.ID)
	require.NoError(t, err)
	require.NotNil(t, got.LastUsedAt)
	assert.True(t, got.LastUsedAt.Equal(at), "LastUsedAt round-trip: got=%v want=%v", got.LastUsedAt, at)
}

func TestSQLiteStore_TouchLastUsed_NotFound(t *testing.T) {
	store, _, tc := newAPIKeyStoreForTest(t)
	ctx := tenant.Into(t.Context(), tc)

	err := store.TouchLastUsed(ctx, uuid.New(), time.Now())
	assert.True(t, apikey.IsNotFoundError(err), "got %T: %v", err, err)
}
