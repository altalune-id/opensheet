package schema

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"testing/fstest"

	_ "modernc.org/sqlite"

	"altalune.id/template/internal/platform/config"
)

func TestRenderTemplate_SubstitutesTablePrefix(t *testing.T) {
	body := []byte(`CREATE TABLE {{.TablePrefix}}users (id TEXT);`)
	got, err := renderTemplate("001.sql", body, templateVars{TablePrefix: "altempl_"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "altempl_users") {
		t.Errorf("template did not substitute: %s", got)
	}
}

func TestRenderTemplate_StripsRLSBlockWhenDisabled(t *testing.T) {
	body := []byte(`CREATE TABLE t (id TEXT);
{{if .RLSEnforce}}
ALTER TABLE t ENABLE ROW LEVEL SECURITY;
{{end}}`)
	got, err := renderTemplate("001.sql", body, templateVars{RLSEnforce: false})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "ENABLE ROW LEVEL SECURITY") {
		t.Errorf("RLS block leaked with RLSEnforce=false: %s", got)
	}
}

func TestRenderTemplate_KeepsRLSBlockWhenEnabled(t *testing.T) {
	body := []byte(`CREATE TABLE t (id TEXT);
{{if .RLSEnforce}}
ALTER TABLE t ENABLE ROW LEVEL SECURITY;
{{end}}`)
	got, err := renderTemplate("001.sql", body, templateVars{RLSEnforce: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "ENABLE ROW LEVEL SECURITY") {
		t.Errorf("RLS block missing with RLSEnforce=true: %s", got)
	}
}

func TestRenderTemplate_ShortCircuitsWhenNoDelimiters(t *testing.T) {
	body := []byte(`CREATE TABLE t (id TEXT);`)
	got, err := renderTemplate("001.sql", body, templateVars{})
	if err != nil {
		t.Fatal(err)
	}
	if &got[0] != &body[0] {
		t.Errorf("expected zero-copy passthrough (same underlying array) when no delimiters present")
	}
	if string(got) != string(body) {
		t.Errorf("passthrough mismatch")
	}
}

func TestRenderTemplate_MissingKeyErrors(t *testing.T) {
	body := []byte(`{{.NotAField}}`)
	if _, err := renderTemplate("001.sql", body, templateVars{}); err == nil {
		t.Fatal("expected missingkey=error to fail on unknown field")
	}
}

func TestTemplatedFS_ReadFileRenders(t *testing.T) {
	base := fstest.MapFS{
		"001.sql": &fstest.MapFile{Data: []byte(`SELECT '{{.TablePrefix}}';`)},
		"README":  &fstest.MapFile{Data: []byte("noop")},
	}
	tfs := newTemplatedFS(base, templateVars{TablePrefix: "altempl_"}).(*templatedFS)
	got, err := tfs.ReadFile("001.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "altempl_") {
		t.Errorf("expected rendered body, got %s", got)
	}
	nonSQL, err := tfs.ReadFile("README")
	if err != nil {
		t.Fatal(err)
	}
	if string(nonSQL) != "noop" {
		t.Errorf("non-sql should pass through, got %s", nonSQL)
	}
}

func TestTemplatedFS_OpenRenders(t *testing.T) {
	base := fstest.MapFS{
		"001.sql": &fstest.MapFile{Data: []byte(`SELECT '{{.TablePrefix}}';`)},
	}
	tfs := newTemplatedFS(base, templateVars{TablePrefix: "altempl_"})
	f, err := tfs.Open("001.sql")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 128)
	n, _ := f.Read(buf)
	if !strings.Contains(string(buf[:n]), "altempl_") {
		t.Errorf("Open did not render: %s", buf[:n])
	}
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.IsDir() {
		t.Error("expected file")
	}
}

func TestMigrateUp_SQLite_CreatesAllTables(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cfg := config.Defaults()
	if err := MigrateUp(context.Background(), db, cfg); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}

	wantTables := []string{
		"altempl_users",
		"altempl_orgs",
		"altempl_memberships",
		"altempl_projects",
		"altempl_invites",
		"altempl_todos",
	}
	for _, tbl := range wantTables {
		var n int
		if err := db.QueryRow(
			`SELECT count(*) FROM sqlite_master WHERE type='table' AND name = ?`, tbl,
		).Scan(&n); err != nil {
			t.Fatalf("query %s: %v", tbl, err)
		}
		if n != 1 {
			t.Errorf("table %s missing (count=%d)", tbl, n)
		}
	}
}

func TestMigrateUp_SQLite_Idempotent(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cfg := config.Defaults()
	if err := MigrateUp(context.Background(), db, cfg); err != nil {
		t.Fatal(err)
	}
	if err := MigrateUp(context.Background(), db, cfg); err != nil {
		t.Fatalf("second MigrateUp failed: %v", err)
	}
}
