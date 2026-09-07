package keys_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"altalune.id/template/cmd/i18n-lint/internal/keys"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func setup(t *testing.T, templates, locales map[string]string) keys.Options {
	t.Helper()
	tdir := t.TempDir()
	tmplDir := filepath.Join(tdir, "templates")
	locDir := filepath.Join(tdir, "locales")
	for name, body := range templates {
		writeFile(t, filepath.Join(tmplDir, name), body)
	}
	for name, body := range locales {
		writeFile(t, filepath.Join(locDir, name), body)
	}
	return keys.Options{TemplatesDir: tmplDir, LocalesDir: locDir}
}

func TestRun_AllMatch(t *testing.T) {
	t.Parallel()
	opts := setup(t,
		map[string]string{
			"a.templ": `templ Foo() { <span>{ d.Tr("nav.overview") }</span> }`,
		},
		map[string]string{
			"active.en-US.yaml": "nav.overview: Overview\n",
			"active.id-ID.yaml": "nav.overview: Ringkasan\n",
		},
	)
	r, err := keys.Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Usages) != 1 {
		t.Errorf("usages=%d", len(r.Usages))
	}
	for l, ks := range r.Missing {
		if len(ks) > 0 {
			t.Errorf("locale %s missing %v — should be empty", l, ks)
		}
	}
	if r.HasBlockingIssues(false) {
		t.Error("no issues expected")
	}
}

func TestRun_MissingKeyDetected(t *testing.T) {
	t.Parallel()
	opts := setup(t,
		map[string]string{"a.templ": `d.Tr("nav.projects")`},
		map[string]string{"active.en-US.yaml": "nav.overview: Overview\n"},
	)
	r, err := keys.Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !keys.Contains(r.Missing["en-US"], "nav.projects") {
		t.Errorf("expected nav.projects missing, got %v", r.Missing["en-US"])
	}
	if !r.HasBlockingIssues(false) {
		t.Error("check should fail")
	}
}

func TestRun_DeadKey(t *testing.T) {
	t.Parallel()
	opts := setup(t,
		map[string]string{"a.templ": `d.Tr("nav.overview")`},
		map[string]string{"active.en-US.yaml": "nav.overview: Overview\nnav.dead: Dead\n"},
	)
	r, err := keys.Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !keys.Contains(r.Dead, "nav.dead") {
		t.Errorf("expected nav.dead in Dead, got %v", r.Dead)
	}
	if r.HasBlockingIssues(false) {
		t.Error("dead keys should not fail without --strict")
	}
	if !r.HasBlockingIssues(true) {
		t.Error("dead keys should fail with --strict")
	}
}

func TestRun_IncompletePluralArabic(t *testing.T) {
	t.Parallel()
	opts := setup(t,
		map[string]string{"a.templ": `d.TrN("count", n)`},
		map[string]string{
			"active.ar-SA.yaml": "count:\n  other: '{{.Count}}'\n",
		},
	)
	r, err := keys.Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !keys.Contains(r.IncompletePlurals["ar-SA"], "count") {
		t.Errorf("expected count in incomplete plurals for ar-SA, got %v", r.IncompletePlurals)
	}
	if !r.HasBlockingIssues(false) {
		t.Error("incomplete plurals must fail --check")
	}
}

func TestRun_IndonesianOnlyNeedsOther(t *testing.T) {
	t.Parallel()
	opts := setup(t,
		map[string]string{"a.templ": `d.TrN("count", n)`},
		map[string]string{
			"active.id-ID.yaml": "count:\n  other: '{{.Count}}'\n",
		},
	)
	r, err := keys.Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.IncompletePlurals["id-ID"]) != 0 {
		t.Errorf("Indonesian only needs 'other', got %v", r.IncompletePlurals["id-ID"])
	}
}

func TestRun_FixWritesEmptyPlaceholders(t *testing.T) {
	t.Parallel()
	tdir := t.TempDir()
	tmplDir := filepath.Join(tdir, "templates")
	locDir := filepath.Join(tdir, "locales")
	writeFile(t, filepath.Join(tmplDir, "a.templ"), `d.Tr("nav.projects")`)
	writeFile(t, filepath.Join(locDir, "active.en-US.yaml"), "nav.overview: Overview\n")

	if _, err := keys.Run(keys.Options{TemplatesDir: tmplDir, LocalesDir: locDir, Fix: true}); err != nil {
		t.Fatal(err)
	}
	buf, err := os.ReadFile(filepath.Join(locDir, "active.en-US.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf, []byte("nav.projects")) {
		t.Errorf("after --fix file should contain nav.projects, got:\n%s", buf)
	}
}

func TestRun_TemplateDirMissing(t *testing.T) {
	t.Parallel()
	_, err := keys.Run(keys.Options{TemplatesDir: "/does/not/exist", LocalesDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRun_NestedYAMLKeys(t *testing.T) {
	t.Parallel()
	opts := setup(t,
		map[string]string{"a.templ": `d.Tr("nav.overview") d.Tr("dashboard.title")`},
		map[string]string{
			"active.en-US.yaml": "nav:\n  overview: Overview\ndashboard:\n  title: Dashboard\n",
		},
	)
	r, err := keys.Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Missing["en-US"]) != 0 {
		t.Errorf("nested keys should be recognised, got missing=%v", r.Missing["en-US"])
	}
}

func TestReport_PrintWithAllIssues(t *testing.T) {
	t.Parallel()
	opts := setup(t,
		map[string]string{"a.templ": `d.Tr("nav.missing") d.TrN("count", n)`},
		map[string]string{
			"active.en-US.yaml": "nav.other: Other\ncount:\n  other: '{{.Count}}'\n",
			"active.ar-SA.yaml": "count:\n  other: '{{.Count}}'\n",
		},
	)
	r, err := keys.Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	r.Print(&buf)
	got := buf.String()
	for _, sub := range []string{"missing keys per locale", "incomplete plural forms per locale", "dead keys"} {
		if !bytes.Contains([]byte(got), []byte(sub)) {
			t.Errorf("Print output missing section %q; got:\n%s", sub, got)
		}
	}
}

func TestReport_PrintRunsClean(t *testing.T) {
	t.Parallel()
	opts := setup(t,
		map[string]string{"a.templ": `d.Tr("k")`},
		map[string]string{"active.en-US.yaml": "k: v\n"},
	)
	r, err := keys.Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	r.Print(&buf)
	if !bytes.Contains(buf.Bytes(), []byte("OK")) {
		t.Errorf("expected OK message, got: %s", buf.String())
	}
}
