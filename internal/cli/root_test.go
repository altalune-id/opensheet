package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"altalune.id/template/internal/boot"
	"altalune.id/template/internal/platform/config"
)

func stubServerBoot(_ context.Context, _ *config.Config, _ ...boot.Option) (*boot.Server, error) {
	return &boot.Server{}, nil
}

func stubClientBoot(_ context.Context, cfg *config.Config, _ string) (*boot.Client, error) {
	return &boot.Client{Cfg: cfg}, nil
}

func setSelfhostedEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	sessPath := filepath.Join(dir, "session.json")
	t.Setenv("ALT_MODE", "selfhosted")
	t.Setenv("ALT_DB_DRIVER", "sqlite")
	t.Setenv("ALT_DB_DSN", filepath.Join(dir, "alt.db"))
	t.Setenv("ALT_HTTP_ADDR", "127.0.0.1:0")
	t.Setenv("ALT_HTTP_BASEURL", "http://127.0.0.1")
	t.Setenv("ALT_GENESIS_EMAIL", "root@example.com")
	t.Setenv("ALT_GENESIS_PASSWORD", "hunter2")
	t.Setenv("ALT_SESSION_PATH", sessPath)
	t.Setenv("ALT_MAIL_DRIVER", "console")
	t.Setenv("ALT_MAIL_FROM", "no-reply@example.com")
	return sessPath
}

func TestRoot_VersionSubcommand(t *testing.T) {
	setSelfhostedEnv(t)
	root := NewRootCmd(stubServerBoot, stubClientBoot)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"--output", "text", "version"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(buf.String(), "altempl") {
		t.Errorf("expected altempl in output, got %q", buf.String())
	}
}

func TestRoot_PersistentFlagsRegistered(t *testing.T) {
	root := NewRootCmd(stubServerBoot, stubClientBoot)
	want := []string{"config", "token", "token-file", "output", "org", "project", "no-interactive", "log-level", "log-format"}
	for _, name := range want {
		if f := root.PersistentFlags().Lookup(name); f == nil {
			t.Errorf("persistent flag --%s must be registered", name)
		}
	}
	if f := root.PersistentFlags().ShorthandLookup("c"); f == nil {
		t.Fatal("--config must have -c shorthand")
	}
}

func TestRoot_UnknownSubcommandErrors(t *testing.T) {
	root := NewRootCmd(stubServerBoot, stubClientBoot)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"__nonexistent__"})
	if err := root.ExecuteContext(context.Background()); err == nil {
		t.Fatal("expected unknown command error")
	}
}

func TestRoot_KnownSubcommandsRegistered(t *testing.T) {
	root := NewRootCmd(stubServerBoot, stubClientBoot)
	want := map[string]bool{
		"version": false, "serve": false, "init": false, "migrate": false, "auth": false,
		"org": false, "project": false, "todo": false, "invite": false,
		"completion": false,
	}
	for _, c := range root.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("subcommand %q not registered on root", name)
		}
	}
}

func TestRoot_AuthSubcommandsRegistered(t *testing.T) {
	root := NewRootCmd(stubServerBoot, stubClientBoot)
	var authCmd *bytes.Buffer
	_ = authCmd
	// Locate the auth group and verify its children.
	names := map[string]bool{"login": false, "logout": false, "whoami": false, "token": false}
	for _, c := range root.Commands() {
		if c.Name() != "auth" {
			continue
		}
		for _, child := range c.Commands() {
			if _, ok := names[child.Name()]; ok {
				names[child.Name()] = true
			}
		}
	}
	for k, v := range names {
		if !v {
			t.Errorf("auth subcommand %q missing", k)
		}
	}
}

func TestRoot_ConfigLoadedViaPreRun(t *testing.T) {
	setSelfhostedEnv(t)
	root := NewRootCmd(stubServerBoot, stubClientBoot)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"version"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}
}
