package schema

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"altalune.id/opensheet/internal/platform/config"
)

// SECURITY: the SQLite rebuild of api_keys drops the old table, which fires todos' ON DELETE SET NULL; foreign keys are on here, as in production, so a lost link would show.
func TestMigrateUp_SQLite_OrgAPIKeysKeepsTodoKeyAuthorship(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "m.db") + "?_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	cfg := config.Defaults()
	if err := MigrateUp(ctx, db, cfg); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	if err := MigrateDownTo(ctx, db, cfg, 14); err != nil {
		t.Fatalf("MigrateDownTo 14: %v", err)
	}

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	const ts = "2026-01-01T00:00:00Z"
	exec(`INSERT INTO opensheet_users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ('u1', 'u1@x.co', '', '', 0, ?, ?)`, ts, ts)
	exec(`INSERT INTO opensheet_orgs (id, slug, name, created_by, created_at, updated_at) VALUES ('o1', 'o1', 'O', 'u1', ?, ?)`, ts, ts)
	exec(`INSERT INTO opensheet_projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES ('p1', 'o1', 'p1', 'P', 'u1', ?, ?)`, ts, ts)
	exec(`INSERT INTO opensheet_api_keys (id, org_id, project_id, name, secret_hash, created_at) VALUES ('k1', 'o1', 'p1', 'ci', x'01', ?)`, ts)
	exec(`INSERT INTO opensheet_todos (id, org_id, project_id, created_by_key_id, title, created_at, updated_at) VALUES ('t1', 'o1', 'p1', 'k1', 'by key', ?, ?)`, ts, ts)

	author := func() string {
		t.Helper()
		var got sql.NullString
		if err := db.QueryRowContext(ctx, `SELECT created_by_key_id FROM opensheet_todos WHERE id = 't1'`).Scan(&got); err != nil {
			t.Fatal(err)
		}
		return got.String
	}

	if err := MigrateUp(ctx, db, cfg); err != nil {
		t.Fatalf("MigrateUp over an existing key: %v", err)
	}
	if got := author(); got != "k1" {
		t.Fatalf("todo key authorship after up = %q, want k1", got)
	}
	var kind string
	if err := db.QueryRowContext(ctx, `SELECT kind FROM opensheet_api_keys WHERE id = 'k1'`).Scan(&kind); err != nil {
		t.Fatal(err)
	}
	if kind != "project" {
		t.Fatalf("an existing key migrated as kind %q, want project", kind)
	}

	if err := MigrateDownTo(ctx, db, cfg, 14); err != nil {
		t.Fatalf("MigrateDownTo 14 again: %v", err)
	}
	if got := author(); got != "k1" {
		t.Fatalf("todo key authorship after down = %q, want k1", got)
	}
}
