package schema

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"altalune.id/opensheet/internal/platform/config"
)

func TestMigration019_PartialUniqueIndex(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "m.db") + "?_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := MigrateUp(ctx, db, config.Defaults()); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}

	exec := func(q string, args ...any) error {
		_, err := db.ExecContext(ctx, q, args...)
		return err
	}
	const ts = "2026-01-01T00:00:00Z"
	for _, q := range []string{
		`INSERT INTO opensheet_users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ('u1', 'u1@x.co', '', '', 0, '` + ts + `', '` + ts + `')`,
		`INSERT INTO opensheet_orgs (id, slug, name, created_by, created_at, updated_at) VALUES ('o1', 'o1', 'O', 'u1', '` + ts + `', '` + ts + `')`,
		`INSERT INTO opensheet_projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES ('p1', 'o1', 'p1', 'P', 'u1', '` + ts + `', '` + ts + `')`,
		`INSERT INTO opensheet_projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES ('p2', 'o1', 'p2', 'P', 'u1', '` + ts + `', '` + ts + `')`,
		`INSERT INTO opensheet_credentials (id, org_id, project_id, name, kind, authorized_by_user_id, sealed, created_at, updated_at) VALUES ('c1', 'o1', 'p1', 'c', 'google_oauth', 'u1', x'01', '` + ts + `', '` + ts + `')`,
	} {
		if err := exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	insert := func(id, project, file string, key any) error {
		return exec(`INSERT INTO opensheet_spreadsheets (id, org_id, project_id, credential_id, google_file_id, created_at, updated_at, provision_key)
			VALUES (?, 'o1', ?, 'c1', ?, ?, ?, ?)`, id, project, file, ts, ts, key)
	}

	if err := insert("s1", "p1", "f1", nil); err != nil {
		t.Fatalf("first NULL key: %v", err)
	}
	if err := insert("s2", "p1", "f2", nil); err != nil {
		t.Fatalf("two NULL keys in one project must both insert: %v", err)
	}
	if err := insert("s3", "p1", "f3", "k1"); err != nil {
		t.Fatalf("first key: %v", err)
	}
	if err := insert("s4", "p1", "f4", "k1"); err == nil {
		t.Fatal("a duplicate key in one project must be refused")
	}
	if err := insert("s5", "p2", "f5", "k1"); err != nil {
		t.Fatalf("the same key in another project must insert: %v", err)
	}
}
