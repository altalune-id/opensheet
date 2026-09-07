package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"altalune.id/template/internal/platform/session"
)

func TestSessionFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "session.json")

	in := &sessionFile{
		Principal: session.Principal{
			UserID: uuid.New(),
			Email:  "a@b.co",
			Name:   "Alice",
			Source: session.SourceGenesis,
		},
		IssuedAt: time.Now().UTC().Truncate(time.Second),
	}
	if err := saveSessionFile(path, in); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 0600", info.Mode().Perm())
	}

	out, err := loadSessionFile(path)
	if err != nil || out == nil {
		t.Fatalf("load: err=%v out=%v", err, out)
	}
	if out.Principal.Email != in.Principal.Email {
		t.Errorf("email mismatch: got %q", out.Principal.Email)
	}
}

func TestSessionFile_LoadMissingReturnsNil(t *testing.T) {
	dir := t.TempDir()
	out, err := loadSessionFile(filepath.Join(dir, "nope.json"))
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if out != nil {
		t.Fatal("expected nil session for missing file")
	}
}

func TestSessionFile_ClearIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	if err := clearSessionFile(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := clearSessionFile(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("expected file to be removed")
	}
}
