//go:build integration

package schema_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/testutil/pgtest"
	"altalune.id/opensheet/schema"
)

func TestMigrate_Postgres_OrgAPIKeysRollsBackAndForward(t *testing.T) {
	h := pgtest.New(t)
	suffix := uniqueSuffix(t)
	prefix := "t" + suffix + "_"
	migDB := migrationRoleDB(t, h, "opensheet_orgkeys_"+suffix, "BYPASSRLS")
	cfg := migrationConfig(prefix)
	require.NoError(t, schema.MigrateUp(t.Context(), migDB, cfg))

	exec := func(q string) {
		t.Helper()
		_, err := migDB.ExecContext(t.Context(), q)
		require.NoError(t, err, q)
	}
	t0 := "'2026-01-01T00:00:00Z'"
	exec(`INSERT INTO public.` + prefix + `users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ('00000000-0000-0000-0000-000000000001', 'u@x.co', '', '', false, ` + t0 + `, ` + t0 + `)`)
	exec(`INSERT INTO public.` + prefix + `orgs (id, slug, name, created_by, created_at, updated_at) VALUES ('00000000-0000-0000-0000-0000000000a1', 'o', 'O', '00000000-0000-0000-0000-000000000001', ` + t0 + `, ` + t0 + `)`)
	exec(`INSERT INTO public.` + prefix + `projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES ('00000000-0000-0000-0000-0000000000b1', '00000000-0000-0000-0000-0000000000a1', 'p', 'P', '00000000-0000-0000-0000-000000000001', ` + t0 + `, ` + t0 + `)`)
	exec(`INSERT INTO public.` + prefix + `api_keys (id, org_id, project_id, name, secret_hash, created_at) VALUES ('00000000-0000-0000-0000-0000000000c1', '00000000-0000-0000-0000-0000000000a1', '00000000-0000-0000-0000-0000000000b1', 'project', '\x01', ` + t0 + `)`)
	exec(`INSERT INTO public.` + prefix + `api_keys (id, org_id, project_id, kind, name, secret_hash, created_at) VALUES ('00000000-0000-0000-0000-0000000000c2', '00000000-0000-0000-0000-0000000000a1', NULL, 'org', 'org', '\x02', ` + t0 + `)`)
	exec(`INSERT INTO public.` + prefix + `api_key_projects (org_id, key_id, project_id, created_at) VALUES ('00000000-0000-0000-0000-0000000000a1', '00000000-0000-0000-0000-0000000000c2', '00000000-0000-0000-0000-0000000000b1', ` + t0 + `)`)

	var grant string
	require.NoError(t, migDB.QueryRowContext(t.Context(),
		`SELECT project_ids FROM public.`+prefix+`resolve_api_key_by_secret_hash('\x02')`).Scan(&grant))
	require.Equal(t, "{00000000-0000-0000-0000-0000000000b1}", grant, "the resolver must return the org key's grant")

	require.NoError(t, schema.MigrateDownTo(t.Context(), migDB, cfg, 17))
	var n int
	require.NoError(t, migDB.QueryRowContext(t.Context(), `SELECT count(*) FROM public.`+prefix+`api_keys`).Scan(&n))
	require.Equal(t, 1, n, "rollback keeps project keys and drops org keys")
	require.NoError(t, migDB.QueryRowContext(t.Context(),
		`SELECT count(*) FROM public.`+prefix+`resolve_api_key_by_secret_hash('\x01')`).Scan(&n))
	require.Equal(t, 1, n, "the restored resolver must still find a project key")

	require.NoError(t, schema.MigrateUp(t.Context(), migDB, cfg))
}
