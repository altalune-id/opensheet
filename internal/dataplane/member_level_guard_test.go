package dataplane

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// SECURITY: a token is admitted on membership alone, so a manager-only action here needs a role check in the token path first.
func TestDataPlane_SheetActionsAreMemberLevel(t *testing.T) {
	t.Parallel()

	reGate := regexp.MustCompile(`\b(RequireManager|RequireCallerManager|IsManager)\(`)
	if b, err := os.ReadFile(filepath.Join("..", "org", "service.go")); err != nil || !reGate.Match(b) {
		t.Fatalf("org/service.go no longer declares a manager gate (%v): the guard has gone stale", err)
	}

	for _, pkg := range []string{"sheet", "spreadsheet", "credential"} {
		files, err := filepath.Glob(filepath.Join("..", pkg, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("glob %s: %d files, %v", pkg, len(files), err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, rErr := os.ReadFile(f) //nolint:gosec // G304: path comes from a package-local glob
			if rErr != nil {
				t.Fatalf("read %s: %v", f, rErr)
			}
			if loc := reGate.FindIndex(b); loc != nil {
				t.Errorf("%s calls a manager gate (%q): add the role check to the data plane token path before shipping it", f, b[loc[0]:loc[1]])
			}
		}
	}
}
