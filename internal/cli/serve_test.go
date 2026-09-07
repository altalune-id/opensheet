package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"altalune.id/template/internal/boot"
	"altalune.id/template/internal/platform/config"
	"altalune.id/template/worker"
)

func TestServe_ExitsWhenSupervisorReturns(t *testing.T) {
	setSelfhostedEnv(t)

	bootFn := func(_ context.Context, cfg *config.Config, _ ...boot.Option) (*boot.Server, error) {
		return &boot.Server{
			Cfg:        cfg,
			Supervisor: worker.New(nil),
		}, nil
	}

	root := NewRootCmd(bootFn, stubClientBoot)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"serve"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := root.ExecuteContext(ctx); err != nil {
		t.Fatalf("serve: %v", err)
	}
	if !strings.Contains(buf.String(), "listening on") {
		t.Errorf("expected listening banner, got %q", buf.String())
	}
}
