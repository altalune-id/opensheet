//go:build integration

package schema_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/db"
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

func migrationRoleDB(t *testing.T, h *pgtest.Handle, role, attrs string) *sql.DB {
	t.Helper()

	admin, err := db.Open(t.Context(), db.DBConfig{Driver: db.DriverPostgres, DSN: h.DSN}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })

	_, err = admin.ExecContext(t.Context(), fmt.Sprintf(`CREATE ROLE %q NOLOGIN %s`, role, attrs))
	require.NoError(t, err)
	// NOTE: t.Context() is already canceled by the time cleanups run, so teardown needs its own context.
	t.Cleanup(func() {
		_, err := admin.ExecContext(context.Background(), fmt.Sprintf(`DROP OWNED BY %q`, role))
		require.NoError(t, err, "leaked objects owned by %s", role)
		_, err = admin.ExecContext(context.Background(), fmt.Sprintf(`DROP ROLE IF EXISTS %q`, role))
		require.NoError(t, err, "leaked role %s", role)
	})
	_, err = admin.ExecContext(t.Context(), fmt.Sprintf(`GRANT %q TO CURRENT_USER`, role))
	require.NoError(t, err)
	// NOTE: USAGE as well as CREATE — without USAGE the schema drops out of search_path and DDL fails with 3F000.
	_, err = admin.ExecContext(t.Context(), fmt.Sprintf(`GRANT USAGE, CREATE ON SCHEMA public TO %q`, role))
	require.NoError(t, err)

	migDB, err := db.Open(t.Context(), db.DBConfig{
		Driver: db.DriverPostgres, DSN: h.DSN, Role: role, MaxOpenConns: 1,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = migDB.Close() })

	return migDB
}

func migrationConfig(prefix string) *config.Config {
	cfg := config.Defaults()
	cfg.DB.Driver = "postgres"
	cfg.DB.Schema = "public"
	cfg.DB.TablePrefix = prefix
	return cfg
}

func TestMigrateUp_BookkeepingOwnershipIsUniform(t *testing.T) {
	h := pgtest.New(t)
	suffix := uniqueSuffix(t)
	role := "altempl_owner_" + suffix
	prefix := "t" + suffix + "_"

	// NOTE: BYPASSRLS is required from migration 005 onward — see 005_definer_functions.sql.
	migDB := migrationRoleDB(t, h, role, "BYPASSRLS")
	cfg := migrationConfig(prefix)

	require.NoError(t, schema.MigrateUp(t.Context(), migDB, cfg), "first MigrateUp")
	require.NoError(t, schema.MigrateUp(t.Context(), migDB, cfg),
		"second MigrateUp must not hit permission denied on the bookkeeping table")

	var owner string
	require.NoError(t, migDB.QueryRowContext(t.Context(),
		`SELECT tableowner FROM pg_tables WHERE tablename = $1`,
		prefix+"goose_db_version").Scan(&owner))
	require.Equal(t, role, owner, "bookkeeping table must be owned by the migration role")
}
