//go:build integration

package schema_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/schema"
)

const grantsProject = "00000000-0000-0000-0000-0000000000c1"

func TestMigration_ExistingMembersStayUnrestrictedPostgres(t *testing.T) {
	migDB, prefix, exec := rolesPostgresDB(t, "grantskeep")
	cfg := migrationConfig(prefix)
	require.NoError(t, schema.MigrateUp(t.Context(), migDB, cfg))
	require.NoError(t, schema.MigrateDownTo(t.Context(), migDB, cfg, 21))

	p := "public." + prefix
	t0 := "2026-01-01T00:00:00Z"
	require.NoError(t, exec(`INSERT INTO `+p+`users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ($1, 'u1@x.co', '', '', false, $2, $2), ($3, 'u2@x.co', '', '', false, $2, $2)`, rolesUser, t0, rolesUser2))
	require.NoError(t, exec(`INSERT INTO `+p+`orgs (id, slug, name, created_by, created_at, updated_at) VALUES ($1, 'o', 'O', $2, $3, $3)`, rolesOrg, rolesUser, t0))
	require.NoError(t, exec(`INSERT INTO `+p+`memberships (id, org_id, user_id, role, created_at) VALUES (gen_random_uuid(), $1, $2, 'owner', $4), (gen_random_uuid(), $1, $3, 'member', $4)`, rolesOrg, rolesUser, rolesUser2, t0))
	require.NoError(t, exec(`INSERT INTO `+p+`invites (id, org_id, email, role, token_hash, expires_at, invited_by, created_at) VALUES (gen_random_uuid(), $1, 'n@x.co', 'admin', 'h1', $3, $2, $3)`, rolesOrg, rolesUser, t0))

	require.NoError(t, schema.MigrateUp(t.Context(), migDB, cfg))

	var restricted int
	require.NoError(t, migDB.QueryRowContext(t.Context(), `SELECT count(*) FROM `+p+`memberships WHERE NOT all_projects`).Scan(&restricted))
	require.Zero(t, restricted, "every existing membership keeps every project")

	var all bool
	var ids string
	require.NoError(t, migDB.QueryRowContext(t.Context(),
		`SELECT all_projects, project_ids FROM `+p+`resolve_invite_by_token_hash('h1')`).Scan(&all, &ids))
	require.True(t, all, "the existing invite keeps every project")
	require.Equal(t, "{}", ids)
}

func TestMigration_OwnerMustBeAllProjectsPostgres(t *testing.T) {
	migDB, prefix, exec := rolesPostgresDB(t, "grantsowner")
	require.NoError(t, schema.MigrateUp(t.Context(), migDB, migrationConfig(prefix)))

	p := "public." + prefix
	t0 := "2026-01-01T00:00:00Z"
	require.NoError(t, exec(`INSERT INTO `+p+`users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ($1, 'u1@x.co', '', '', false, $2, $2), ($3, 'u2@x.co', '', '', false, $2, $2)`, rolesUser, t0, rolesUser2))
	require.NoError(t, exec(`INSERT INTO `+p+`orgs (id, slug, name, created_by, created_at, updated_at) VALUES ($1, 'o', 'O', $2, $3, $3)`, rolesOrg, rolesUser, t0))

	member := `INSERT INTO ` + p + `memberships (id, org_id, user_id, role, all_projects, created_at) VALUES (gen_random_uuid(), $1, $2, $3, $4, $5)`
	require.Error(t, exec(member, rolesOrg, rolesUser, "owner", false, t0), "a restricted owner")
	require.NoError(t, exec(member, rolesOrg, rolesUser, "owner", true, t0))
	require.NoError(t, exec(member, rolesOrg, rolesUser2, "admin", false, t0))
	require.Error(t, exec(`UPDATE `+p+`memberships SET role = 'owner' WHERE user_id = $1`, rolesUser2), "promoting a restricted member to owner")

	invite := `INSERT INTO ` + p + `invites (id, org_id, email, role, all_projects, token_hash, expires_at, invited_by, created_at) VALUES (gen_random_uuid(), $1, 'n@x.co', $2, $3, $4, $6, $5, $6)`
	require.Error(t, exec(invite, rolesOrg, "owner", false, "h1", rolesUser, t0), "a restricted owner invite")
	require.NoError(t, exec(invite, rolesOrg, "owner", true, "h1", rolesUser, t0))
	require.NoError(t, exec(invite, rolesOrg, "member", false, "h2", rolesUser, t0))
}

func TestMigration_GrantRowsCannotCrossOrgsPostgres(t *testing.T) {
	migDB, prefix, exec := rolesPostgresDB(t, "grantscross")
	require.NoError(t, schema.MigrateUp(t.Context(), migDB, migrationConfig(prefix)))

	p := "public." + prefix
	t0 := "2026-01-01T00:00:00Z"
	const project2 = "00000000-0000-0000-0000-0000000000c2"
	require.NoError(t, exec(`INSERT INTO `+p+`users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ($1, 'u1@x.co', '', '', false, $2, $2)`, rolesUser, t0))
	require.NoError(t, exec(`INSERT INTO `+p+`orgs (id, slug, name, created_by, created_at, updated_at) VALUES ($1, 'o', 'O', $3, $4, $4), ($2, 'o2', 'O', $3, $4, $4)`, rolesOrg, rolesOrg2, rolesUser, t0))
	require.NoError(t, exec(`INSERT INTO `+p+`projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES ($1, $3, 'p', 'P', $5, $6, $6), ($2, $4, 'p', 'P', $5, $6, $6)`, grantsProject, project2, rolesOrg, rolesOrg2, rolesUser, t0))

	var membershipID, inviteID string
	require.NoError(t, migDB.QueryRowContext(t.Context(), `INSERT INTO `+p+`memberships (id, org_id, user_id, role, all_projects, created_at) VALUES (gen_random_uuid(), $1, $2, 'member', false, $3) RETURNING id`, rolesOrg, rolesUser, t0).Scan(&membershipID))
	require.NoError(t, migDB.QueryRowContext(t.Context(), `INSERT INTO `+p+`invites (id, org_id, email, role, all_projects, token_hash, expires_at, invited_by, created_at) VALUES (gen_random_uuid(), $1, 'n@x.co', 'member', false, 'h1', $3, $2, $3) RETURNING id`, rolesOrg, rolesUser, t0).Scan(&inviteID))

	grant := `INSERT INTO ` + p + `membership_projects (org_id, membership_id, project_id, created_at) VALUES ($1, $2, $3, $4)`
	require.Error(t, exec(grant, rolesOrg, membershipID, project2, t0), "a project of another org")
	require.Error(t, exec(grant, rolesOrg2, membershipID, project2, t0), "a membership of another org")
	require.NoError(t, exec(grant, rolesOrg, membershipID, grantsProject, t0))

	inviteGrant := `INSERT INTO ` + p + `invite_projects (org_id, invite_id, project_id, created_at) VALUES ($1, $2, $3, $4)`
	require.Error(t, exec(inviteGrant, rolesOrg, inviteID, project2, t0), "an invite grant on a project of another org")
	require.Error(t, exec(inviteGrant, rolesOrg2, inviteID, project2, t0), "an invite of another org")
	require.NoError(t, exec(inviteGrant, rolesOrg, inviteID, grantsProject, t0))

	var ids string
	require.NoError(t, migDB.QueryRowContext(t.Context(),
		`SELECT project_ids FROM `+p+`list_pending_invites_for_email('n@x.co')`).Scan(&ids))
	require.Equal(t, "{"+grantsProject+"}", ids, "the pending-invite resolver carries the grant")
}

func TestMigration_GrantsDownRestoresRolesFunctions(t *testing.T) {
	migDB, prefix, exec := rolesPostgresDB(t, "grantsdown")
	cfg := migrationConfig(prefix)
	require.NoError(t, schema.MigrateUp(t.Context(), migDB, cfg))

	p := "public." + prefix
	t0 := "2026-01-01T00:00:00Z"
	require.NoError(t, exec(`INSERT INTO `+p+`users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ($1, 'u1@x.co', '', '', false, $2, $2), ($3, 'u2@x.co', '', '', false, $2, $2)`, rolesUser, t0, rolesUser2))
	require.NoError(t, exec(`INSERT INTO `+p+`orgs (id, slug, name, created_by, created_at, updated_at) VALUES ($1, 'o', 'O', $2, $3, $3)`, rolesOrg, rolesUser, t0))
	require.NoError(t, exec(`INSERT INTO `+p+`projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES ($1, $2, 'p', 'P', $3, $4, $4)`, grantsProject, rolesOrg, rolesUser, t0))
	require.NoError(t, exec(`INSERT INTO `+p+`memberships (id, org_id, user_id, role, all_projects, created_at) VALUES (gen_random_uuid(), $1, $2, 'owner', true, $4), (gen_random_uuid(), $1, $3, 'member', false, $4)`, rolesOrg, rolesUser, rolesUser2, t0))
	require.NoError(t, exec(`INSERT INTO `+p+`invites (id, org_id, email, role, all_projects, token_hash, expires_at, invited_by, created_at) VALUES (gen_random_uuid(), $1, 'n@x.co', 'member', false, 'h1', $3, $2, $3)`, rolesOrg, rolesUser, t0))

	require.NoError(t, schema.MigrateDownTo(t.Context(), migDB, cfg, 21))

	result := func(fn string) string {
		t.Helper()
		var out string
		require.NoError(t, migDB.QueryRowContext(t.Context(),
			`SELECT pg_get_function_result(p.oid) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
			  WHERE n.nspname = 'public' AND p.proname = $1`, prefix+fn).Scan(&out))
		return out
	}
	const cols = "TABLE(id uuid, org_id uuid, email text, role text, role_id uuid, token_hash text, expires_at timestamp with time zone, accepted_at timestamp with time zone, created_at timestamp with time zone)"
	require.Equal(t, cols, result("resolve_invite_by_token_hash"))
	require.Equal(t, cols, result("list_pending_invites_for_email"))

	var def string
	require.NoError(t, migDB.QueryRowContext(t.Context(),
		`SELECT pg_get_functiondef(($1 || '(text)')::regprocedure)`, p+"list_pending_invites_for_email").Scan(&def))
	require.Contains(t, def, "ORDER BY i.created_at ASC, i.id ASC")

	var n int
	require.NoError(t, migDB.QueryRowContext(t.Context(), `SELECT count(*) FROM `+p+`resolve_invite_by_token_hash('h1')`).Scan(&n))
	require.Equal(t, 1, n, "the restored resolver still finds the formerly restricted invite")
	require.NoError(t, migDB.QueryRowContext(t.Context(), `SELECT count(*) FROM `+p+`memberships`).Scan(&n))
	require.Equal(t, 2, n, "the down keeps restricted members, now unrestricted")

	require.NoError(t, schema.MigrateUp(t.Context(), migDB, cfg))
}
