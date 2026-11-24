//go:build integration

package schema_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/testutil/pgtest"
	"altalune.id/opensheet/schema"
)

func TestMigration019_Postgres_PartialUniqueIndex(t *testing.T) {
	h := pgtest.New(t)
	suffix := uniqueSuffix(t)
	prefix := "t" + suffix + "_"
	migDB := migrationRoleDB(t, h, "opensheet_provkey_"+suffix, "BYPASSRLS")
	cfg := migrationConfig(prefix)
	require.NoError(t, schema.MigrateUp(t.Context(), migDB, cfg))

	exec := func(q string) error {
		_, err := migDB.ExecContext(t.Context(), q)
		return err
	}
	const (
		t0   = "'2026-01-01T00:00:00Z'"
		user = "'00000000-0000-0000-0000-000000000001'"
		org  = "'00000000-0000-0000-0000-0000000000a1'"
		p1   = "'00000000-0000-0000-0000-0000000000b1'"
		p2   = "'00000000-0000-0000-0000-0000000000b2'"
		cred = "'00000000-0000-0000-0000-0000000000c1'"
	)
	for _, q := range []string{
		`INSERT INTO public.` + prefix + `users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES (` + user + `, 'u@x.co', '', '', false, ` + t0 + `, ` + t0 + `)`,
		`INSERT INTO public.` + prefix + `orgs (id, slug, name, created_by, created_at, updated_at) VALUES (` + org + `, 'o', 'O', ` + user + `, ` + t0 + `, ` + t0 + `)`,
		`INSERT INTO public.` + prefix + `projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES (` + p1 + `, ` + org + `, 'p1', 'P', ` + user + `, ` + t0 + `, ` + t0 + `)`,
		`INSERT INTO public.` + prefix + `projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES (` + p2 + `, ` + org + `, 'p2', 'P', ` + user + `, ` + t0 + `, ` + t0 + `)`,
		`INSERT INTO public.` + prefix + `credentials (id, org_id, project_id, name, kind, authorized_by_user_id, sealed, created_at, updated_at) VALUES (` + cred + `, ` + org + `, ` + p1 + `, 'c', 'google_oauth', ` + user + `, '\x01', ` + t0 + `, ` + t0 + `)`,
	} {
		require.NoError(t, exec(q), q)
	}
	insert := func(n, project, key string) error {
		return exec(`INSERT INTO public.` + prefix + `spreadsheets (id, org_id, project_id, credential_id, google_file_id, created_at, updated_at, provision_key)
			VALUES ('00000000-0000-0000-0000-0000000000d` + n + `', ` + org + `, ` + project + `, ` + cred + `, 'f` + n + `', ` + t0 + `, ` + t0 + `, ` + key + `)`)
	}

	require.NoError(t, insert("1", p1, "NULL"))
	require.NoError(t, insert("2", p1, "NULL"), "two NULL keys in one project must both insert")
	require.NoError(t, insert("3", p1, "'k1'"))
	require.Error(t, insert("4", p1, "'k1'"), "a duplicate key in one project must be refused")
	require.NoError(t, insert("5", p2, "'k1'"), "the same key in another project must insert")

	require.NoError(t, schema.MigrateDownTo(t.Context(), migDB, cfg, 18))
	require.NoError(t, schema.MigrateUp(t.Context(), migDB, cfg))
}
