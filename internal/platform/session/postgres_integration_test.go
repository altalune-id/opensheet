//go:build integration

package session_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/sealer"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/testutil/pgtest"
	"altalune.id/opensheet/nanoid"
	"altalune.id/opensheet/schema"
)

// NOTE: must mirror session.deleteExpiredBatch, which is unexported and out of reach from this package.
const pgDeleteExpiredBatch = 500

type pgSessionFixture struct {
	ownerDB *sql.DB
	appDB   *sql.DB
	prefix  string
	dbCfg   db.DBConfig
	sealer  sealer.Sealer
}

func newPgSessionFixture(t *testing.T) *pgSessionFixture {
	t.Helper()
	h := pgtest.New(t)
	suffix := pgUniqueSuffix(t)
	ownerRole := "opensheet_sessowner_" + suffix
	appRole := "opensheet_sessapp_" + suffix
	prefix := "x" + suffix + "_"

	admin, err := db.Open(t.Context(), db.DBConfig{Driver: db.DriverPostgres, DSN: h.DSN}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })

	pgCreateRole(t, admin, ownerRole, "NOLOGIN BYPASSRLS")
	_, err = admin.ExecContext(t.Context(), fmt.Sprintf(`GRANT USAGE, CREATE ON SCHEMA public TO %q`, ownerRole))
	require.NoError(t, err)

	ownerDB, err := db.Open(t.Context(), db.DBConfig{
		Driver: db.DriverPostgres, DSN: h.DSN, Role: ownerRole, MaxOpenConns: 1,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ownerDB.Close() })

	cfg := config.Defaults()
	cfg.DB.Driver = "postgres"
	cfg.DB.Schema = "public"
	cfg.DB.TablePrefix = prefix
	cfg.Tenant.RLSEnforce = true
	require.NoError(t, schema.MigrateUp(t.Context(), ownerDB, cfg))

	pgCreateRole(t, admin, appRole, "LOGIN PASSWORD 'pw' NOBYPASSRLS")
	_, err = admin.ExecContext(t.Context(), fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO %q`, appRole))
	require.NoError(t, err)
	_, err = ownerDB.ExecContext(t.Context(),
		fmt.Sprintf(`GRANT SELECT, INSERT, UPDATE, DELETE ON public.%ssessions TO %q`, prefix, appRole))
	require.NoError(t, err)

	appDB, err := db.Open(t.Context(), db.DBConfig{
		Driver: db.DriverPostgres, DSN: pgtest.DSNWithUser(t, h.DSN, appRole, "pw"), MaxOpenConns: 2,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = appDB.Close() })

	var bypass bool
	require.NoError(t, appDB.QueryRowContext(t.Context(),
		`SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&bypass))
	require.False(t, bypass, "the store must run as a NOBYPASSRLS role or this proves nothing about the deployed grants")

	return &pgSessionFixture{
		ownerDB: ownerDB,
		appDB:   appDB,
		prefix:  prefix,
		dbCfg:   db.DBConfig{Driver: db.DriverPostgres, Schema: "public", TablePrefix: prefix},
		sealer:  newTestSealer(t),
	}
}

func (f *pgSessionFixture) newStore(unexpected apperror.UnexpectedFunc) session.Store {
	return session.NewStore(f.dbCfg, db.Pool{W: f.appDB, R: f.appDB}, f.sealer, unexpected)
}

// NOTE: without this, one subtest's expired row is swept by the next subtest's DeleteExpired count.
func (f *pgSessionFixture) reset(t *testing.T) {
	t.Helper()
	_, err := f.ownerDB.ExecContext(t.Context(), "TRUNCATE TABLE public."+f.prefix+"sessions")
	require.NoError(t, err)
}

func (f *pgSessionFixture) seedUser(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := f.ownerDB.ExecContext(t.Context(),
		"INSERT INTO "+f.prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) "+
			"VALUES ($1,$2,'','',false,$3,$3)",
		id, id.String()+"@x.co", time.Now().UTC())
	require.NoError(t, err)
	return id
}

// NOTE: pgtest reuses TEST_PG_DSN when set, so role and table names must be unique per run.
func pgUniqueSuffix(t *testing.T) string {
	t.Helper()
	s, err := nanoid.New(10)
	require.NoError(t, err)
	return strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(s))
}

func pgCreateRole(t *testing.T, admin *sql.DB, name, attrs string) {
	t.Helper()
	_, err := admin.ExecContext(t.Context(), fmt.Sprintf(`CREATE ROLE %q %s`, name, attrs))
	require.NoError(t, err)
	// NOTE: t.Context() is already canceled by the time cleanups run, so teardown needs its own context.
	t.Cleanup(func() {
		_, dropErr := admin.ExecContext(context.Background(), fmt.Sprintf(`DROP OWNED BY %q`, name))
		require.NoError(t, dropErr, "leaked objects owned by %s", name)
		_, dropErr = admin.ExecContext(context.Background(), fmt.Sprintf(`DROP ROLE IF EXISTS %q`, name))
		require.NoError(t, dropErr, "leaked role %s", name)
	})
	_, err = admin.ExecContext(t.Context(), fmt.Sprintf(`GRANT %q TO CURRENT_USER`, name))
	require.NoError(t, err)
}

func TestPgStore_Contract(t *testing.T) {
	f := newPgSessionFixture(t)
	runStoreContract(t,
		func(t *testing.T) session.Store {
			f.reset(t)
			return f.newStore(nil)
		},
		func(t *testing.T) session.Principal {
			return session.Principal{UserID: f.seedUser(t)}
		},
	)
}

func TestPgStore_SurvivesANewStoreInstance(t *testing.T) {
	f := newPgSessionFixture(t)
	ctx := t.Context()
	uid := f.seedUser(t)

	first := f.newStore(nil)
	require.NoError(t, first.Save(ctx, "restart-sid", session.Principal{UserID: uid, Email: "a@b"}, time.Now().Add(time.Hour)))

	second := f.newStore(nil)
	p, ok, err := second.Load(ctx, "restart-sid")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, uid, p.UserID)
	assert.Equal(t, "a@b", p.Email)
}

func TestPgStore_WorksAsTheNobypassrlsServiceRole(t *testing.T) {
	f := newPgSessionFixture(t)
	ctx := t.Context()

	var bypass bool
	require.NoError(t, f.appDB.QueryRowContext(ctx,
		`SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&bypass))
	require.False(t, bypass)

	// SECURITY: no tenant scope is set here on purpose — a session is loaded before any org scope exists.
	st := f.newStore(nil)
	uid := f.seedUser(t)
	require.NoError(t, st.Save(ctx, "norls-sid", session.Principal{UserID: uid}, time.Now().Add(time.Hour)))

	p, ok, err := st.Load(ctx, "norls-sid")
	require.NoError(t, err)
	require.True(t, ok, "an RLS policy on sessions would return zero rows here and nobody could sign in")
	assert.Equal(t, uid, p.UserID)

	require.NoError(t, st.Delete(ctx, "norls-sid"))
}

func TestPgStore_DeleteExpiredBatchingTerminates(t *testing.T) {
	f := newPgSessionFixture(t)
	ctx := t.Context()
	uid := f.seedUser(t)
	rows := pgDeleteExpiredBatch + 10

	_, err := f.ownerDB.ExecContext(ctx,
		"INSERT INTO "+f.prefix+"sessions (sid, user_id, payload, expires_at, created_at) "+
			"SELECT 'expired-' || g, $1, '\\x00'::bytea, now() - interval '1 hour', now() "+
			"FROM generate_series(1, $2) g",
		uid, rows)
	require.NoError(t, err)
	require.NoError(t, f.newStore(nil).Save(ctx, "keeper", session.Principal{UserID: uid}, time.Now().Add(time.Hour)))

	n, err := f.newStore(nil).DeleteExpired(ctx)
	require.NoError(t, err)
	assert.Equal(t, rows, n, "the batching loop must keep going until every expired row is gone")

	var left int
	require.NoError(t, f.ownerDB.QueryRowContext(ctx,
		"SELECT count(*) FROM "+f.prefix+"sessions").Scan(&left))
	assert.Equal(t, 1, left, "the unexpired session must survive the sweep")
}

func TestPgStore_LoadOfAnUnopenablePayloadLogsOutAndReports(t *testing.T) {
	f := newPgSessionFixture(t)
	ctx := t.Context()
	uid := f.seedUser(t)

	reported := 0
	st := f.newStore(func(context.Context, string, error, ...any) *apperror.AppError {
		reported++
		return nil
	})

	_, err := f.ownerDB.ExecContext(ctx,
		"INSERT INTO "+f.prefix+"sessions (sid, user_id, payload, expires_at, created_at) VALUES ($1,$2,$3,$4,$5)",
		"rotten-sid", uid, []byte("not ciphertext"), time.Now().Add(time.Hour).UTC(), time.Now().UTC())
	require.NoError(t, err)

	p, ok, err := st.Load(ctx, "rotten-sid")
	require.NoError(t, err, "an unopenable session is not a request failure")
	require.False(t, ok)
	assert.Zero(t, p.UserID)
	assert.Equal(t, 1, reported, "a systemic decode failure would log out every user; it must reach the reporter")
}
