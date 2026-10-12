package handlers

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// SECURITY: a handler calling the scope primitives directly resolves a project without the org membership check above it; this guard names any such file.
func TestTenantGateIsNotCopiedIntoHandlers(t *testing.T) {
	t.Parallel()

	rePrimitive := regexp.MustCompile(`\b(OrgScopeFor|ProjectScopeFor)\(`)

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob handlers: %v", err)
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
		if !rePrimitive.Match(b) {
			continue
		}
		switch f {
		case "scope.go":
			sawGate = true
		case "handlers.go": // the primitives are defined here
		default:
			t.Errorf("%s calls OrgScopeFor/ProjectScopeFor directly — use Deps.RequireOrg or Deps.RequireProject", f)
		}
	}
	if !sawGate {
		t.Fatal("scope.go no longer calls the scope primitives — the guard has gone stale")
	}
}
