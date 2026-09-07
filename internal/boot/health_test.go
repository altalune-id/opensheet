package boot_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"altalune.id/template/internal/boot"
)

func statusOf(t *testing.T, h http.Handler, path string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code
}

func TestBootServer_SchedulerEnabled_SnapshotExistsBeforeFirstTick(t *testing.T) {
	cfg := newSmokeCfg(t)
	cfg.Scheduler.Enabled = true

	srv, err := boot.BootServer(context.Background(), cfg)
	if err != nil {
		t.Fatalf("BootServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	if srv.Scheduler == nil {
		t.Fatal("scheduler must be wired when scheduler.enabled=true")
	}
	if srv.Health.Snapshot() == nil {
		t.Fatal("boot must publish a health snapshot before returning")
	}
	if got := statusOf(t, srv.Web, "/readyz"); got != http.StatusOK {
		t.Fatalf("GET /readyz before any tick: status=%d, want 200", got)
	}
}

func TestBootServer_SchedulerEnabled_ReadyzTracksSnapshot(t *testing.T) {
	cfg := newSmokeCfg(t)
	cfg.Scheduler.Enabled = true

	srv, err := boot.BootServer(context.Background(), cfg)
	if err != nil {
		t.Fatalf("BootServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	_ = srv.Platform.Pool.W.Close()
	_ = srv.Health.Probe(context.Background())

	if got := statusOf(t, srv.Web, "/readyz"); got != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz with an unhealthy pool: status=%d, want 503", got)
	}
}

func TestBootServer_SchedulerDisabled_ReadyzStillTracksSnapshot(t *testing.T) {
	cfg := newSmokeCfg(t)
	cfg.Scheduler.Enabled = true

	srv, err := boot.BootServer(context.Background(), cfg, boot.WithScheduler(false))
	if err != nil {
		t.Fatalf("BootServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	if srv.Scheduler != nil {
		t.Fatal("WithScheduler(false) must not wire a runner")
	}
	if got := statusOf(t, srv.Web, "/readyz"); got != http.StatusOK {
		t.Fatalf("GET /readyz with a healthy pool: status=%d, want 200", got)
	}

	_ = srv.Platform.Pool.W.Close()
	_ = srv.Health.Probe(context.Background())

	if got := statusOf(t, srv.Web, "/readyz"); got != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz on a no-scheduler replica: status=%d, want 503 — the db-health worker runs regardless of the scheduler", got)
	}
}

func TestBootServer_HealthWorkerIsRegisteredRegardlessOfScheduler(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []boot.Option
	}{
		{name: "scheduler-on"},
		{name: "scheduler-off", opts: []boot.Option{boot.WithScheduler(false)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newSmokeCfg(t)
			cfg.Scheduler.Enabled = true

			srv, err := boot.BootServer(context.Background(), cfg, tc.opts...)
			if err != nil {
				t.Fatalf("BootServer: %v", err)
			}
			t.Cleanup(func() { _ = srv.Close() })

			var names []string
			for _, w := range srv.Supervisor.Workers() {
				names = append(names, w.Name())
			}
			if !slices.Contains(names, "db-health") {
				t.Fatalf("supervisor workers = %v, want a db-health worker", names)
			}
		})
	}
}

func TestBootServer_HealthzIsDBIndependent(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []boot.Option
	}{
		{name: "scheduler-on"},
		{name: "scheduler-off", opts: []boot.Option{boot.WithScheduler(false)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newSmokeCfg(t)
			cfg.Scheduler.Enabled = true

			srv, err := boot.BootServer(context.Background(), cfg, tc.opts...)
			if err != nil {
				t.Fatalf("BootServer: %v", err)
			}
			t.Cleanup(func() { _ = srv.Close() })

			_ = srv.Platform.Pool.W.Close()
			_ = srv.Health.Probe(context.Background())

			if got := statusOf(t, srv.Web, "/healthz"); got != http.StatusOK {
				t.Fatalf("GET /healthz with a dead pool: status=%d, want 200", got)
			}
		})
	}
}

func TestBootServer_SchedulerOnlyWithSchedulerDisabled_IsRejected(t *testing.T) {
	tests := []struct {
		name       string
		enabled    bool
		opts       []boot.Option
		wantSubstr string
	}{
		{
			name:       "disabled by config",
			enabled:    false,
			opts:       []boot.Option{boot.WithSchedulerOnly(true)},
			wantSubstr: "scheduler.enabled=false",
		},
		{
			name:       "disabled by option",
			enabled:    true,
			opts:       []boot.Option{boot.WithSchedulerOnly(true), boot.WithScheduler(false)},
			wantSubstr: "WithScheduler(false)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newSmokeCfg(t)
			cfg.Scheduler.Enabled = tc.enabled

			srv, err := boot.BootServer(context.Background(), cfg, tc.opts...)
			if srv != nil {
				t.Cleanup(func() { _ = srv.Close() })
				t.Fatal("a scheduler-only process with no scheduler must not boot")
			}
			if err == nil {
				t.Fatal("BootServer must reject --scheduler-only with a disabled scheduler")
			}
			if !strings.Contains(err.Error(), "--scheduler-only") ||
				!strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("error must name both inputs, got: %v", err)
			}
		})
	}
}
