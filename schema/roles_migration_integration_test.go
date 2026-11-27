//go:build integration

package schema_test

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/testutil/pgtest"
	"altalune.id/opensheet/schema"
)

const (
	rolesUser  = "00000000-0000-0000-0000-000000000001"
	rolesUser2 = "00000000-0000-0000-0000-000000000002"
	rolesOrg   = "00000000-0000-0000-0000-0000000000a1"
	rolesOrg2  = "00000000-0000-0000-0000-0000000000a2"
	rolesRole  = "00000000-0000-0000-0000-0000000000d1"
)

func rolesPostgresDB(t *testing.T, name string) (*sql.DB, string, func(q string, args ...any) error) {
	t.Helper()
	h := pgtest.New(t)
	suffix := uniqueSuffix(t)
	prefix := "t" + suffix + "_"
	migDB := migrationRoleDB(t, h, "opensheet_"+name+"_"+suffix, "BYPASSRLS")
	exec := func(q string, args ...any) error {
		_, err := migDB.ExecContext(t.Context(), q, args...)
		return err
	}
	return migDB, prefix, exec
}

func TestMigration_RolesKeepExistingMembersPostgres(t *testing.T) {
	migDB, prefix, exec := rolesPostgresDB(t, "roleskeep")
	cfg := migrationConfig(prefix)
	require.NoError(t, schema.MigrateUp(t.Context(), migDB, cfg))
	require.NoError(t, schema.MigrateDownTo(t.Context(), migDB, cfg, 20))

	p := "public." + prefix
	t0 := "2026-01-01T00:00:00Z"
	require.NoError(t, exec(`INSERT INTO `+p+`users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ($1, 'u1@x.co', '', '', false, $2, $2), ($3, 'u2@x.co', '', '', false, $2, $2)`, rolesUser, t0, rolesUser2))
	require.NoError(t, exec(`INSERT INTO `+p+`orgs (id, slug, name, created_by, created_at, updated_at) VALUES ($1, 'o', 'O', $2, $3, $3)`, rolesOrg, rolesUser, t0))
	require.NoError(t, exec(`INSERT INTO `+p+`memberships (id, org_id, user_id, role, created_at) VALUES (gen_random_uuid(), $1, $2, 'owner', $4), (gen_random_uuid(), $1, $3, 'member', $4)`, rolesOrg, rolesUser, rolesUser2, t0))
	require.NoError(t, exec(`INSERT INTO `+p+`invites (id, org_id, email, role, token_hash, expires_at, invited_by, created_at) VALUES (gen_random_uuid(), $1, 'n@x.co', 'admin', 'h1', $3, $2, $3)`, rolesOrg, rolesUser, t0))

	require.NoError(t, schema.MigrateUp(t.Context(), migDB, cfg))

	rows, err := migDB.QueryContext(t.Context(), `SELECT role, role_id IS NULL FROM `+p+`memberships ORDER BY role`)
	require.NoError(t, err)
	var got []string
	for rows.Next() {
		var role string
		var noRoleID bool
		require.NoError(t, rows.Scan(&role, &noRoleID))
		require.True(t, noRoleID, "an existing %s membership must get no role_id", role)
		got = append(got, role)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Equal(t, []string{"member", "owner"}, got)

	var inviteRole string
	require.NoError(t, migDB.QueryRowContext(t.Context(),
		`SELECT role FROM `+p+`resolve_invite_by_token_hash('h1') WHERE role_id IS NULL`).Scan(&inviteRole))
	require.Equal(t, "admin", inviteRole, "the resolver must return the pending invite with its role_id")
}

func TestMigration_CustomRoleNeedsRoleIDPostgres(t *testing.T) {
	migDB, prefix, exec := rolesPostgresDB(t, "rolescheck")
	require.NoError(t, schema.MigrateUp(t.Context(), migDB, migrationConfig(prefix)))

	p := "public." + prefix
	t0 := "2026-01-01T00:00:00Z"
	require.NoError(t, exec(`INSERT INTO `+p+`users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ($1, 'u1@x.co', '', '', false, $2, $2), ($3, 'u2@x.co', '', '', false, $2, $2)`, rolesUser, t0, rolesUser2))
	require.NoError(t, exec(`INSERT INTO `+p+`orgs (id, slug, name, created_by, created_at, updated_at) VALUES ($1, 'o', 'O', $3, $4, $4), ($2, 'o2', 'O', $3, $4, $4)`, rolesOrg, rolesOrg2, rolesUser, t0))
	require.NoError(t, exec(`INSERT INTO `+p+`roles (id, org_id, name, permissions, created_at, updated_at) VALUES ($1, $2, 'Clerk', '{}', $3, $3)`, rolesRole, rolesOrg, t0))

	member := `INSERT INTO ` + p + `memberships (id, org_id, user_id, role, role_id, created_at) VALUES (gen_random_uuid(), $1, $2, $3, $4, $5)`
	require.Error(t, exec(member, rolesOrg, rolesUser2, "custom", nil, t0), "custom without role_id")
	require.Error(t, exec(member, rolesOrg, rolesUser2, "member", rolesRole, t0), "built-in with role_id")
	require.Error(t, exec(member, rolesOrg2, rolesUser2, "custom", rolesRole, t0), "role_id of another org")
	require.NoError(t, exec(member, rolesOrg, rolesUser2, "custom", rolesRole, t0))

	invite := `INSERT INTO ` + p + `invites (id, org_id, email, role, role_id, token_hash, expires_at, invited_by, created_at) VALUES (gen_random_uuid(), $1, 'n@x.co', $2, $3, 'h1', $5, $4, $5)`
	require.Error(t, exec(invite, rolesOrg, "custom", nil, rolesUser, t0), "custom invite without role_id")
	require.Error(t, exec(invite, rolesOrg, "admin", rolesRole, rolesUser, t0), "built-in invite with role_id")
	require.Error(t, exec(invite, rolesOrg2, "custom", rolesRole, rolesUser, t0), "invite role_id of another org")
	require.NoError(t, exec(invite, rolesOrg, "custom", rolesRole, rolesUser, t0))
}

func TestMigration_RolesDownRestoresOpensheetFunctions(t *testing.T) {
	migDB, prefix, exec := rolesPostgresDB(t, "rolesdown")
	cfg := migrationConfig(prefix)
	require.NoError(t, schema.MigrateUp(t.Context(), migDB, cfg))

	p := "public." + prefix
	t0 := "2026-01-01T00:00:00Z"
	require.NoError(t, exec(`INSERT INTO `+p+`users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ($1, 'u1@x.co', '', '', false, $2, $2), ($3, 'u2@x.co', '', '', false, $2, $2)`, rolesUser, t0, rolesUser2))
	require.NoError(t, exec(`INSERT INTO `+p+`orgs (id, slug, name, created_by, created_at, updated_at) VALUES ($1, 'o', 'O', $2, $3, $3)`, rolesOrg, rolesUser, t0))
	require.NoError(t, exec(`INSERT INTO `+p+`roles (id, org_id, name, permissions, created_at, updated_at) VALUES ($1, $2, 'Clerk', '{}', $3, $3)`, rolesRole, rolesOrg, t0))
	require.NoError(t, exec(`INSERT INTO `+p+`memberships (id, org_id, user_id, role, role_id, created_at) VALUES (gen_random_uuid(), $1, $2, 'owner', NULL, $4), (gen_random_uuid(), $1, $3, 'custom', $5, $4)`, rolesOrg, rolesUser, rolesUser2, t0, rolesRole))
	require.NoError(t, exec(`INSERT INTO `+p+`invites (id, org_id, email, role, token_hash, expires_at, invited_by, created_at) VALUES (gen_random_uuid(), $1, 'n@x.co', 'member', 'h1', $3, $2, $3)`, rolesOrg, rolesUser, t0))

	require.NoError(t, schema.MigrateDownTo(t.Context(), migDB, cfg, 20))

	result := func(fn string) string {
		t.Helper()
		var out string
		require.NoError(t, migDB.QueryRowContext(t.Context(),
			`SELECT pg_get_function_result(p.oid) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
			  WHERE n.nspname = 'public' AND p.proname = $1`, prefix+fn).Scan(&out))
		return out
	}
	const cols = "TABLE(id uuid, org_id uuid, email text, role text, token_hash text, expires_at timestamp with time zone, accepted_at timestamp with time zone, created_at timestamp with time zone)"
	require.Equal(t, cols, result("resolve_invite_by_token_hash"))
	require.Equal(t, cols, result("list_pending_invites_for_email"))

	var def string
	require.NoError(t, migDB.QueryRowContext(t.Context(),
		`SELECT pg_get_functiondef(($1 || '(text)')::regprocedure)`, p+"list_pending_invites_for_email").Scan(&def))
	require.Contains(t, def, "ORDER BY i.created_at ASC, i.id ASC", "the down must restore the opensheet body, not the template's")

	var n int
	require.NoError(t, migDB.QueryRowContext(t.Context(), `SELECT count(*) FROM `+p+`resolve_invite_by_token_hash('h1')`).Scan(&n))
	require.Equal(t, 1, n, "the restored resolver must still find the invite")
	require.NoError(t, migDB.QueryRowContext(t.Context(), `SELECT count(*) FROM `+p+`memberships`).Scan(&n))
	require.Equal(t, 1, n, "the down drops custom-role members and keeps built-in ones")

	require.NoError(t, schema.MigrateUp(t.Context(), migDB, cfg))
}
