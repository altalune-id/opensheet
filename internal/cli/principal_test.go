package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/session"
)

func TestResolve_FromSessionFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	uid := uuid.New()
	oid := uuid.New()
	pid := uuid.New()
	if err := saveSessionFile(path, &sessionFile{
		Principal: session.Principal{
			UserID: uid, Email: "z@z", Source: session.SourceGenesis,
			ActiveOrgID: oid, ActiveProjectID: pid,
		},
		IssuedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Session: config.SessionConfig{Path: path}}

	root := NewRootCmd(stubServerBoot, stubClientBoot)
	root.SetArgs([]string{"version"})
	// Root cmd has an empty context; principal.Resolve will read from flags on it.
	root.SetContext(context.Background())

	p, err := Resolve(context.Background(), root, cfg, stubClientBoot)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.UserID != uid || p.OrgID != oid || p.ProjectID != pid {
		t.Errorf("Resolve returned unexpected principal: %+v", p)
	}
	if p.Source != SourceSession {
		t.Errorf("source = %q, want session", p.Source)
	}
}

func TestResolve_NoSessionNoToken(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Session: config.SessionConfig{Path: filepath.Join(dir, "nope.json")}}

	root := NewRootCmd(stubServerBoot, stubClientBoot)
	root.SetArgs([]string{"version"})
	root.SetContext(context.Background())

	if _, err := Resolve(context.Background(), root, cfg, stubClientBoot); !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("want ErrNotSignedIn, got %v", err)
	}
}

func TestResolve_TokenFlagWins(t *testing.T) {
	t.Setenv("ALT_TOKEN", "env-token")
	cfg := &config.Config{}

	root := NewRootCmd(stubServerBoot, stubClientBoot)
	root.SetContext(context.Background())
	root.SetArgs([]string{"--token", "flag-token", "version"})
	// Trigger parse without executing subcommand run.
	if err := root.ParseFlags([]string{"--token", "flag-token"}); err != nil {
		t.Fatal(err)
	}

	p, err := Resolve(context.Background(), root, cfg, stubClientBoot)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.Source != SourceFlag {
		t.Errorf("source = %q, want flag", p.Source)
	}
}

func TestResolve_TokenEnv(t *testing.T) {
	t.Setenv("ALT_TOKEN", "env-token")
	cfg := &config.Config{}

	root := NewRootCmd(stubServerBoot, stubClientBoot)
	root.SetContext(context.Background())

	p, err := Resolve(context.Background(), root, cfg, stubClientBoot)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.Source != SourceEnv {
		t.Errorf("source = %q, want env", p.Source)
	}
}

func TestResolve_TokenFileEnv(t *testing.T) {
	dir := t.TempDir()
	tokFile := filepath.Join(dir, "tok")
	if err := os.WriteFile(tokFile, []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ALT_TOKEN_FILE", tokFile)
	cfg := &config.Config{}

	root := NewRootCmd(stubServerBoot, stubClientBoot)
	root.SetContext(context.Background())

	p, err := Resolve(context.Background(), root, cfg, stubClientBoot)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.Source != SourceEnv {
		t.Errorf("source = %q, want env", p.Source)
	}
}
