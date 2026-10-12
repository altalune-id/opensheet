package comments

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckFile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		fixture string
		want    []Violation
	}{
		{
			name:    "compliant fixture passes every rule",
			fixture: "compliant.go",
		},
		{
			name:    "comment on an unexported identifier is reported",
			fixture: "unexported_doc.go",
			want: []Violation{
				{Line: 5, Ident: "helper", Rule: RuleUnexportedDoc},
				{Line: 8, Ident: "var tally", Rule: RuleUnexportedDoc},
				{Line: 11, Ident: "const knob", Rule: RuleUnexportedDoc},
				{Line: 14, Ident: "type bucket", Rule: RuleUnexportedDoc},
			},
		},
		{
			name:    "multi-line godoc on an exported identifier is reported",
			fixture: "multiline_doc.go",
			want: []Violation{
				{Line: 5, Ident: "Load", Rule: RuleMultilineDoc},
				{Line: 10, Ident: "type Widget", Rule: RuleMultilineDoc},
			},
		},
		{
			name:    "blank comment line inside a doc block is reported",
			fixture: "blank_doc_line.go",
			want: []Violation{
				{Line: 6, Ident: "BySlug", Rule: RuleBlankDocLine},
				{Line: 5, Ident: "BySlug", Rule: RuleMultilineDoc},
			},
		},
		{
			name:    "decorative section header is reported",
			fixture: "section_header.go",
			want: []Violation{
				{Line: 5, Ident: "Build", Rule: RuleSectionHeader},
			},
		},
		{
			name:    "multi-line prose block inside a body is reported",
			fixture: "prose_block.go",
			want: []Violation{
				{Line: 6, Ident: "(free comment)", Rule: RuleProseBlock},
			},
		},
		{
			name:    "generated files are skipped",
			fixture: "generated.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join("testdata", tt.fixture)
			got, err := checkFile(path)
			if err != nil {
				t.Fatalf("checkFile(%s): %v", tt.fixture, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d violations, want %d:\n%s", len(got), len(tt.want), render(got))
			}
			for _, want := range tt.want {
				if !contains(got, want) {
					t.Errorf("missing %s at line %d (%s); got:\n%s", want.Rule, want.Line, want.Ident, render(got))
				}
			}
		})
	}
}

func TestCheckFileExemptions(t *testing.T) {
	t.Parallel()
	got, err := checkFile(filepath.Join("testdata", "compliant.go"))
	if err != nil {
		t.Fatalf("checkFile: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("markers, URLs, directives and the blank-line-before-directive case must all be exempt; got:\n%s", render(got))
	}
}

func TestRunSkipsGeneratedAndVendoredTrees(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	violating := "package p\n\n// helper does the work.\nfunc helper() {}\n"
	files := map[string]string{
		"good.go":                "package p\n\n// Helper does the work.\nfunc Helper() {}\n",
		"bad.go":                 violating,
		"page_templ.go":          violating,
		"api.pb.go":              violating,
		"api.connect.go":         violating,
		"tool.mcp.go":            violating,
		"gen/x.go":               violating,
		"vendor/y.go":            violating,
		"testdata/z.go":          violating,
		"internal/node_modules/": "",
	}
	for name, body := range files {
		if body == "" {
			continue
		}
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	report, err := Run(Options{Roots: []string{root}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Violations) != 1 {
		t.Fatalf("want only bad.go reported, got:\n%s", render(report.Violations))
	}
	if filepath.Base(report.Violations[0].File) != "bad.go" {
		t.Errorf("reported %s, want bad.go", report.Violations[0].File)
	}
}

func TestReportPrintTalliesByRule(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	Report{Files: 2, Violations: []Violation{
		{File: "a.go", Line: 1, Ident: "x", Rule: RuleUnexportedDoc, Message: "m"},
		{File: "b.go", Line: 2, Ident: "Y", Rule: RuleMultilineDoc, Message: "m"},
	}}.Print(&buf)
	out := buf.String()
	for _, want := range []string{"a.go:1: [unexported-doc] x: m", "2 violations in 2 files", "multiline-doc    1"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func contains(got []Violation, want Violation) bool {
	for _, v := range got {
		if v.Line == want.Line && v.Ident == want.Ident && v.Rule == want.Rule {
			return true
		}
	}
	return false
}

func render(vs []Violation) string {
	var b strings.Builder
	for _, v := range vs {
		b.WriteString("  " + v.String() + "\n")
	}
	return b.String()
}
