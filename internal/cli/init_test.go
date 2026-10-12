package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/boot"
	"altalune.id/opensheet/internal/platform/config"
)

func TestInit_SucceedsAndSecondCallReportsAlreadyOnboarded(t *testing.T) {
	setSelfhostedEnv(t)
	t.Setenv("OPENSHEET_GENESIS_EMAIL", "")
	t.Setenv("OPENSHEET_GENESIS_PASSWORD", "")

	bootFn := func(ctx context.Context, cfg *config.Config, _ ...boot.Option) (*boot.Server, error) {
		return boot.BootServer(ctx, cfg)
	}

	root := NewRootCmd(bootFn, stubClientBoot)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{
		"init",
		"--email", "root@example.com",
		"--name", "Root",
		"--org-slug", "main",
		"--org-name", "Main",
		"--project-slug", "default",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(buf.String(), "onboarded") {
		t.Errorf("expected onboarded banner, got %q", buf.String())
	}

	root2 := NewRootCmd(bootFn, stubClientBoot)
	buf2 := &bytes.Buffer{}
	root2.SetOut(buf2)
	root2.SetErr(buf2)
	root2.SetArgs([]string{
		"init",
		"--email", "root@example.com",
		"--name", "Root",
		"--org-slug", "main",
		"--org-name", "Main",
		"--project-slug", "default",
	})
	err := root2.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected AlreadyOnboarded error on second call")
	}
	ae, ok := apperror.AsAppError(err)
	if !ok {
		t.Fatalf("want AppError, got %T: %v", err, err)
	}
	if ae.Code() != apperror.CodeOnboardingAlreadyDone {
		t.Fatalf("code=%q want %q", ae.Code(), apperror.CodeOnboardingAlreadyDone)
	}
	if code := ExitCodeFor(err); code != ExitAlreadyExists {
		t.Fatalf("exit=%d want %d", code, ExitAlreadyExists)
	}
}

func TestInit_CompletesOnboardingThroughTheServer(t *testing.T) {
	setSelfhostedEnv(t)
	t.Setenv("OPENSHEET_GENESIS_EMAIL", "")
	t.Setenv("OPENSHEET_GENESIS_PASSWORD", "")

	var calls int
	bootFn := func(ctx context.Context, cfg *config.Config, _ ...boot.Option) (*boot.Server, error) {
		srv, err := boot.BootServer(ctx, cfg)
		if err != nil {
			return nil, err
		}
		srv.CompleteOnboarding = func(context.Context) { calls++ }
		return srv, nil
	}

	root := NewRootCmd(bootFn, stubClientBoot)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"init", "--email", "root@example.com"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("init: %v", err)
	}
	if calls != 1 {
		t.Fatalf("CompleteOnboarding calls=%d want 1", calls)
	}
}

func TestInit_BlankOrgSlugGeneratesAndPrintsTheRealSlug(t *testing.T) {
	setSelfhostedEnv(t)
	t.Setenv("OPENSHEET_GENESIS_EMAIL", "")
	t.Setenv("OPENSHEET_GENESIS_PASSWORD", "")

	var booted *config.Config
	bootFn := func(ctx context.Context, cfg *config.Config, _ ...boot.Option) (*boot.Server, error) {
		booted = cfg
		return boot.BootServer(ctx, cfg)
	}
	root := NewRootCmd(bootFn, stubClientBoot)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"init", "--email", "root@example.com"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("init: %v", err)
	}

	srv, err := boot.BootServer(context.Background(), booted)
	if err != nil {
		t.Fatalf("reboot: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	o, err := srv.Orgs.SystemOrg(context.Background())
	if err != nil {
		t.Fatalf("init must create the system org even without --org-slug: %v", err)
	}
	if o.Slug == "default" || !strings.Contains(buf.String(), "org="+o.Slug) {
		t.Fatalf("want a generated slug printed as org=%s, got %q", o.Slug, buf.String())
	}
}
