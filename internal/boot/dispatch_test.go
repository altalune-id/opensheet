package boot_test

import (
	"context"
	"slices"
	"testing"

	"altalune.id/opensheet/internal/boot"
	"altalune.id/opensheet/internal/platform/outbox"
)

type noopDeliverer struct{}

func (noopDeliverer) Deliver(context.Context, outbox.Entry) error { return nil }

func TestBootServer_OutboxIsOnTheKernel(t *testing.T) {
	srv, err := boot.BootServer(context.Background(), newSmokeCfg(t), boot.WithScheduler(false))
	if err != nil {
		t.Fatalf("BootServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	if srv.Platform.Outbox == nil {
		t.Fatal("Kernel.Outbox is nil; the durable outbox is unreachable from the composition root")
	}
}

func TestBootServer_DispatchWorkerIsAlwaysRegistered(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []boot.Option
	}{
		{name: "webhook-deliverer", opts: []boot.Option{boot.WithScheduler(false)}},
		{
			name: "injected-deliverer",
			opts: []boot.Option{boot.WithScheduler(false), boot.WithDispatch(noopDeliverer{}, outbox.WorkerOpts{})},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, err := boot.BootServer(context.Background(), newSmokeCfg(t), tc.opts...)
			if err != nil {
				t.Fatalf("BootServer: %v", err)
			}
			t.Cleanup(func() { _ = srv.Close() })

			var names []string
			for _, w := range srv.Supervisor.Workers() {
				names = append(names, w.Name())
			}
			if !slices.Contains(names, outbox.WorkerName) {
				t.Fatalf("supervisor workers = %v, want %q registered", names, outbox.WorkerName)
			}
		})
	}
}
