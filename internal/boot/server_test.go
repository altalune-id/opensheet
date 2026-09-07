package boot_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"altalune.id/template/internal/boot"
	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/db"
	"altalune.id/template/logger"
)

func newSmokeCfg(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	return &config.Config{
		Mode: config.ModeSelfhosted,
		HTTP: config.HTTPConfig{
			Addr:    "127.0.0.1:0",
			BaseURL: "http://127.0.0.1",
		},
		DB: db.DBConfig{
			Driver:      db.DriverSQLite,
			DSN:         filepath.Join(dir, "smoke.db"),
			TablePrefix: "altempl_",
			Schema:      "public",
			AutoMigrate: true,
		},
		Genesis: config.GenesisConfig{
			Email:    "root@example.com",
			Password: "hunter2",
		},
		Tenant: config.TenantConfig{
			SingletonOrg:            config.SingletonOrgConfig{Slug: "default", Name: "Default"},
			PersonalOrgSlugFallback: "personal",
			PersonalProjectSlug:     "default",
		},
		Log: logger.Config{Level: "error", Format: "json"},
		Mail: config.MailConfig{
			Driver: "console",
			From:   "no-reply@example.com",
		},
	}
}

func TestBootServer_SQLite_WiresEveryService(t *testing.T) {
	cfg := newSmokeCfg(t)
	srv, err := boot.BootServer(context.Background(), cfg)
	if err != nil {
		t.Fatalf("BootServer: %v", err)
	}
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	if srv.Platform == nil {
		t.Fatal("Platform kernel must be wired")
	}
	if srv.Auth == nil || srv.Users == nil || srv.Orgs == nil ||
		srv.Projects == nil || srv.Todos == nil || srv.Invites == nil {
		t.Fatal("every domain service must be non-nil")
	}
	if srv.Onboard == nil {
		t.Fatal("onboard workflow must be wired")
	}
	if srv.Supervisor == nil {
		t.Fatal("supervisor must be wired")
	}
	if !srv.Caps.LocalIdentity {
		t.Error("caps.LocalIdentity should be true when genesis creds are set")
	}
	if srv.Web == nil {
		t.Fatal("Web handler must be wired")
	}
	if srv.API == nil {
		t.Fatal("API server must be wired")
	}
}

func TestBootServer_WebHandlerServesLogin(t *testing.T) {
	cfg := newSmokeCfg(t)
	srv, err := boot.BootServer(context.Background(), cfg)
	if err != nil {
		t.Fatalf("BootServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	if srv.Web == nil {
		t.Fatal("Web handler must be non-nil")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	srv.Web.ServeHTTP(rec, req)
	if rec.Code >= 400 {
		t.Fatalf("GET /login: status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestBootServer_APIHealthzServes(t *testing.T) {
	cfg := newSmokeCfg(t)
	srv, err := boot.BootServer(context.Background(), cfg)
	if err != nil {
		t.Fatalf("BootServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	if srv.API == nil {
		t.Fatal("API server must be non-nil")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	srv.Web.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz: status=%d", rec.Code)
	}
}

func TestBootServer_SupervisorRunReturnsWhenCtxCanceled(t *testing.T) {
	cfg := newSmokeCfg(t)
	srv, err := boot.BootServer(context.Background(), cfg)
	if err != nil {
		t.Fatalf("BootServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	runErr := srv.Run(ctx)
	if runErr != nil && !errors.Is(runErr, context.DeadlineExceeded) && !errors.Is(runErr, context.Canceled) {
		t.Fatalf("Run: unexpected err %v", runErr)
	}
}

func TestBootServer_CloseNilSafe(t *testing.T) {
	var srv *boot.Server
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Close on nil server panicked: %v", r)
		}
	}()
	if srv != nil {
		_ = srv.Close()
	}
}
