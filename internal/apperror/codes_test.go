package apperror_test

import (
	"os"
	"path/filepath"
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

func documentedCodes(t *testing.T) string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "docs", "errors", "*.md"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no docs/errors/*.md: %v", err)
	}
	var b strings.Builder
	for _, f := range files {
		raw, err := os.ReadFile(f)
		require.NoError(t, err)
		b.Write(raw)
	}
	return b.String()
}

func TestCodes_EveryRefIsDocumented(t *testing.T) {
	doc := documentedCodes(t)
	documented := map[string]bool{}
	for _, m := range reDoc.FindAllStringSubmatch(doc, -1) {
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
	require.Empty(t, missing, "add these to docs/errors/*.md:\n%s", strings.Join(missing, "\n"))

	refs := map[string]bool{}
	for _, ref := range reg {
		refs[ref] = true
	}
	for ref := range documented {
		require.True(t, refs[ref], "docs/errors/*.md lists %s, which no code defines", ref)
	}
}
