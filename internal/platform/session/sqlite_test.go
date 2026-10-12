package session_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
	"altalune.id/opensheet/internal/platform/sealer"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/schema"
)

func testSealer(t *testing.T) sealer.Sealer {
	t.Helper()
	key := make([]byte, sealer.KeyLen)
	for i := range key {
		key[i] = byte(i)
	}
	sl, err := sealer.New(key)
	require.NoError(t, err)
	return sl
}

func newSQLiteSessionDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	_, err = sqlDB.Exec("PRAGMA foreign_keys = ON;")
	require.NoError(t, err)
	cfg := config.Defaults()
	require.NoError(t, schema.MigrateUp(t.Context(), sqlDB, cfg))
	return sqlDB, cfg.DB.TablePrefix
}

func seedSessionUser(t *testing.T, sqlDB *sql.DB, prefix string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	now := sqliteent.SQLiteTime(time.Now())
	_, err := sqlDB.Exec(
		"INSERT INTO "+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) "+
			"VALUES (?, ?, '', '', 0, ?, ?)",
		id.String(), id.String()+"@example.com", now, now)
	require.NoError(t, err)
	return id
}

func TestSQLiteStore_Contract(t *testing.T) {
	var (
		sqlDB  *sql.DB
		prefix string
	)
	runStoreContract(t,
		func(t *testing.T) session.Store {
			t.Helper()
			sqlDB, prefix = newSQLiteSessionDB(t)
			return session.NewStore(
				db.DBConfig{Driver: db.DriverSQLite, TablePrefix: prefix},
				db.Pool{W: sqlDB, R: sqlDB},
				testSealer(t),
				nil,
			)
		},
		func(t *testing.T) session.Principal {
			t.Helper()
			return session.Principal{UserID: seedSessionUser(t, sqlDB, prefix)}
		},
	)
}

// SECURITY: the sessions row must never hold a readable IDToken — it is a live IdP bearer assertion.
func TestSQLiteStore_PayloadIsSealedAtRest(t *testing.T) {
	sqlDB, prefix := newSQLiteSessionDB(t)
	userID := seedSessionUser(t, sqlDB, prefix)
	store := session.NewStore(
		db.DBConfig{Driver: db.DriverSQLite, TablePrefix: prefix},
		db.Pool{W: sqlDB, R: sqlDB},
		testSealer(t),
		nil,
	)

	const token = "header.payload.SUPERSECRETSIGNATURE" //nolint:gosec // test fixture, not a credential
	require.NoError(t, store.Save(t.Context(), "sid-1",
		session.Principal{UserID: userID, Email: "a@b", IDToken: token},
		time.Now().Add(time.Hour)))

	var blob []byte
	require.NoError(t, sqlDB.QueryRow(
		"SELECT payload FROM "+prefix+"sessions WHERE sid = ?", "sid-1").Scan(&blob))
	require.NotEmpty(t, blob)
	require.NotContains(t, string(blob), token, "the ID token must not be readable in the row")
	require.NotContains(t, string(blob), "a@b", "the principal must not be readable in the row")
}

// SECURITY: the AAD is the sid, so a row copied under a different sid must not open.
func TestSQLiteStore_PayloadIsBoundToItsSID(t *testing.T) {
	sqlDB, prefix := newSQLiteSessionDB(t)
	userID := seedSessionUser(t, sqlDB, prefix)
	store := session.NewStore(
		db.DBConfig{Driver: db.DriverSQLite, TablePrefix: prefix},
		db.Pool{W: sqlDB, R: sqlDB},
		testSealer(t),
		nil,
	)
	require.NoError(t, store.Save(t.Context(), "sid-1",
		session.Principal{UserID: userID, Email: "a@b"}, time.Now().Add(time.Hour)))

	_, err := sqlDB.Exec(
		"INSERT INTO " + prefix + "sessions (sid, user_id, payload, expires_at, created_at) " +
			"SELECT 'sid-2', user_id, payload, expires_at, created_at FROM " + prefix + "sessions WHERE sid = 'sid-1'")
	require.NoError(t, err)

	_, ok, err := store.Load(t.Context(), "sid-2")
	require.NoError(t, err, "a replayed row logs the holder out rather than failing the request")
	require.False(t, ok, "a payload sealed for sid-1 must not open under sid-2")
}

// A rotated key must log the holder out, not 500 them.
func TestSQLiteStore_UnopenablePayloadIsNotSignedIn(t *testing.T) {
	sqlDB, prefix := newSQLiteSessionDB(t)
	userID := seedSessionUser(t, sqlDB, prefix)
	cfg := db.DBConfig{Driver: db.DriverSQLite, TablePrefix: prefix}
	pool := db.Pool{W: sqlDB, R: sqlDB}

	require.NoError(t, session.NewStore(cfg, pool, testSealer(t), nil).Save(
		t.Context(), "sid-1", session.Principal{UserID: userID}, time.Now().Add(time.Hour)))

	rotated := make([]byte, sealer.KeyLen)
	for i := range rotated {
		rotated[i] = byte(255 - i)
	}
	sl, err := sealer.New(rotated)
	require.NoError(t, err)

	_, ok, err := session.NewStore(cfg, pool, sl, nil).Load(t.Context(), "sid-1")
	require.NoError(t, err)
	require.False(t, ok)
}
