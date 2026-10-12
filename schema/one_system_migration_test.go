package schema

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"altalune.id/opensheet/internal/platform/config"
)

func TestMigrateUp_SQLite_OneSystemIndexDemotesExistingDuplicates(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	cfg := config.Defaults()
	if err := MigrateUp(ctx, db, cfg); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	if err := MigrateDownTo(ctx, db, cfg, 13); err != nil {
		t.Fatalf("MigrateDownTo 13: %v", err)
	}

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO opensheet_users (id, email, name, avatar_url, is_admin, created_at, updated_at)
		VALUES ('u1', 'u1@x.co', '', '', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	for _, o := range [][2]string{{"org-z", "2026-01-01T00:00:00Z"}, {"org-a", "2026-02-01T00:00:00Z"}, {"org-m", "2026-02-01T00:00:00Z"}} {
		exec(`INSERT INTO opensheet_orgs (id, slug, name, system, created_by, created_at, updated_at)
			VALUES (?, ?, 'O', 1, 'u1', ?, ?)`, o[0], o[0], o[1], o[1])
	}
	for _, p := range [][3]string{
		{"p-z", "org-z", "2026-01-01T00:00:00Z"}, {"p-a", "org-z", "2026-01-02T00:00:00Z"},
		{"q-b", "org-a", "2026-03-01T00:00:00Z"}, {"q-a", "org-a", "2026-03-01T00:00:00Z"},
	} {
		exec(`INSERT INTO opensheet_projects (id, org_id, slug, name, system, created_by, created_at, updated_at)
			VALUES (?, ?, ?, 'P', 1, 'u1', ?, ?)`, p[0], p[1], p[0], p[2], p[2])
	}

	if err := MigrateUp(ctx, db, cfg); err != nil {
		t.Fatalf("MigrateUp over duplicate system rows must demote, not fail: %v", err)
	}

	systemIDs := func(q string) []string {
		t.Helper()
		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			out = append(out, id)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := systemIDs(`SELECT id FROM opensheet_orgs WHERE system = 1`); len(got) != 1 || got[0] != "org-z" {
		t.Fatalf("system orgs = %v, want only the earliest (org-z), matching SystemOrg", got)
	}
	if got := systemIDs(`SELECT id FROM opensheet_projects WHERE system = 1 ORDER BY org_id`); len(got) != 2 || got[0] != "q-a" || got[1] != "p-z" {
		t.Fatalf("system projects = %v, want the earliest per org (q-a by id tie-break, p-z by created_at)", got)
	}
}
