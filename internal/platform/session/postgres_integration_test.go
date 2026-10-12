//go:build integration

package session_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/sealer"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/testutil/pgtest"
	"altalune.id/opensheet/schema"
)

func newPostgresSessionDB(t *testing.T) (*sql.DB, db.DBConfig) {
	t.Helper()
	h := pgtest.New(t)
	sqlDB := h.OpenDB(t)

	cfg := config.Defaults()
	cfg.DB.Driver = "postgres"
	cfg.DB.DSN = h.DSN
	cfg.DB.Schema = h.Schema
	cfg.DB.AllowBypassRLS = true
	require.NoError(t, schema.MigrateUp(t.Context(), sqlDB, cfg))

	return sqlDB, db.DBConfig{
		Driver:      db.DriverPostgres,
		Schema:      h.Schema,
		TablePrefix: cfg.DB.TablePrefix,
	}
}

func seedPostgresUser(t *testing.T, sqlDB *sql.DB, cfg db.DBConfig) uuid.UUID {
	t.Helper()
	id := uuid.New()
	now := time.Now().UTC()
	_, err := sqlDB.ExecContext(t.Context(),
		"INSERT INTO "+cfg.Schema+"."+cfg.TablePrefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) "+
			"VALUES ($1, $2, '', '', false, $3, $3)",
		id, id.String()+"@example.com", now)
	require.NoError(t, err)
	return id
}

func TestPostgresStore_Contract(t *testing.T) {
	var (
		sqlDB *sql.DB
		cfg   db.DBConfig
	)
	runStoreContract(t,
		func(t *testing.T) session.Store {
			t.Helper()
			sqlDB, cfg = newPostgresSessionDB(t)
			return session.NewStore(cfg, db.Pool{W: sqlDB, R: sqlDB}, testSealer(t), nil)
		},
		func(t *testing.T) session.Principal {
			t.Helper()
			return session.Principal{UserID: seedPostgresUser(t, sqlDB, cfg)}
		},
	)
}

// SECURITY: the sessions row must never hold a readable IDToken — it is a live IdP bearer assertion.
func TestPostgresStore_PayloadIsSealedAtRest(t *testing.T) {
	sqlDB, cfg := newPostgresSessionDB(t)
	userID := seedPostgresUser(t, sqlDB, cfg)
	store := session.NewStore(cfg, db.Pool{W: sqlDB, R: sqlDB}, testSealer(t), nil)

	const token = "header.payload.SUPERSECRETSIGNATURE" //nolint:gosec // test fixture, not a credential
	require.NoError(t, store.Save(t.Context(), "sid-1",
		session.Principal{UserID: userID, Email: "a@b", IDToken: token},
		time.Now().Add(time.Hour)))

	var blob []byte
	require.NoError(t, sqlDB.QueryRowContext(t.Context(),
		"SELECT payload FROM "+cfg.Schema+"."+cfg.TablePrefix+"sessions WHERE sid = $1", "sid-1").Scan(&blob))
	require.NotEmpty(t, blob)
	require.NotContains(t, string(blob), token, "the ID token must not be readable in the row")
	require.NotContains(t, string(blob), "a@b", "the principal must not be readable in the row")
}

// DeleteExpired batches at 500; this drives more than one batch to prove the loop terminates and totals correctly.
func TestPostgresStore_DeleteExpiredSpansBatches(t *testing.T) {
	sqlDB, cfg := newPostgresSessionDB(t)
	userID := seedPostgresUser(t, sqlDB, cfg)
	store := session.NewStore(cfg, db.Pool{W: sqlDB, R: sqlDB}, testSealer(t), nil)

	const expired = 1201
	for i := range expired {
		require.NoError(t, store.Save(t.Context(),
			"dead-"+uuid.New().String(), session.Principal{UserID: userID},
			time.Now().Add(-time.Duration(i+1)*time.Second)))
	}
	require.NoError(t, store.Save(t.Context(), "live",
		session.Principal{UserID: userID}, time.Now().Add(time.Hour)))

	n, err := store.DeleteExpired(t.Context())
	require.NoError(t, err)
	require.Equal(t, expired, n)

	_, ok, err := store.Load(t.Context(), "live")
	require.NoError(t, err)
	require.True(t, ok, "the live session must survive a multi-batch sweep")
}

// A session outlives the process that created it — this is the whole point of the table.
func TestPostgresStore_SurvivesANewStoreInstance(t *testing.T) {
	sqlDB, cfg := newPostgresSessionDB(t)
	userID := seedPostgresUser(t, sqlDB, cfg)
	pool := db.Pool{W: sqlDB, R: sqlDB}

	require.NoError(t, session.NewStore(cfg, pool, testSealer(t), nil).Save(
		t.Context(), "sid-1", session.Principal{UserID: userID, Email: "a@b"},
		time.Now().Add(time.Hour)))

	p, ok, err := session.NewStore(cfg, pool, testSealer(t), nil).Load(t.Context(), "sid-1")
	require.NoError(t, err)
	require.True(t, ok, "a restart must not sign the holder out")
	require.Equal(t, "a@b", p.Email)
}

// A rotated key must log the holder out, not 500 them.
func TestPostgresStore_UnopenablePayloadIsNotSignedIn(t *testing.T) {
	sqlDB, cfg := newPostgresSessionDB(t)
	userID := seedPostgresUser(t, sqlDB, cfg)
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
