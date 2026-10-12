package schema

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var (
	sessionsEnableRLSRe = regexp.MustCompile(`(?is)ALTER\s+TABLE\s+(?:{{\.Schema}}\.)?{{\.TablePrefix}}sessions\s+(?:NO\s+)?(?:ENABLE|FORCE)\s+ROW\s+LEVEL\s+SECURITY`)
	sessionsPolicyRe    = regexp.MustCompile(`(?is)CREATE\s+POLICY\s+\S+\s+ON\s+(?:{{\.Schema}}\.)?{{\.TablePrefix}}sessions`)
)

const sessionsRLSConsequence = "a session row is resolved before any tenant scope exists, so an RLS policy on it rejects every login when db.allowBypassRLS=false"

// TestTenantTableSuffixes_OmitsSessions pins sessions out of the RLS-managed table list.
func TestTenantTableSuffixes_OmitsSessions(t *testing.T) {
	if slices.Contains(TenantTableSuffixes, "sessions") {
		t.Fatalf("TenantTableSuffixes contains %q: %s. The list is generated from ALTER TABLE ... ENABLE ROW LEVEL SECURITY in schema/migrations/postgres/*.sql by schema/cmd/gen-tenant-tables, so remove the statement that added it rather than editing tenant_tables_gen.go", "sessions", sessionsRLSConsequence)
	}

	const prefix = "opensheet_"
	if names := TenantTableNames(prefix); slices.Contains(names, prefix+"sessions") {
		t.Fatalf("TenantTableNames(%q) contains %q: %s", prefix, prefix+"sessions", sessionsRLSConsequence)
	}
}

// TestRLSMigration_MatchesTenantTableSuffixes checks the postgres migrations against the generated list in both directions.
func TestRLSMigration_MatchesTenantTableSuffixes(t *testing.T) {
	// NOTE: scans every migration, not just 002_rls.sql — a table added after 002 declares its own RLS block, exactly as gen-tenant-tables scans for it.
	src := allPostgresMigrations(t)

	if len(TenantTableSuffixes) == 0 {
		t.Fatal("TenantTableSuffixes is empty; the guard would pass vacuously")
	}
	for _, suffix := range TenantTableSuffixes {
		enable := fmt.Sprintf("ALTER TABLE {{.Schema}}.{{.TablePrefix}}%s ENABLE ROW LEVEL SECURITY;", suffix)
		if !strings.Contains(src, enable) {
			t.Errorf("no postgres migration enables RLS on %q, but tenant_tables_gen.go lists it as tenant-scoped; the boot RLS audit will fail on every start", suffix)
		}
		policy := fmt.Sprintf("CREATE POLICY {{.TablePrefix}}%s_tenant", suffix)
		if !strings.Contains(src, policy) {
			t.Errorf("no postgres migration has a tenant policy for %q; with FORCE ROW LEVEL SECURITY and no policy the table reads as empty for every org", suffix)
		}
	}

	if rls := readPostgresMigration(t, "002_rls.sql"); strings.Contains(rls, "sessions") {
		t.Errorf("002_rls.sql mentions sessions: %s", sessionsRLSConsequence)
	}
}

// TestPostgresMigrations_NeverEnableRLSOnSessions catches an RLS statement added after the generated list was last refreshed.
func TestPostgresMigrations_NeverEnableRLSOnSessions(t *testing.T) {
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
		src := readPostgresMigration(t, e.Name())
		for _, re := range []*regexp.Regexp{sessionsEnableRLSRe, sessionsPolicyRe} {
			if m := re.FindString(src); m != "" {
				t.Errorf("%s contains %q: %s", e.Name(), strings.Join(strings.Fields(m), " "), sessionsRLSConsequence)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no postgres migrations found; the guard would pass vacuously")
	}
}

func allPostgresMigrations(t *testing.T) string {
	t.Helper()
	entries, err := migrationsFS.ReadDir("migrations/postgres")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	var b strings.Builder
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		b.WriteString(readPostgresMigration(t, e.Name()))
		b.WriteString("\n")
	}
	if b.Len() == 0 {
		t.Fatal("no postgres migrations found; the guard would pass vacuously")
	}
	return b.String()
}

func readPostgresMigration(t *testing.T, name string) string {
	t.Helper()
	body, err := migrationsFS.ReadFile("migrations/postgres/" + name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(body)
}
