package schema

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"altalune.id/opensheet/internal/platform/config"
)

func rolesSQLiteDB(t *testing.T) (*sql.DB, *config.Config) {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "m.db") + "?_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db, config.Defaults()
}

func TestMigration_RolesKeepExistingMembers(t *testing.T) {
	db, cfg := rolesSQLiteDB(t)
	ctx := context.Background()
	require.NoError(t, MigrateUp(ctx, db, cfg))
	require.NoError(t, MigrateDownTo(ctx, db, cfg, 17))

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

	rows, err := db.QueryContext(ctx, `SELECT user_id, role, system, role_id IS NULL FROM opensheet_memberships ORDER BY user_id`)
	require.NoError(t, err)
	type member struct {
		user, role string
		system     int
		noRoleID   bool
	}
	var got []member
	for rows.Next() {
		var m member
		require.NoError(t, rows.Scan(&m.user, &m.role, &m.system, &m.noRoleID))
		got = append(got, m)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Equal(t, []member{{"u1", "owner", 1, true}, {"u2", "admin", 0, true}, {"u3", "member", 0, true}}, got,
		"every membership keeps its built-in role and gets no role_id")

	var inviteRole string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT role FROM opensheet_invites WHERE id = 'i1' AND role_id IS NULL`).Scan(&inviteRole))
	require.Equal(t, "member", inviteRole)

	_, err = db.ExecContext(ctx, `INSERT INTO opensheet_memberships (id, org_id, user_id, role, created_at) VALUES ('m4', 'o1', 'u1', 'member', ?)`, ts)
	require.Error(t, err, "UNIQUE (org_id, user_id) must survive the rebuild")
}

func TestMigration_CustomRoleNeedsRoleID(t *testing.T) {
	db, cfg := rolesSQLiteDB(t)
	ctx := context.Background()
	require.NoError(t, MigrateUp(ctx, db, cfg))

	exec := func(q string, args ...any) error {
		_, err := db.ExecContext(ctx, q, args...)
		return err
	}
	const ts = "2026-01-01T00:00:00Z"
	require.NoError(t, exec(`INSERT INTO opensheet_users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ('u1', 'u1@x.co', '', '', 0, ?, ?)`, ts, ts))
	require.NoError(t, exec(`INSERT INTO opensheet_users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ('u2', 'u2@x.co', '', '', 0, ?, ?)`, ts, ts))
	require.NoError(t, exec(`INSERT INTO opensheet_orgs (id, slug, name, created_by, created_at, updated_at) VALUES ('o1', 'o1', 'O', 'u1', ?, ?)`, ts, ts))
	require.NoError(t, exec(`INSERT INTO opensheet_orgs (id, slug, name, created_by, created_at, updated_at) VALUES ('o2', 'o2', 'O', 'u1', ?, ?)`, ts, ts))
	require.NoError(t, exec(`INSERT INTO opensheet_roles (id, org_id, name, permissions, created_at, updated_at) VALUES ('r1', 'o1', 'Clerk', '[]', ?, ?)`, ts, ts))

	member := `INSERT INTO opensheet_memberships (id, org_id, user_id, role, role_id, created_at) VALUES (?, ?, 'u2', ?, ?, ?)`
	require.Error(t, exec(member, "m1", "o1", "custom", nil, ts), "custom without role_id")
	require.Error(t, exec(member, "m1", "o1", "member", "r1", ts), "built-in with role_id")
	require.Error(t, exec(member, "m1", "o2", "custom", "r1", ts), "role_id of another org")
	require.NoError(t, exec(member, "m1", "o1", "custom", "r1", ts))

	invite := `INSERT INTO opensheet_invites (id, org_id, email, role, role_id, token_hash, expires_at, invited_by, created_at) VALUES ('i1', ?, 'n@x.co', ?, ?, 'h1', ?, 'u1', ?)`
	require.Error(t, exec(invite, "o1", "custom", nil, ts, ts), "custom invite without role_id")
	require.Error(t, exec(invite, "o1", "admin", "r1", ts, ts), "built-in invite with role_id")
	require.Error(t, exec(invite, "o2", "custom", "r1", ts, ts), "invite role_id of another org")
	require.NoError(t, exec(invite, "o1", "custom", "r1", ts, ts))
}
