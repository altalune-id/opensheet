package schema

import (
	"strings"
	"testing"
)

func TestPostgresMigrations_CarryNoRoleStatements(t *testing.T) {
	entries, err := migrationsFS.ReadDir("migrations/postgres")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	seen := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		seen++
		body, err := migrationsFS.ReadFile("migrations/postgres/" + e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		src := string(body)
		for _, banned := range []string{"SET ROLE", "RESET ROLE", "{{.Role}}"} {
			if strings.Contains(src, banned) {
				t.Errorf("%s still contains %q", e.Name(), banned)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no postgres migrations found; the guard would pass vacuously")
	}
}
