package schema

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration_ExistingMembersStayUnrestricted(t *testing.T) {
	db, cfg := rolesSQLiteDB(t)
	ctx := context.Background()
	require.NoError(t, MigrateUp(ctx, db, cfg))
	require.NoError(t, MigrateDownTo(ctx, db, cfg, 18))

	exec := func(q string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, q, args...)
		require.NoError(t, err, q)
	}
	const ts = "2026-01-01T00:00:00Z"
	for _, u := range []string{"u1", "u2", "u3"} {
		exec(`INSERT INTO opensheet_users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES (?, ?, '', '', 0, ?, ?)`, u, u+"@x.co", ts, ts)
	}
	exec(`INSERT INTO opensheet_orgs (id, slug, name, created_by, created_at, updated_at) VALUES ('o1', 'o1', 'O', 'u1', ?, ?)`, ts, ts)
	exec(`INSERT INTO opensheet_memberships (id, org_id, user_id, role, system, created_at) VALUES ('m1', 'o1', 'u1', 'owner', 1, ?), ('m2', 'o1', 'u2', 'admin', 0, ?), ('m3', 'o1', 'u3', 'member', 0, ?)`, ts, ts, ts)
	exec(`INSERT INTO opensheet_invites (id, org_id, email, role, token_hash, expires_at, invited_by, created_at) VALUES ('i1', 'o1', 'n@x.co', 'member', 'h1', ?, 'u1', ?)`, ts, ts)

	require.NoError(t, MigrateUp(ctx, db, cfg))

	var restricted, grants int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM opensheet_memberships WHERE all_projects <> 1`).Scan(&restricted))
	require.Zero(t, restricted, "every existing membership keeps every project")
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM opensheet_invites WHERE all_projects <> 1`).Scan(&restricted))
	require.Zero(t, restricted, "every existing invite keeps every project")
	require.NoError(t, db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM opensheet_membership_projects) + (SELECT count(*) FROM opensheet_invite_projects)`).Scan(&grants))
	require.Zero(t, grants)
}

func TestMigration_OwnerMustBeAllProjects(t *testing.T) {
	db, cfg := rolesSQLiteDB(t)
	ctx := context.Background()
	require.NoError(t, MigrateUp(ctx, db, cfg))

	exec := func(q string, args ...any) error {
		_, err := db.ExecContext(ctx, q, args...)
		return err
	}
	const ts = "2026-01-01T00:00:00Z"
	require.NoError(t, exec(`INSERT INTO opensheet_users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ('u1', 'u1@x.co', '', '', 0, ?, ?), ('u2', 'u2@x.co', '', '', 0, ?, ?)`, ts, ts, ts, ts))
	require.NoError(t, exec(`INSERT INTO opensheet_orgs (id, slug, name, created_by, created_at, updated_at) VALUES ('o1', 'o1', 'O', 'u1', ?, ?)`, ts, ts))

	member := `INSERT INTO opensheet_memberships (id, org_id, user_id, role, all_projects, created_at) VALUES (?, 'o1', ?, ?, ?, ?)`
	require.Error(t, exec(member, "m1", "u1", "owner", 0, ts), "a restricted owner")
	require.NoError(t, exec(member, "m1", "u1", "owner", 1, ts))
	require.NoError(t, exec(member, "m2", "u2", "admin", 0, ts))
	require.Error(t, exec(`UPDATE opensheet_memberships SET role = 'owner' WHERE id = 'm2'`), "promoting a restricted member to owner")

	invite := `INSERT INTO opensheet_invites (id, org_id, email, role, all_projects, token_hash, expires_at, invited_by, created_at) VALUES (?, 'o1', 'n@x.co', ?, ?, ?, ?, 'u1', ?)`
	require.Error(t, exec(invite, "i1", "owner", 0, "h1", ts, ts), "a restricted owner invite")
	require.NoError(t, exec(invite, "i1", "owner", 1, "h1", ts, ts))
	require.NoError(t, exec(invite, "i2", "member", 0, "h2", ts, ts))
}

func TestMigration_GrantRowsCannotCrossOrgs(t *testing.T) {
	db, cfg := rolesSQLiteDB(t)
	ctx := context.Background()
	require.NoError(t, MigrateUp(ctx, db, cfg))

	exec := func(q string, args ...any) error {
		_, err := db.ExecContext(ctx, q, args...)
		return err
	}
	const ts = "2026-01-01T00:00:00Z"
	require.NoError(t, exec(`INSERT INTO opensheet_users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ('u1', 'u1@x.co', '', '', 0, ?, ?)`, ts, ts))
	require.NoError(t, exec(`INSERT INTO opensheet_orgs (id, slug, name, created_by, created_at, updated_at) VALUES ('o1', 'o1', 'O', 'u1', ?, ?), ('o2', 'o2', 'O', 'u1', ?, ?)`, ts, ts, ts, ts))
	require.NoError(t, exec(`INSERT INTO opensheet_projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES ('p1', 'o1', 'p', 'P', 'u1', ?, ?), ('p2', 'o2', 'p', 'P', 'u1', ?, ?)`, ts, ts, ts, ts))
	require.NoError(t, exec(`INSERT INTO opensheet_memberships (id, org_id, user_id, role, all_projects, created_at) VALUES ('m1', 'o1', 'u1', 'member', 0, ?)`, ts))
	require.NoError(t, exec(`INSERT INTO opensheet_invites (id, org_id, email, role, all_projects, token_hash, expires_at, invited_by, created_at) VALUES ('i1', 'o1', 'n@x.co', 'member', 0, 'h1', ?, 'u1', ?)`, ts, ts))

	grant := `INSERT INTO opensheet_membership_projects (org_id, membership_id, project_id, created_at) VALUES (?, 'm1', ?, ?)`
	require.Error(t, exec(grant, "o1", "p2", ts), "a project of another org")
	require.Error(t, exec(grant, "o2", "p2", ts), "a membership of another org")
	require.NoError(t, exec(grant, "o1", "p1", ts))

	inviteGrant := `INSERT INTO opensheet_invite_projects (org_id, invite_id, project_id, created_at) VALUES (?, 'i1', ?, ?)`
	require.Error(t, exec(inviteGrant, "o1", "p2", ts), "an invite grant on a project of another org")
	require.Error(t, exec(inviteGrant, "o2", "p2", ts), "an invite of another org")
	require.NoError(t, exec(inviteGrant, "o1", "p1", ts))

	require.NoError(t, exec(`DELETE FROM opensheet_projects WHERE id = 'p1'`))
	var n int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM opensheet_membership_projects) + (SELECT count(*) FROM opensheet_invite_projects)`).Scan(&n))
	require.Zero(t, n, "deleting a project drops its grant rows")
}

func TestMigration_GrantsDownMakesRestrictedUnrestricted(t *testing.T) {
	db, cfg := rolesSQLiteDB(t)
	ctx := context.Background()
	require.NoError(t, MigrateUp(ctx, db, cfg))

	exec := func(q string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, q, args...)
		require.NoError(t, err, q)
	}
	const ts = "2026-01-01T00:00:00Z"
	exec(`INSERT INTO opensheet_users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ('u1', 'u1@x.co', '', '', 0, ?, ?), ('u2', 'u2@x.co', '', '', 0, ?, ?)`, ts, ts, ts, ts)
	exec(`INSERT INTO opensheet_orgs (id, slug, name, created_by, created_at, updated_at) VALUES ('o1', 'o1', 'O', 'u1', ?, ?)`, ts, ts)
	exec(`INSERT INTO opensheet_projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES ('p1', 'o1', 'p', 'P', 'u1', ?, ?)`, ts, ts)
	exec(`INSERT INTO opensheet_memberships (id, org_id, user_id, role, all_projects, created_at) VALUES ('m1', 'o1', 'u1', 'owner', 1, ?), ('m2', 'o1', 'u2', 'member', 0, ?)`, ts, ts)
	exec(`INSERT INTO opensheet_membership_projects (org_id, membership_id, project_id, created_at) VALUES ('o1', 'm2', 'p1', ?)`, ts)
	exec(`INSERT INTO opensheet_invites (id, org_id, email, role, all_projects, token_hash, expires_at, invited_by, created_at) VALUES ('i1', 'o1', 'n@x.co', 'member', 0, 'h1', ?, 'u1', ?)`, ts, ts)
	exec(`INSERT INTO opensheet_invite_projects (org_id, invite_id, project_id, created_at) VALUES ('o1', 'i1', 'p1', ?)`, ts)

	require.NoError(t, MigrateDownTo(ctx, db, cfg, 18))

	var n int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name IN ('opensheet_membership_projects', 'opensheet_invite_projects')`).Scan(&n))
	require.Zero(t, n, "the down drops both grant tables")
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('opensheet_memberships') WHERE name = 'all_projects'`).Scan(&n))
	require.Zero(t, n, "the down drops memberships.all_projects despite its own CHECK")
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('opensheet_invites') WHERE name = 'all_projects'`).Scan(&n))
	require.Zero(t, n, "the down drops invites.all_projects despite its own CHECK")
	require.NoError(t, db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM opensheet_memberships) + (SELECT count(*) FROM opensheet_invites)`).Scan(&n))
	require.Equal(t, 3, n, "the down keeps every member and invite")

	require.NoError(t, MigrateUp(ctx, db, cfg))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM opensheet_memberships WHERE all_projects <> 1) + (SELECT count(*) FROM opensheet_invites WHERE all_projects <> 1)`).Scan(&n))
	require.Zero(t, n, "a formerly restricted member and invite reach every project again")
}
