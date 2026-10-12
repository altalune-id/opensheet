//go:build integration

package tenant_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/pgtest"
	"altalune.id/opensheet/nanoid"
	"altalune.id/opensheet/schema"
)

// NOTE: pgtest reuses TEST_PG_DSN when set, so role and table names must be unique per run.
func uniqueSuffix(t *testing.T) string {
	t.Helper()
	s, err := nanoid.New(10)
	require.NoError(t, err)
	return strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(s))
}

func createRole(t *testing.T, admin *sql.DB, name, attrs string) {
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

type orgDefinerFixture struct {
	migDB   *sql.DB
	appConn *sql.DB
	prefix  string
	userID  uuid.UUID
	orgID   uuid.UUID
	suffix  string
	appRole string
}

func newOrgDefinerFixture(t *testing.T) *orgDefinerFixture {
	t.Helper()
	h := pgtest.New(t)
	suffix := uniqueSuffix(t)
	ownerRole := "opensheet_tenowner_" + suffix
	appRole := "opensheet_tenapp_" + suffix
	prefix := "t" + suffix + "_"

	admin, err := db.Open(t.Context(), db.DBConfig{Driver: db.DriverPostgres, DSN: h.DSN}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })

	createRole(t, admin, ownerRole, "NOLOGIN BYPASSRLS")
	// NOTE: USAGE as well as CREATE — without USAGE the schema drops out of search_path and DDL fails with 3F000.
	_, err = admin.ExecContext(t.Context(), fmt.Sprintf(`GRANT USAGE, CREATE ON SCHEMA public TO %q`, ownerRole))
	require.NoError(t, err)

	migDB, err := db.Open(t.Context(), db.DBConfig{
		Driver: db.DriverPostgres, DSN: h.DSN, Role: ownerRole, MaxOpenConns: 1,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = migDB.Close() })

	cfg := config.Defaults()
	cfg.DB.Driver = "postgres"
	cfg.DB.Schema = "public"
	cfg.DB.TablePrefix = prefix
	cfg.Tenant.RLSEnforce = true
	require.NoError(t, schema.MigrateUp(t.Context(), migDB, cfg))

	f := &orgDefinerFixture{
		migDB: migDB, prefix: prefix, userID: uuid.New(), orgID: uuid.New(), suffix: suffix, appRole: appRole,
	}
	now := time.Now().UTC()
	_, err = migDB.ExecContext(t.Context(),
		"INSERT INTO "+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ($1,$2,'','',false,$3,$3)",
		f.userID, f.userID.String()+"@x.co", now)
	require.NoError(t, err)
	_, err = migDB.ExecContext(t.Context(),
		"INSERT INTO "+prefix+"orgs (id, slug, name, created_by, created_at, updated_at) VALUES ($1,$2,'Acme',$3,$4,$4)",
		f.orgID, "acme-"+suffix, f.userID, now)
	require.NoError(t, err)

	createRole(t, admin, appRole, "LOGIN PASSWORD 'pw' NOBYPASSRLS")
	_, err = admin.ExecContext(t.Context(), fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO %q`, appRole))
	require.NoError(t, err)
	_, err = migDB.ExecContext(t.Context(), fmt.Sprintf(`GRANT SELECT ON public.%s TO %q`, prefix+"orgs", appRole))
	require.NoError(t, err)
	_, err = migDB.ExecContext(t.Context(),
		fmt.Sprintf(`GRANT EXECUTE ON FUNCTION public.%s() TO %q`, prefix+"list_org_ids", appRole))
	require.NoError(t, err)

	f.appConn, err = db.Open(t.Context(), db.DBConfig{
		Driver: db.DriverPostgres, DSN: pgtest.DSNWithUser(t, h.DSN, appRole, "pw"), MaxOpenConns: 1,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.appConn.Close() })
	return f
}

// TestEnumerator_Each_ReadsEveryOrgThroughTheDefinerWrapper asserts a NOBYPASSRLS app role sees zero orgs in the table yet every org through the wrapper.
func TestEnumerator_Each_ReadsEveryOrgThroughTheDefinerWrapper(t *testing.T) {
	f := newOrgDefinerFixture(t)

	var direct int
	require.NoError(t, f.appConn.QueryRowContext(t.Context(),
		"SELECT count(*) FROM public."+f.prefix+"orgs").Scan(&direct))
	require.Zero(t, direct, "app role must read no orgs from the table under FORCE row level security")

	tn := tenant.NewEnumerator(
		tenant.NewOrgReader(db.Pool{W: f.appConn, R: f.appConn}, db.DriverPostgres, "public", f.prefix),
		discardLogger())
	var seen []string
	require.NoError(t, tn.Each(t.Context(), func(_ context.Context, tenantID string) error {
		seen = append(seen, tenantID)
		return nil
	}))
	require.Equal(t, []string{f.orgID.String()}, seen,
		"the wrapper must lift RLS for a NOBYPASSRLS caller")
}

func sortedOrgIDs(t *testing.T, n int) []uuid.UUID {
	t.Helper()
	ids := make([]uuid.UUID, 0, n)
	for range n {
		ids = append(ids, uuid.New())
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	return ids
}

func (f *orgDefinerFixture) seedTiedOrgs(t *testing.T, ids []uuid.UUID) {
	t.Helper()
	tied := time.Now().UTC().Truncate(time.Microsecond)
	for i := len(ids) - 1; i >= 0; i-- {
		_, err := f.migDB.ExecContext(t.Context(),
			"INSERT INTO public."+f.prefix+"orgs (id, slug, name, system, created_by, created_at, updated_at) VALUES ($1,$2,'Tied',false,$3,$4,$4)",
			ids[i], "tied-"+f.suffix+"-"+ids[i].String(), f.userID, tied)
		require.NoError(t, err)
	}

	var seeded, distinct int
	require.NoError(t, f.migDB.QueryRowContext(t.Context(),
		"SELECT count(*), count(DISTINCT created_at) FROM public."+f.prefix+"orgs WHERE name = 'Tied'").
		Scan(&seeded, &distinct))
	require.Equal(t, len(ids), seeded)
	require.Equal(t, 1, distinct, "test premise: every seeded org must share one created_at")
}

func (f *orgDefinerFixture) cloneWrapperUnordered(t *testing.T, prefix string) {
	t.Helper()
	_, err := f.migDB.ExecContext(t.Context(),
		"CREATE FUNCTION public."+prefix+"list_org_ids() RETURNS TABLE (id uuid, created_at timestamptz)"+
			" LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_catalog, pg_temp"+
			" AS $$ SELECT o.id, o.created_at FROM public."+f.prefix+"orgs o $$")
	require.NoError(t, err)
	_, err = f.migDB.ExecContext(t.Context(),
		fmt.Sprintf(`GRANT EXECUTE ON FUNCTION public.%slist_org_ids() TO %q`, prefix, f.appRole))
	require.NoError(t, err)
}

func (f *orgDefinerFixture) readTied(t *testing.T, prefix string, tied []uuid.UUID) []uuid.UUID {
	t.Helper()
	all, err := tenant.NewOrgReader(db.Pool{W: f.appConn, R: f.appConn}, db.DriverPostgres, "public", prefix).
		OrgIDs(t.Context())
	require.NoError(t, err)
	require.Len(t, all, len(tied)+1, "the reader lost or duplicated an org")

	want := make(map[uuid.UUID]bool, len(tied))
	for _, id := range tied {
		want[id] = true
	}
	got := make([]uuid.UUID, 0, len(tied))
	for _, id := range all {
		if want[id] {
			got = append(got, id)
		}
	}
	return got
}

// TestOrgReader_TiedCreatedAtIsOrderedByID proves created_at alone is not a total order over orgs.
func TestOrgReader_TiedCreatedAtIsOrderedByID(t *testing.T) {
	f := newOrgDefinerFixture(t)
	ids := sortedOrgIDs(t, 12)
	f.seedTiedOrgs(t, ids)

	for read := range 5 {
		require.Equal(t, ids, f.readTied(t, f.prefix, ids),
			"read %d returned a different order for rows tied on created_at", read)
	}

	// NOTE: SECURITY DEFINER blocks SRF inlining, so the wrapper's own ORDER BY masks the consumer's — an unordered clone is what proves the reader's ORDER BY stands on its own.
	unordered := "u" + f.suffix + "_"
	f.cloneWrapperUnordered(t, unordered)
	require.Equal(t, ids, f.readTied(t, unordered, ids),
		"the reader must impose the total order itself, not inherit it from the wrapper body")
}
