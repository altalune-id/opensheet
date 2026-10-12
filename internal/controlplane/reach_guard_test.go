package controlplane

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// SECURITY: a handler comparing a row's tenant against the principal by hand skips the key's project grant; this guard names any such file.
func TestReachIsNotCopiedIntoHandlers(t *testing.T) {
	t.Parallel()

	reHandRolled := regexp.MustCompile(`\.(OrgID|ProjectID)\s*[!=]=\s*\w+\.Active(Org|Project)ID|\.Active(Org|Project)ID\s*[!=]=\s*\w+\.(OrgID|ProjectID)\b`)
	reGate := regexp.MustCompile(`\.Reaches(Project|WholeProject|Resource)\(`)

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob controlplane: %v", err)
	}
	sawGate := false
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, rErr := os.ReadFile(f) //nolint:gosec // G304: path comes from a package-local glob
		if rErr != nil {
			t.Fatalf("read %s: %v", f, rErr)
		}
		if f == "reach.go" && reGate.Match(b) {
			sawGate = true
		}
		if loc := reHandRolled.FindIndex(b); loc != nil {
			t.Errorf("%s compares a tenant id against the principal by hand (%q) — use scopeToProject or scopeToResource", f, b[loc[0]:loc[1]])
		}
	}
	if !sawGate {
		t.Fatal("reach.go no longer asks session.Principal for reach — the guard has gone stale")
	}
}
