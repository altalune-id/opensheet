package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"altalune.id/opensheet/internal/boot"
	"altalune.id/opensheet/internal/controlplane"
	"altalune.id/opensheet/internal/platform/config"
)

func runCLI(t *testing.T, bootClient ClientBootFn, args ...string) (string, error) {
	t.Helper()
	root := NewRootCmd(stubServerBoot, bootClient)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return buf.String(), err
}

func TestWhoami_NotSignedIn(t *testing.T) {
	setSelfhostedEnv(t)
	clearCLIEnv(t)
	_, err := runCLI(t, stubClientBoot, "auth", "whoami")
	if !IsNotSignedInError(err) || ExitCodeFor(err) != ExitUnauth {
		t.Fatalf("err = %v (exit %d), want NotSignedInError exiting 2", err, ExitCodeFor(err))
	}
}

func TestWhoami_AfterLogoutWithNoServerURLIsNotSignedIn(t *testing.T) {
	sessPath := setSelfhostedEnv(t)
	clearCLIEnv(t)
	t.Setenv("OPENSHEET_HTTP_BASEURL", "")
	writeRemoteProfile(t, sessPath, "prod", profile{URL: "https://app.example.com"})
	if _, err := runCLI(t, stubClientBoot, "--profile", "prod", "auth", "logout"); err != nil {
		t.Fatalf("logout: %v", err)
	}

	_, err := runCLI(t, stubClientBoot, "--profile", "prod", "auth", "whoami")
	if !IsNotSignedInError(err) || ExitCodeFor(err) != ExitUnauth {
		t.Fatalf("err = %v (exit %d), want NotSignedInError exiting 2", err, ExitCodeFor(err))
	}
}

func TestWhoami_LocalProfilePrintsTheStoredPrincipal(t *testing.T) {
	sessPath := setSelfhostedEnv(t)
	clearCLIEnv(t)
	writeRemoteProfile(t, sessPath, defaultProfile, profile{Principal: principalView{Source: "genesis", Email: "who@here"}})

	out, err := runCLI(t, stubClientBoot, "--output", "json", "auth", "whoami")
	if err != nil {
		t.Fatalf("whoami: %v", err)
	}
	if !strings.Contains(out, `"data"`) || !strings.Contains(out, "who@here") || !strings.Contains(out, `"source": "genesis"`) {
		t.Fatalf("output = %q", out)
	}
}

func TestWhoami_RemoteProfileAsksTheServerAndRefreshesThePrincipal(t *testing.T) {
	sessPath := setSelfhostedEnv(t)
	clearCLIEnv(t)
	f := newRemoteFixture(t, testCLIClient)
	f.store = profileStore{path: sessPath}
	p := f.signedIn(t, "prod")
	p.Principal = principalView{Email: "stale@example.com"}
	writeRemoteProfile(t, sessPath, "prod", p)

	out, err := runCLI(t, boot.BootClient, "--profile", "prod", "--output", "text", "auth", "whoami")
	if err != nil {
		t.Fatalf("whoami: %v (%s)", err, out)
	}
	if !strings.Contains(out, testEmail) || !strings.Contains(out, f.app.projectID) || !strings.Contains(out, "prod") {
		t.Fatalf("output = %q", out)
	}
	if !strings.Contains(out, "source:     token") {
		t.Fatalf("output = %q, want the server's principal source", out)
	}
	if got := f.profile(t, "prod").Principal.Email; got != testEmail {
		t.Fatalf("stored principal email = %q, want whoami's", got)
	}
}

func TestWhoami_ExplicitTokenLeavesProfilesAlone(t *testing.T) {
	sessPath := setSelfhostedEnv(t)
	clearCLIEnv(t)
	f := newRemoteFixture(t, testCLIClient)
	f.store = profileStore{path: sessPath}
	p := f.signedIn(t, defaultProfile)
	p.Principal = principalView{Email: "stale@example.com"}
	writeRemoteProfile(t, sessPath, defaultProfile, p)

	out, err := runCLI(t, boot.BootClient, "--url", f.app.URL, "--token", p.Token.Access, "--output", "json", "auth", "whoami")
	if err != nil {
		t.Fatalf("whoami: %v (%s)", err, out)
	}
	if !strings.Contains(out, testEmail) {
		t.Fatalf("output = %q", out)
	}
	if got := f.profile(t, defaultProfile).Principal.Email; got != "stale@example.com" {
		t.Fatalf("an explicit token rewrote the profile: %q", got)
	}

	_, err = runCLI(t, boot.BootClient, "--url", f.app.URL, "--token", "not-a-token", "auth", "whoami")
	if ExitCodeFor(err) != ExitUnauth || !strings.Contains(err.Error(), "auth login") {
		t.Fatalf("a rejected token: err = %v (exit %d), want exit 2 with a sign-in hint", err, ExitCodeFor(err))
	}
}

func TestLogin_RefusesATokenAndLocalFlagsWithURL(t *testing.T) {
	setSelfhostedEnv(t)
	clearCLIEnv(t)
	for _, args := range [][]string{
		{"auth", "login", "--token", "tok"},
		{"auth", "login", "--url", "https://app.example.com", "--admin"},
		{"auth", "login", "--url", "https://app.example.com", "--email", "a@b.co"},
	} {
		if _, err := runCLI(t, stubClientBoot, args...); ExitCodeFor(err) != ExitInvalidArg {
			t.Fatalf("%v: err = %v, want exit %d", args, err, ExitInvalidArg)
		}
	}
}

func TestLogout_CommandRemovesALocalProfile(t *testing.T) {
	sessPath := setSelfhostedEnv(t)
	clearCLIEnv(t)
	writeRemoteProfile(t, sessPath, defaultProfile, profile{Principal: principalView{Email: "z@z"}})

	out, err := runCLI(t, stubClientBoot, "auth", "logout")
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if !strings.Contains(out, "Signed out (profile default)") {
		t.Fatalf("output = %q", out)
	}
	if p, _ := (profileStore{path: sessPath}).load(defaultProfile); p != nil {
		t.Fatal("the profile survived logout")
	}
}

func TestLogin_LocalSavesALocalProfileTheInProcessCommandsUse(t *testing.T) {
	sessPath := setSelfhostedEnv(t)
	clearCLIEnv(t)
	bootFn := func(ctx context.Context, cfg *config.Config, _ ...boot.Option) (*boot.Server, error) {
		return boot.BootServer(ctx, cfg)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString("hunter2\n"); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	stdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = stdin })

	root := NewRootCmd(bootFn, stubClientBoot)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"--profile", "ops", "auth", "login", "--email", "root@example.com", "--password-stdin"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("login: %v (%s)", err, buf.String())
	}
	if !strings.Contains(buf.String(), "profile ops") {
		t.Fatalf("output = %q", buf.String())
	}
	p, err := (profileStore{path: sessPath}).load("ops")
	if err != nil || p == nil || p.URL != "" || p.Token != nil || p.Principal.Email != "root@example.com" || p.Principal.UserID == "" {
		t.Fatalf("local profile = %+v, %v", p, err)
	}

	root = NewRootCmd(bootFn, stubClientBoot)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"--profile", "ops", "--output", "json", "org", "list"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("org list as the local profile: %v (%s)", err, buf.String())
	}
}

// SECURITY: an http.baseURL from a config file found in the working directory never decides where a token goes.
func TestRemoteCommands_IgnoreADiscoveredConfigBaseURL(t *testing.T) {
	setSelfhostedEnv(t)
	t.Setenv("OPENSHEET_HTTP_BASEURL", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "opensheet.yaml"), []byte("http:\n  baseURL: \"https://attacker.example\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	var dialed string
	bootClient := func(_ context.Context, cfg *config.Config, baseURL, token string) (*boot.Client, error) {
		dialed = baseURL
		return &boot.Client{Cfg: cfg, Conn: controlplane.NewClient(baseURL, token)}, nil
	}

	_, err := runCLI(t, bootClient, "--token", "not-a-real-token", "auth", "whoami")
	if !IsMissingURLError(err) {
		t.Fatalf("err = %v, want MissingURLError: a discovered config file must not pick the server", err)
	}
	if dialed != "" {
		t.Fatalf("the token was sent to %q", dialed)
	}
}
