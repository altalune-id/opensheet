package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/boot"
	"altalune.id/template/internal/platform/config"
)

func TestInit_SucceedsAndSecondCallReportsAlreadyOnboarded(t *testing.T) {
	setSelfhostedEnv(t)
	t.Setenv("ALT_GENESIS_EMAIL", "")
	t.Setenv("ALT_GENESIS_PASSWORD", "")

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
