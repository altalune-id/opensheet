package boot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/db"
	"altalune.id/template/logger"
	"altalune.id/template/scheduler"
	"altalune.id/template/worker"
)

var _ worker.Worker = (*scheduler.Runner)(nil)

func TestAssertSchedulerWiring(t *testing.T) {
	tests := []struct {
		name      string
		providers []scheduler.Provider
		wantErr   string
	}{
		{"all wired", []scheduler.Provider{stubProvider{}}, ""},
		{"only slot missing", []scheduler.Provider{nil}, "todo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := assertSchedulerWiring(tt.providers)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestAssertSchedulerWiring_LengthMismatchIsAnError(t *testing.T) {
	require.Error(t, assertSchedulerWiring([]scheduler.Provider{stubProvider{}, stubProvider{}}))
}

func TestSchedulerDomains_MatchesProviderCount(t *testing.T) {
	require.Len(t, schedulerDomains, 1, "add the new domain to schedulerDomains and schedulerProviders together")
}

func TestWarnUnusedTimezoneOverrides(t *testing.T) {
	tests := []struct {
		name      string
		jobs      map[string]config.SchedulerJobConfig
		wallClock map[string]bool
		want      string
	}{
		{
			name:      "unknown job",
			jobs:      map[string]config.SchedulerJobConfig{"ghost": {Timezone: "Asia/Jakarta"}},
			wallClock: map[string]bool{"real": true},
			want:      "unknown job",
		},
		{
			name:      "interval schedule",
			jobs:      map[string]config.SchedulerJobConfig{"interval-job": {Timezone: "Asia/Jakarta"}},
			wallClock: map[string]bool{"interval-job": false},
			want:      "interval schedule",
		},
		{
			name:      "wall clock job is silent",
			jobs:      map[string]config.SchedulerJobConfig{"sweep": {Timezone: "Asia/Jakarta"}},
			wallClock: map[string]bool{"sweep": true},
			want:      "",
		},
		{
			name:      "empty override is silent",
			jobs:      map[string]config.SchedulerJobConfig{"ghost": {}},
			wallClock: map[string]bool{},
			want:      "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
			cfg := &config.Config{Scheduler: config.SchedulerConfig{Jobs: tt.jobs}}

			warnUnusedTimezoneOverrides(cfg, tt.wallClock, log)

			if tt.want == "" {
				require.Empty(t, buf.String())
				return
			}
			require.Contains(t, buf.String(), tt.want)
		})
	}
}

func TestBootServer_RegistersEveryProvidersJobs(t *testing.T) {
	srv, err := BootServer(context.Background(), schedulerBootCfg(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, srv.Close()) })

	require.NotNil(t, srv.Scheduler)
	require.NotNil(t, srv.Health)

	names := make([]string, 0, len(schedulerDomains))
	for _, j := range srv.Scheduler.Jobs() {
		names = append(names, j.Name)
	}
	require.Equal(t, []string{"todo-autocomplete-stale"}, names,
		"the db health probe is a standalone worker, not a scheduler job")
}

func TestBootServer_SchedulerDisabledByOption(t *testing.T) {
	srv, err := BootServer(context.Background(), schedulerBootCfg(t), WithScheduler(false))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, srv.Close()) })

	require.Nil(t, srv.Scheduler)
}

func TestBootServer_SchedulerDisabledByConfig(t *testing.T) {
	cfg := schedulerBootCfg(t)
	cfg.Scheduler.Enabled = false
	srv, err := BootServer(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, srv.Close()) })

	require.Nil(t, srv.Scheduler)
}

func TestBootServer_ReadyzReflectsDBHealth(t *testing.T) {
	srv, err := BootServer(context.Background(), schedulerBootCfg(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, srv.Close()) })

	require.NotNil(t, srv.Health.Snapshot(), "boot must probe once so readiness is never unanswered")

	rec := httptest.NewRecorder()
	srv.Web.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusOK, rec.Code, "a healthy pool is ready from the first request")

	require.NoError(t, srv.Platform.Pool.W.Close())
	_ = srv.Health.Probe(context.Background())

	rec = httptest.NewRecorder()
	srv.Web.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code, "an unhealthy pool must fail readiness")
}

func TestBootServer_HealthzIgnoresDBHealth(t *testing.T) {
	srv, err := BootServer(context.Background(), schedulerBootCfg(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, srv.Close()) })

	rec := httptest.NewRecorder()
	srv.Web.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, http.StatusOK, rec.Code, "liveness must not depend on the database")
}

func TestBootServer_UnknownJobTimezoneOverrideStillBoots(t *testing.T) {
	cfg := schedulerBootCfg(t)
	cfg.Scheduler.Jobs = map[string]config.SchedulerJobConfig{"ghost": {Timezone: "Asia/Jakarta"}}
	srv, err := BootServer(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, srv.Close()) })

	require.NotNil(t, srv.Scheduler)
}

func TestBootServer_BadJobTimezoneFailsBoot(t *testing.T) {
	cfg := schedulerBootCfg(t)
	cfg.Scheduler.Jobs = map[string]config.SchedulerJobConfig{"ghost": {Timezone: "Mars/Olympus"}}
	_, err := BootServer(context.Background(), cfg)
	require.ErrorContains(t, err, "Mars/Olympus")
}

func TestHealthOnlyHandler_ServesProbesAndNothingElse(t *testing.T) {
	srv, err := BootServer(context.Background(), schedulerBootCfg(t), WithSchedulerOnly(true))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, srv.Close()) })
	require.NotNil(t, srv.Scheduler)

	h := healthOnlyHandler(srv.Cfg, srv.Health.Ready)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login", nil))
	require.Equal(t, http.StatusNotFound, rec.Code, "the scheduler-only listener serves no web handler")
}

func TestReadyz_IsDBAwareInEveryProcessShape(t *testing.T) {
	tests := []struct {
		name             string
		schedulerEnabled bool
		schedulerOnly    bool
		wantBootRejected bool
	}{
		{name: "scheduler on, full process", schedulerEnabled: true},
		{name: "scheduler off, full process"},
		{name: "scheduler on, scheduler-only", schedulerEnabled: true, schedulerOnly: true},
		{name: "scheduler off, scheduler-only", schedulerOnly: true, wantBootRejected: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := schedulerBootCfg(t)
			cfg.Scheduler.Enabled = tt.schedulerEnabled
			var opts []Option
			if tt.schedulerOnly {
				opts = append(opts, WithSchedulerOnly(true))
			}

			srv, err := BootServer(context.Background(), cfg, opts...)
			if tt.wantBootRejected {
				require.Nil(t, srv)
				require.ErrorContains(t, err, "--scheduler-only requires the scheduler")
				return
			}
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, srv.Close()) })

			h := srv.Web
			if tt.schedulerOnly {
				h = healthOnlyHandler(srv.Cfg, srv.Health.Ready)
			}

			require.NotNil(t, srv.Health.Snapshot(), "boot must probe once before serving")

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			require.Equal(t, http.StatusOK, rec.Code, "a healthy pool is ready from the first request")

			require.NoError(t, srv.Platform.Pool.W.Close())
			_ = srv.Health.Probe(context.Background())

			rec = httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			require.Equal(t, http.StatusServiceUnavailable, rec.Code,
				"readiness must track the DB regardless of whether the scheduler runs here")
		})
	}
}

func schedulerBootCfg(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	return &config.Config{
		Mode: config.ModeSelfhosted,
		HTTP: config.HTTPConfig{Addr: "127.0.0.1:0", BaseURL: "http://127.0.0.1"},
		DB: db.DBConfig{
			Driver:      db.DriverSQLite,
			DSN:         filepath.Join(dir, "scheduler.db"),
			TablePrefix: "altempl_",
			Schema:      "public",
			AutoMigrate: true,
			Health:      db.HealthConfig{Interval: 30 * time.Second, Timeout: 2 * time.Second},
		},
		Genesis: config.GenesisConfig{Email: "root@example.com", Password: "hunter2"},
		Tenant: config.TenantConfig{
			SingletonOrg:            config.SingletonOrgConfig{Slug: "default", Name: "Default"},
			PersonalOrgSlugFallback: "personal",
			PersonalProjectSlug:     "default",
		},
		Log:       logger.Config{Level: "error", Format: "json"},
		Mail:      config.MailConfig{Driver: "console", From: "no-reply@example.com"},
		Scheduler: config.SchedulerConfig{Enabled: true, Timezone: "UTC", ShutdownGrace: 5 * time.Second},
	}
}

type stubProvider struct{}

func (stubProvider) SchedulerJobs() []scheduler.Job { return nil }

func newReportedAppError(msg string) *apperror.AppError {
	return apperror.New(apperror.CodeUnexpectedError, msg, codes.Internal)
}

func TestReporterAdapter_Escalation(t *testing.T) {
	reported := newReportedAppError("already reported")
	tests := []struct {
		name         string
		cause        error
		wantEscalate bool
	}{
		{"raw error escalates", errors.New("boom"), true},
		{"wrapped raw error escalates", fmt.Errorf("job: %w", errors.New("boom")), true},
		{"nil cause escalates", nil, true},
		{"app error does not escalate", reported, false},
		{"wrapped app error does not escalate", fmt.Errorf("tenant a: %w", reported), false},
		{
			"joined app errors do not escalate",
			errors.Join(
				fmt.Errorf("tenant a: %w", reported),
				fmt.Errorf("tenant b: %w", newReportedAppError("also reported")),
			),
			false,
		},
		{
			"joined app errors with one raw error escalates",
			errors.Join(fmt.Errorf("tenant a: %w", reported), errors.New("tenant b: boom")),
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
			var calls int
			a := reporterAdapter{
				report: func(context.Context, string, error, ...any) *apperror.AppError {
					calls++
					return nil
				},
				log: log,
			}

			a.Report(t.Context(), "scheduler.job: sweep", tt.cause, slog.String("job", "sweep"))

			if tt.wantEscalate {
				require.Equal(t, 1, calls, "must escalate to the Reporter")
				return
			}
			require.Zero(t, calls, "must not escalate an already-reported error")
			require.Contains(t, buf.String(), "level=ERROR")
			require.Contains(t, buf.String(), "scheduler.job: sweep")
			require.Contains(t, buf.String(), `job=sweep`)
		})
	}
}
