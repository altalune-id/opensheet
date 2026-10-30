package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeRemoteProfile(t *testing.T, path, name string, p profile) {
	t.Helper()
	if err := (profileStore{path: path}).put(t.Context(), name, p); err != nil {
		t.Fatalf("put profile: %v", err)
	}
}

func TestSessionFile_RoundTripKeepsProfilesByName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "session.json")
	store := profileStore{path: path}
	exp := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	remote := profile{
		URL: "https://app.example.com", ExpiresAt: exp, Principal: principalView{Email: "a@b.co"},
		Token: &profileToken{Issuer: "https://idp.example.com", ClientID: "cli", Resource: "https://app.example.com/api", Access: "at", Refresh: "rt"},
	}
	if err := store.put(t.Context(), "prod", remote); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := store.put(t.Context(), defaultProfile, profile{Principal: principalView{Email: "local@b.co"}}); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, err := store.load("prod")
	if err != nil || got == nil {
		t.Fatalf("load: %v %v", got, err)
	}
	if got.URL != remote.URL || !got.ExpiresAt.Equal(exp) || got.Token.Refresh != "rt" || got.Principal.Email != "a@b.co" {
		t.Fatalf("profile = %+v", got)
	}
	local, err := store.load(defaultProfile)
	if err != nil || local == nil || local.URL != "" || local.Token != nil {
		t.Fatalf("local profile = %+v, %v", local, err)
	}
	if missing, err := store.load("nope"); err != nil || missing != nil {
		t.Fatalf("missing profile = %+v, %v", missing, err)
	}
}

func TestSessionFile_IsPrivateAndAtomic(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "opensheet")
	path := filepath.Join(dir, "session.json")
	for range 3 {
		if err := saveSessionFile(path, &sessionFile{Profiles: map[string]profile{"p": {URL: "https://a.example"}}}); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %o, want 0600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %o, want 0700", dirInfo.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "session.json" && e.Name() != "session.json.lock" {
			t.Errorf("leftover file %q: a save must rename its temp file into place", e.Name())
		}
	}
}

func TestSessionFile_MissingOrOldFormatLoadsEmpty(t *testing.T) {
	dir := t.TempDir()
	f, err := loadSessionFile(filepath.Join(dir, "nope.json"))
	if err != nil || f == nil || len(f.Profiles) != 0 {
		t.Fatalf("missing file = %+v, %v", f, err)
	}

	old := filepath.Join(dir, "old.json")
	legacy := `{"principal":{"UserID":"3f0e0c4e-8a43-4bb5-9c4a-9d7f1d7b2f10","Email":"z@z"},"profiles":{"https://a.example":{"url":"https://a.example","device":"d"}}}`
	if err := os.WriteFile(old, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err = loadSessionFile(old)
	if err != nil || len(f.Profiles) != 0 {
		t.Fatalf("an old-format file must load empty so people sign in again; got %+v, %v", f, err)
	}
}

func TestSessionFile_RefusesANewerFormatAndALooseMode(t *testing.T) {
	dir := t.TempDir()
	newer := filepath.Join(dir, "newer.json")
	if err := os.WriteFile(newer, []byte(`{"version": 2, "profiles": {}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSessionFile(newer); !IsSessionFileError(err) {
		t.Fatalf("a newer format must be an error, never loaded empty and overwritten; got %v", err)
	}

	loose := filepath.Join(dir, "loose.json")
	if err := os.WriteFile(loose, []byte(`{"version": 1, "profiles": {}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(loose, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSessionFile(loose); !IsSessionFileError(err) || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("a world-readable session file must be refused; got %v", err)
	}
}

func TestProfileStore_LockWaitHonoursTheContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	unlock, err := lockSessionFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	err = (profileStore{path: path}).put(ctx, "p", profile{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a held lock must give way to the context, got %v", err)
	}
}
