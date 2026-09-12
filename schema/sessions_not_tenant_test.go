package schema

import (
	"strings"
	"testing"
)

// SECURITY: a session is loaded before any tenant scope exists, so an RLS policy on this
// table returns zero rows at login. The failure appears only when bypass is disabled.
func TestSessionsIsNotTenantScoped(t *testing.T) {
	t.Parallel()
	for _, s := range TenantTableSuffixes {
		if strings.TrimSpace(s) == "sessions" {
			t.Fatalf("sessions must not be tenant-scoped: it is loaded before any org scope exists")
		}
	}
}
