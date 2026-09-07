//go:build integration

package schema_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/testutil/pgtest"
	"altalune.id/template/schema"
)

func TestMigrateUp_DefinerFunctionsAreOwnedByTheMigrationRole(t *testing.T) {
	h := pgtest.New(t)
	suffix := uniqueSuffix(t)
	role := "altempl_definer_" + suffix
	prefix := "t" + suffix + "_"

	migDB := migrationRoleDB(t, h, role, "BYPASSRLS")
	cfg := migrationConfig(prefix)
	require.True(t, cfg.Tenant.RLSEnforce, "the wrappers only matter under FORCE row level security")
	require.NoError(t, schema.MigrateUp(t.Context(), migDB, cfg))

	for _, sig := range []string{
		"list_org_ids()", "resolve_org_by_slug(text)", "list_orgs_for_user(uuid)",
		"resolve_invite_by_token_hash(text)", "list_pending_invites_for_email(text)",
	} {
		name := prefix + strings.SplitN(sig, "(", 2)[0]

		var owner string
		var secdef bool
		require.NoError(t, migDB.QueryRowContext(t.Context(),
			`SELECT pg_get_userbyid(p.proowner), p.prosecdef
			   FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
			  WHERE n.nspname = 'public' AND p.proname = $1`,
			name).Scan(&owner, &secdef))
		require.Equal(t, role, owner, "%s must be owned by the migration role or it bypasses nothing", sig)
		require.True(t, secdef, "%s must be SECURITY DEFINER", sig)

		var publicExec bool
		require.NoError(t, migDB.QueryRowContext(t.Context(),
			`SELECT has_function_privilege('public', $1, 'EXECUTE')`,
			"public."+prefix+sig).Scan(&publicExec))
		require.False(t, publicExec, "PUBLIC must not hold EXECUTE on cross-tenant reader %s", sig)
	}

	require.NoError(t, schema.MigrateDownTo(t.Context(), migDB, cfg, 4))
	require.Zero(t, wrapperCount(t, migDB, prefix), "rollback must drop every wrapper")
}

func wrapperCount(t *testing.T, conn *sql.DB, prefix string) int {
	t.Helper()
	var n int
	require.NoError(t, conn.QueryRowContext(t.Context(),
		`SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		  WHERE n.nspname = 'public' AND p.proname LIKE $1 AND p.prosecdef`,
		prefix+"%").Scan(&n))
	return n
}

func TestMigrateUp_RaisesWhenMigrationRoleLacksBypassRLS(t *testing.T) {
	h := pgtest.New(t)
	suffix := uniqueSuffix(t)
	role := "altempl_norls_" + suffix
	prefix := "t" + suffix + "_"

	migDB := migrationRoleDB(t, h, role, "NOBYPASSRLS")

	err := schema.MigrateUp(t.Context(), migDB, migrationConfig(prefix))
	require.Error(t, err, "a migration role without BYPASSRLS must abort, not ship silent wrappers")
	require.Contains(t, err.Error(), "lacks BYPASSRLS")

	var tables int
	require.NoError(t, migDB.QueryRowContext(t.Context(),
		`SELECT count(*) FROM pg_tables WHERE schemaname = 'public' AND tablename = $1`,
		prefix+"orgs").Scan(&tables))
	require.Equal(t, 1, tables, "migrations before 005 must have applied, or the abort proves nothing")

	require.Zero(t, wrapperCount(t, migDB, prefix), "the aborted migration must leave no wrapper behind")
}
