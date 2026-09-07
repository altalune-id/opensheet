//go:build integration

package tenant_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/db"
	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/internal/testutil/pgtest"
	"altalune.id/template/nanoid"
	"altalune.id/template/schema"
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

// TestEnumerator_Each_ReadsEveryOrgThroughTheDefinerWrapper asserts a NOBYPASSRLS app role sees zero orgs in the table yet every org through the wrapper.
func TestEnumerator_Each_ReadsEveryOrgThroughTheDefinerWrapper(t *testing.T) {
	h := pgtest.New(t)
	suffix := uniqueSuffix(t)
	ownerRole := "altempl_tenowner_" + suffix
	appRole := "altempl_tenapp_" + suffix
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

	userID, orgID := uuid.New(), uuid.New()
	now := time.Now().UTC()
	_, err = migDB.ExecContext(t.Context(),
		"INSERT INTO "+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ($1,$2,'','',false,$3,$3)",
		userID, userID.String()+"@x.co", now)
	require.NoError(t, err)
	_, err = migDB.ExecContext(t.Context(),
		"INSERT INTO "+prefix+"orgs (id, slug, name, created_by, created_at, updated_at) VALUES ($1,$2,'Acme',$3,$4,$4)",
		orgID, "acme-"+suffix, userID, now)
	require.NoError(t, err)

	createRole(t, admin, appRole, "LOGIN PASSWORD 'pw' NOBYPASSRLS")
	_, err = admin.ExecContext(t.Context(), fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO %q`, appRole))
	require.NoError(t, err)
	_, err = migDB.ExecContext(t.Context(), fmt.Sprintf(`GRANT SELECT ON public.%s TO %q`, prefix+"orgs", appRole))
	require.NoError(t, err)
	_, err = migDB.ExecContext(t.Context(),
		fmt.Sprintf(`GRANT EXECUTE ON FUNCTION public.%s() TO %q`, prefix+"list_org_ids", appRole))
	require.NoError(t, err)

	appConn, err := db.Open(t.Context(), db.DBConfig{
		Driver: db.DriverPostgres, DSN: pgtest.DSNWithUser(t, h.DSN, appRole, "pw"), MaxOpenConns: 1,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = appConn.Close() })

	var direct int
	require.NoError(t, appConn.QueryRowContext(t.Context(),
		"SELECT count(*) FROM public."+prefix+"orgs").Scan(&direct))
	require.Zero(t, direct, "app role must read no orgs from the table under FORCE row level security")

	tn := tenant.NewEnumerator(
		tenant.NewOrgReader(db.Pool{W: appConn, R: appConn}, db.DriverPostgres, "public", prefix),
		discardLogger(),
	)
	var seen []string
	require.NoError(t, tn.Each(t.Context(), func(_ context.Context, tenantID string) error {
		seen = append(seen, tenantID)
		return nil
	}))
	require.Equal(t, []string{orgID.String()}, seen,
		"the wrapper must lift RLS for a NOBYPASSRLS caller")
}
