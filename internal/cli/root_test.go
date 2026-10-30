package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"altalune.id/opensheet/internal/boot"
	"altalune.id/opensheet/internal/platform/config"
)

func stubServerBoot(_ context.Context, _ *config.Config, _ ...boot.Option) (*boot.Server, error) {
	return &boot.Server{}, nil
}

func stubClientBoot(_ context.Context, cfg *config.Config, _, _ string) (*boot.Client, error) {
	return &boot.Client{Cfg: cfg}, nil
}

func setSelfhostedEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	sessPath := filepath.Join(dir, "session.json")
	t.Setenv("OPENSHEET_MODE", "selfhosted")
	t.Setenv("OPENSHEET_DB_DRIVER", "sqlite")
	t.Setenv("OPENSHEET_DB_DSN", filepath.Join(dir, "alt.db"))
	t.Setenv("OPENSHEET_HTTP_ADDR", "127.0.0.1:0")
	t.Setenv("OPENSHEET_HTTP_BASEURL", "http://127.0.0.1")
	t.Setenv("OPENSHEET_GENESIS_EMAIL", "root@example.com")
	t.Setenv("OPENSHEET_GENESIS_PASSWORD", "hunter2")
	t.Setenv("OPENSHEET_SESSION_PATH", sessPath)
	t.Setenv("OPENSHEET_MAIL_DRIVER", "console")
	t.Setenv("OPENSHEET_MAIL_FROM", "no-reply@example.com")
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
	if !strings.Contains(buf.String(), "opensheet") {
		t.Errorf("expected opensheet in output, got %q", buf.String())
	}
}

func TestRoot_PersistentFlagsRegistered(t *testing.T) {
	root := NewRootCmd(stubServerBoot, stubClientBoot)
	want := []string{"config", "url", "token", "token-file", "profile", "output", "org", "project", "no-interactive", "log-level", "log-format"}
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
		"org": false, "project": false, "invite": false,
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
	names := map[string]bool{"login": false, "logout": false, "whoami": false}
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

func TestRoot_OperatorCommandsRefuseRemoteFlags(t *testing.T) {
	setSelfhostedEnv(t)
	cases := [][]string{
		{"migrate", "status", "--url", "https://app.example.com"},
		{"org", "list", "--token", "tok"},
		{"project", "list", "--token-file", "/dev/null"},
		{"invite", "list", "--url", "https://app.example.com"},
		{"serve", "--token", "tok"},
		{"init", "--email", "a@b.co", "--url", "https://app.example.com"},
		{"scheduler", "list", "--url", "https://app.example.com"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			booted := false
			bootServer := func(context.Context, *config.Config, ...boot.Option) (*boot.Server, error) {
				booted = true
				return &boot.Server{}, nil
			}
			root := NewRootCmd(bootServer, stubClientBoot)
			root.SetOut(&bytes.Buffer{})
			root.SetErr(&bytes.Buffer{})
			root.SetArgs(args)
			err := root.ExecuteContext(context.Background())
			if !IsRemoteFlagError(err) {
				t.Fatalf("err = %v, want a RemoteFlagError", err)
			}
			if ExitCodeFor(err) != ExitInvalidArg {
				t.Fatalf("exit = %d, want %d", ExitCodeFor(err), ExitInvalidArg)
			}
			if booted {
				t.Fatal("the database was booted before the remote flag was refused")
			}
		})
	}
}

func TestRoot_OperatorCommandsKeepProfile(t *testing.T) {
	setSelfhostedEnv(t)
	root := NewRootCmd(stubServerBoot, stubClientBoot)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"--profile", "ops", "org", "list"})
	err := root.ExecuteContext(context.Background())
	if IsRemoteFlagError(err) {
		t.Fatalf("--profile picks the local sign-in and must stay allowed: %v", err)
	}
	if !IsNotSignedInError(err) {
		t.Fatalf("err = %v, want NotSignedInError for the empty ops profile", err)
	}
}
