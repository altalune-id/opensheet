package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"altalune.id/opensheet/internal/platform/config"
)

func parsedRoot(t *testing.T, cfg *config.Config, args ...string) *cobra.Command {
	t.Helper()
	root := NewRootCmd(stubServerBoot, stubClientBoot)
	if err := root.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	root.SetContext(ctxWithConfig(context.Background(), cfg))
	return root
}

func clearCLIEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"url", "token", "profile"} {
		t.Setenv(config.EnvVar(k), "")
	}
}

func TestResolveToken_Precedence(t *testing.T) {
	file := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(file, []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		args    []string
		env     string
		want    string
		wantErr bool
	}{
		{name: "flag beats file and env", args: []string{"--token", "flag-token", "--token-file", file}, env: "env-token", want: "flag-token"},
		{name: "file beats env", args: []string{"--token-file", file}, env: "env-token", want: "file-token"},
		{name: "env alone", env: "env-token", want: "env-token"},
		{name: "nothing falls through to the profile", want: ""},
		{name: "an empty file is an error", args: []string{"--token-file", empty}, wantErr: true},
		{name: "a missing file is an error", args: []string{"--token-file", filepath.Join(t.TempDir(), "nope")}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearCLIEnv(t)
			t.Setenv(config.EnvVar("token"), tt.env)
			got, err := resolveToken(parsedRoot(t, &config.Config{}, tt.args...))
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("resolveToken = %q, %v; want %q (err %v)", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestProfileName_Precedence(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  string
		want string
	}{
		{name: "flag beats env", args: []string{"--profile", "work"}, env: "ci", want: "work"},
		{name: "env alone", env: "ci", want: "ci"},
		{name: "blank flag falls back to env", args: []string{"--profile", " "}, env: "ci", want: "ci"},
		{name: "default", want: defaultProfile},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearCLIEnv(t)
			t.Setenv(config.EnvVar("profile"), tt.env)
			if got := profileName(parsedRoot(t, &config.Config{}, tt.args...)); got != tt.want {
				t.Fatalf("profileName = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveURL_Precedence(t *testing.T) {
	session := config.SessionConfig{Path: filepath.Join(t.TempDir(), "session.json")}
	cfg := &config.Config{HTTP: config.HTTPConfig{BaseURL: "https://cfg.example"}, Session: session}
	saved := &profile{URL: "https://profile.example"}
	tests := []struct {
		name    string
		args    []string
		env     string
		profile *profile
		cfg     *config.Config
		want    string
	}{
		{name: "flag beats everything", args: []string{"--url", "https://flag.example"}, env: "https://env.example", profile: saved, cfg: cfg, want: "https://flag.example"},
		{name: "env beats the profile", env: "https://env.example", profile: saved, cfg: cfg, want: "https://env.example"},
		{name: "the profile beats config", profile: saved, cfg: cfg, want: "https://profile.example"},
		{name: "a local profile falls back to a config named with -c", args: []string{"-c", "opensheet.yaml"}, profile: &profile{}, cfg: cfg, want: "https://cfg.example"},
		{name: "a discovered config never picks the server", profile: &profile{}, cfg: cfg, want: ""},
		{name: "nothing set", cfg: &config.Config{Session: session}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearCLIEnv(t)
			t.Setenv(config.EnvVar("url"), tt.env)
			in, err := invocationFrom(parsedRoot(t, tt.cfg, tt.args...))
			if err != nil {
				t.Fatal(err)
			}
			if got := in.baseURL(tt.profile); got != tt.want {
				t.Fatalf("baseURL = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveLocal_UsesOnlyALocalProfile(t *testing.T) {
	clearCLIEnv(t)
	path := filepath.Join(t.TempDir(), "session.json")
	cfg := &config.Config{Session: config.SessionConfig{Path: path}}
	uid, oid, pid := uuid.New(), uuid.New(), uuid.New()
	writeRemoteProfile(t, path, defaultProfile, profile{Principal: principalView{
		Source: "genesis", UserID: uid.String(), Email: "z@z", ActiveOrgID: oid.String(), ActiveProjectID: pid.String(),
	}})
	writeRemoteProfile(t, path, "prod", profile{URL: "https://app.example.com", Principal: principalView{UserID: uuid.NewString()}})

	p, err := resolveLocal(parsedRoot(t, cfg))
	if err != nil {
		t.Fatalf("resolveLocal: %v", err)
	}
	if p.UserID != uid || p.OrgID != oid || p.ProjectID != pid || p.Source != "genesis" {
		t.Fatalf("principal = %+v", p)
	}

	if _, err := resolveLocal(parsedRoot(t, cfg, "--profile", "prod")); !IsNotSignedInError(err) {
		t.Fatalf("a remote profile's ids belong to another server; err = %v", err)
	}
	if _, err := resolveLocal(parsedRoot(t, cfg, "--profile", "nobody")); !IsNotSignedInError(err) {
		t.Fatalf("err = %v, want NotSignedInError", err)
	}
}
