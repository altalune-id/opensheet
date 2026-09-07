package apperror_test

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var (
	reConst = regexp.MustCompile(`(?m)^\tCode(\w+)\s+=\s+"([^"]+)"`)
	reRef   = regexp.MustCompile(`^[A-Z]{3}[0-9]{3}$`)
	reDoc   = regexp.MustCompile(`(?m)^\|\s*` + "`" + `([A-Z]{3}[0-9]{3})` + "`" + `\s*\|`)
)

// registry parses codes.go so the assertions below have a single source of truth that cannot drift.
func registry(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile("codes.go")
	require.NoError(t, err)
	found := reConst.FindAllStringSubmatch(string(b), -1)
	require.NotEmpty(t, found, "parsed no codes — the scraper regex has gone stale")
	out := make(map[string]string, len(found))
	for _, m := range found {
		out["Code"+m[1]] = m[2]
	}
	return out
}

func TestCodes_RefsAreWellFormed(t *testing.T) {
	for name, ref := range registry(t) {
		require.Regexp(t, reRef, ref, "%s must be a three-letter domain plus three digits", name)
	}
}

func TestCodes_RefsAreUnique(t *testing.T) {
	seen := map[string]string{}
	for name, ref := range registry(t) {
		if prev, dup := seen[ref]; dup {
			t.Fatalf("%s and %s share the ref %s — refs are quoted by users and must be unique", prev, name, ref)
		}
		seen[ref] = name
	}
}

// TestCodes_UnexpectedFailuresUseTheReservedBlock keeps the 900-999 range meaning what the registry says it means.
func TestCodes_UnexpectedFailuresUseTheReservedBlock(t *testing.T) {
	reg := registry(t)
	require.Equal(t, "GEN900", reg["CodeUnexpectedError"])
	for name, ref := range reg {
		if name == "CodeUnexpectedError" {
			continue
		}
		require.Less(t, ref[3:], "900", "%s = %s sits in the reserved unexpected block", name, ref)
	}
}

func TestCodes_EveryRefIsDocumented(t *testing.T) {
	b, err := os.ReadFile("../../docs/ERROR_CODES.md")
	require.NoError(t, err, "docs/ERROR_CODES.md is the table users are pointed at")
	documented := map[string]bool{}
	for _, m := range reDoc.FindAllStringSubmatch(string(b), -1) {
		documented[m[1]] = true
	}
	reg := registry(t)
	var missing []string
	for name, ref := range reg {
		if !documented[ref] {
			missing = append(missing, ref+" ("+name+")")
		}
	}
	sort.Strings(missing)
	require.Empty(t, missing, "add these to docs/ERROR_CODES.md:\n%s", strings.Join(missing, "\n"))

	refs := map[string]bool{}
	for _, ref := range reg {
		refs[ref] = true
	}
	for ref := range documented {
		require.True(t, refs[ref], "docs/ERROR_CODES.md lists %s, which no code defines", ref)
	}
}
