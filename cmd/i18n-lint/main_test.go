package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
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

func TestRun_OK(t *testing.T) {
	t.Parallel()
	tdir := t.TempDir()
	writeFile(t, filepath.Join(tdir, "templates", "a.templ"), `d.Tr("k")`)
	writeFile(t, filepath.Join(tdir, "locales", "active.en-US.yaml"), "k: v\n")

	var out, errBuf bytes.Buffer
	code := run(&out, &errBuf, []string{
		"-templates", filepath.Join(tdir, "templates"),
		"-locales", filepath.Join(tdir, "locales"),
		"-check",
	})
	if code != 0 {
		t.Fatalf("code=%d out=%s err=%s", code, out.String(), errBuf.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("OK")) {
		t.Errorf("expected OK: %s", out.String())
	}
}

func TestRun_FailsCheck(t *testing.T) {
	t.Parallel()
	tdir := t.TempDir()
	writeFile(t, filepath.Join(tdir, "templates", "a.templ"), `d.Tr("nav.missing")`)
	writeFile(t, filepath.Join(tdir, "locales", "active.en-US.yaml"), "k: v\n")

	var out, errBuf bytes.Buffer
	code := run(&out, &errBuf, []string{
		"-templates", filepath.Join(tdir, "templates"),
		"-locales", filepath.Join(tdir, "locales"),
		"-check",
	})
	if code == 0 {
		t.Fatalf("expected non-zero exit, got 0: out=%s", out.String())
	}
}

func TestRun_LocalesDirMissing(t *testing.T) {
	t.Parallel()
	var out, errBuf bytes.Buffer
	code := run(&out, &errBuf, []string{"-locales", "/does/not/exist"})
	if code != 2 {
		t.Errorf("code=%d want 2", code)
	}
}

func TestRun_BadFlag(t *testing.T) {
	t.Parallel()
	var out, errBuf bytes.Buffer
	code := run(&out, &errBuf, []string{"-unknown-flag"})
	if code != 2 {
		t.Errorf("code=%d want 2", code)
	}
}
