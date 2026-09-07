package schema

import (
	"strings"
	"testing"
)

// SECURITY: an inlined current_setting(...)::uuid throws 22P02 once a pooled connection has served a tenant, because a transaction-local set_config resets to empty string rather than NULL.
func TestPostgresMigrations_PoliciesUseTheGuardedOrgIDHelper(t *testing.T) {
	entries, err := migrationsFS.ReadDir("migrations/postgres")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	const helperDef = "SELECT NULLIF(current_setting('app.current_org_id', true), '')::uuid"
	seen, defs := 0, 0
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
		defs += strings.Count(src, helperDef)
		inlined := strings.Count(src, "current_setting('app.current_org_id'") - strings.Count(src, helperDef)
		if inlined > 0 {
			t.Errorf("%s inlines current_setting('app.current_org_id') %d time(s); call %s current_org_id() instead", e.Name(), inlined, "{{.Schema}}.{{.TablePrefix}}")
		}
	}
	if seen == 0 {
		t.Fatal("no postgres migrations found; the guard would pass vacuously")
	}
	if defs != 1 {
		t.Errorf("current_org_id() helper defined %d times, want exactly 1", defs)
	}
}
