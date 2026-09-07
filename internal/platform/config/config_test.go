package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/platform/db"
)

func TestLoad_DefaultsPopulate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	cfg, err := Load("", withCwdOverride(t, dir), withGenesisFallback(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Mode != ModeSelfhosted {
		t.Fatalf("mode: want %q, got %q", ModeSelfhosted, cfg.Mode)
	}
	if cfg.HTTP.Addr != ":5150" {
		t.Fatalf("http.addr: want :5150, got %q", cfg.HTTP.Addr)
	}
	if cfg.API.Enabled != true {
		t.Fatalf("api.enabled: want true, got %v", cfg.API.Enabled)
	}
	if cfg.Log.Level != "info" || cfg.Log.Format != "json" {
		t.Fatalf("log defaults wrong: %+v", cfg.Log)
	}
	if !cfg.DB.AutoMigrate {
		t.Fatalf("db.autoMigrate: want true, got false")
	}
}

func TestLoad_YAMLThenEnvOverride(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	yaml := `
mode: selfhosted
http:
  addr: ":6000"
  baseURL: "http://localhost:6000"
genesis:
  email: "root@example.com"
  password: "correct-horse"
`
	path := writeTempYAML(t, "cfg.yaml", yaml)

	t.Setenv("ALT_HTTP_ADDR", ":8080")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTP.Addr != ":8080" {
		t.Fatalf("env should win: got %q", cfg.HTTP.Addr)
	}
	if cfg.HTTP.BaseURL != "http://localhost:6000" {
		t.Fatalf("yaml value lost: %q", cfg.HTTP.BaseURL)
	}
	if cfg.Genesis.Email != "root@example.com" {
		t.Fatalf("genesis.email: %q", cfg.Genesis.Email)
	}
}

func TestLoad_ValidateCascades(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	yaml := `
mode: selfhosted
genesis:
  email: "root@example.com"
  password: "x"
log:
  level: "shout"
`
	path := writeTempYAML(t, "cfg.yaml", yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), "logger:") {
		t.Fatalf("expected logger validation error, got %v", err)
	}
}

func TestValidate_ModeInvariants(t *testing.T) {
	base := func() *Config {
		c := &Config{
			Mode:    ModeSelfhosted,
			DB:      validDB(),
			Genesis: GenesisConfig{Email: "root@example.com", Password: "x"},
		}
		c.Tenant.SingletonOrg.Slug = "default"
		c.Tenant.SingletonOrg.Name = "Default Organization"
		return c
	}

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantSub string
	}{
		{
			name:    "unset mode fails",
			mutate:  func(c *Config) { c.Mode = "" },
			wantSub: "Config.Mode",
		},
		{
			name:    "unknown mode fails",
			mutate:  func(c *Config) { c.Mode = "hybrid" },
			wantSub: "oneof",
		},
		{
			name: "selfhosted without genesis or oidc is allowed (onboarding-first)",
			mutate: func(c *Config) {
				c.Genesis = GenesisConfig{}
				c.OIDC = OIDCConfig{}
			},
			wantSub: "",
		},
		{
			name: "cloud without oidc fails",
			mutate: func(c *Config) {
				c.Mode = ModeCloud
				c.Genesis = GenesisConfig{}
				c.OIDC = OIDCConfig{}
			},
			wantSub: "cloud requires oidc.issuer",
		},
		{
			name: "cloud without oidc clientID fails",
			mutate: func(c *Config) {
				c.Mode = ModeCloud
				c.Genesis = GenesisConfig{}
				c.OIDC = OIDCConfig{Issuer: "https://iss.example.com"}
			},
			wantSub: "cloud requires oidc.clientID",
		},
		{
			name: "cloud with sqlite driver fails",
			mutate: func(c *Config) {
				c.Mode = ModeCloud
				c.Genesis = GenesisConfig{}
				c.OIDC = OIDCConfig{Issuer: "https://iss.example.com", ClientID: "c", ClientSecret: "s"}
				c.DB.Driver = "sqlite"
			},
			wantSub: "cloud requires db.driver=postgres",
		},
		{
			name: "cloud with full oidc + postgres + genesis + singleton org (no break-glass) is allowed",
			mutate: func(c *Config) {
				c.Mode = ModeCloud
				c.OIDC = OIDCConfig{Issuer: "https://iss.example.com", ClientID: "c", ClientSecret: "s"}
				c.DB.Driver = "postgres"
				c.Genesis = GenesisConfig{Email: "root@example.com", Password: "s3cret", BreakGlass: true}
			},
			wantSub: "",
		},
		{
			name: "cloud with break-glass and genesis password is allowed",
			mutate: func(c *Config) {
				c.Mode = ModeCloud
				c.OIDC = OIDCConfig{Issuer: "https://iss.example.com", ClientID: "c", ClientSecret: "s"}
				c.DB.Driver = "postgres"
				c.Genesis = GenesisConfig{Email: "root@example.com", Password: "s3cret", BreakGlass: true}
			},
			wantSub: "",
		},
		{
			name: "cloud with email only (no password, no break-glass) is allowed",
			mutate: func(c *Config) {
				c.Mode = ModeCloud
				c.OIDC = OIDCConfig{Issuer: "https://iss.example.com", ClientID: "c", ClientSecret: "s"}
				c.DB.Driver = "postgres"
				c.Genesis = GenesisConfig{Email: "root@example.com"}
			},
			wantSub: "",
		},
		{
			name: "cloud with password but no break-glass fails",
			mutate: func(c *Config) {
				c.Mode = ModeCloud
				c.OIDC = OIDCConfig{Issuer: "https://iss.example.com", ClientID: "c", ClientSecret: "s"}
				c.DB.Driver = "postgres"
				c.Genesis = GenesisConfig{Email: "root@example.com", Password: "s3cret"}
			},
			wantSub: "requires genesis.breakGlass",
		},
		{
			name: "cloud without genesis email fails",
			mutate: func(c *Config) {
				c.Mode = ModeCloud
				c.OIDC = OIDCConfig{Issuer: "https://iss.example.com", ClientID: "c", ClientSecret: "s"}
				c.DB.Driver = "postgres"
				c.Genesis = GenesisConfig{}
			},
			wantSub: "requires genesis.email",
		},
		{
			name: "cloud without singleton org slug fails",
			mutate: func(c *Config) {
				c.Mode = ModeCloud
				c.OIDC = OIDCConfig{Issuer: "https://iss.example.com", ClientID: "c", ClientSecret: "s"}
				c.DB.Driver = "postgres"
				c.Genesis = GenesisConfig{Email: "root@example.com", Password: "s3cret", BreakGlass: true}
				c.Tenant.SingletonOrg.Slug = ""
			},
			wantSub: "singletonOrg.slug",
		},
		{
			name: "cloud without singleton org name fails",
			mutate: func(c *Config) {
				c.Mode = ModeCloud
				c.OIDC = OIDCConfig{Issuer: "https://iss.example.com", ClientID: "c", ClientSecret: "s"}
				c.DB.Driver = "postgres"
				c.Genesis = GenesisConfig{Email: "root@example.com", Password: "s3cret", BreakGlass: true}
				c.Tenant.SingletonOrg.Name = ""
			},
			wantSub: "singletonOrg.name",
		},
		{
			name: "selfhosted with genesis email alone is allowed",
			mutate: func(c *Config) {
				c.Genesis = GenesisConfig{Email: "root@example.com"}
			},
			wantSub: "",
		},
		{
			name: "selfhosted with genesis password alone fails",
			mutate: func(c *Config) {
				c.Genesis = GenesisConfig{Password: "x"}
			},
			wantSub: "genesis.password without genesis.email",
		},
		{
			name: "cloud with genesis password alone fails",
			mutate: func(c *Config) {
				c.Mode = ModeCloud
				c.OIDC = OIDCConfig{Issuer: "https://iss.example.com", ClientID: "c", ClientSecret: "s"}
				c.DB.Driver = "postgres"
				c.Genesis = GenesisConfig{Password: "x", BreakGlass: true}
			},
			wantSub: "genesis.password without genesis.email",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			tc.mutate(c)
			err := c.Validate()
			if tc.wantSub == "" {
				if err != nil {
					t.Fatalf("want nil, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("want error containing %q, got %v", tc.wantSub, err)
			}
		})
	}
}

func TestSchedulerConfig_GetTimezone(t *testing.T) {
	tests := []struct {
		name    string
		tz      string
		want    string
		wantErr bool
	}{
		{"empty defaults to UTC", "", "UTC", false},
		{"named zone", "Asia/Jakarta", "Asia/Jakarta", false},
		{"invalid zone errors", "Mars/Olympus", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &SchedulerConfig{Timezone: tt.tz}
			loc, err := c.GetTimezone()
			if (err != nil) != tt.wantErr {
				t.Fatalf("GetTimezone() err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if loc.String() != tt.want {
				t.Errorf("loc = %q, want %q", loc.String(), tt.want)
			}
		})
	}
}

func TestDefaults_SchedulerAndDBKeys(t *testing.T) {
	cfg := Defaults()
	if !cfg.Scheduler.Enabled {
		t.Error("scheduler.enabled must default true")
	}
	if cfg.Scheduler.Timezone != "UTC" {
		t.Errorf("scheduler.timezone = %q, want UTC", cfg.Scheduler.Timezone)
	}
	if cfg.Scheduler.ShutdownGrace != 30*time.Second {
		t.Errorf("scheduler.shutdownGrace = %s, want 30s", cfg.Scheduler.ShutdownGrace)
	}
	if cfg.DB.ConnectTimeout != 30*time.Second {
		t.Errorf("db.connectTimeout = %s, want 30s", cfg.DB.ConnectTimeout)
	}
	if cfg.DB.ConnectBackoff != 250*time.Millisecond {
		t.Errorf("db.connectBackoff = %s, want 250ms", cfg.DB.ConnectBackoff)
	}
	if cfg.DB.Health.Interval != 30*time.Second {
		t.Errorf("db.health.interval = %s, want 30s", cfg.DB.Health.Interval)
	}
	if cfg.DB.Health.Timeout != 2*time.Second {
		t.Errorf("db.health.timeout = %s, want 2s", cfg.DB.Health.Timeout)
	}
}

func TestMode_IsProduction(t *testing.T) {
	if !ModeCloud.IsProduction() {
		t.Fatal("cloud must be production")
	}
	if ModeSelfhosted.IsProduction() {
		t.Fatal("selfhosted must not be production")
	}
}

func TestLoad_RequireFile_Missing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	missing := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	_, err := Load(missing, WithRequireFile())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "required") && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected required-file error, got %v", err)
	}
}

func writeTempYAML(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	return p
}

func validDB() db.DBConfig {
	return db.DBConfig{Driver: db.DriverSQLite, DSN: ":memory:"}
}

func withCwdOverride(t *testing.T, dir string) Option {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	return func(*loadOptions) {}
}

func withGenesisFallback(t *testing.T) Option {
	t.Helper()
	t.Setenv("ALT_GENESIS_EMAIL", "root@example.com")
	t.Setenv("ALT_GENESIS_PASSWORD", "x")
	return func(*loadOptions) {}
}

func TestSchedulerConfig_Locations(t *testing.T) {
	tests := []struct {
		name     string
		cfg      SchedulerConfig
		job      string
		wantZone string
	}{
		{
			name:     "unset global falls back to UTC",
			cfg:      SchedulerConfig{},
			job:      "any-job",
			wantZone: "UTC",
		},
		{
			name:     "global applies to every job",
			cfg:      SchedulerConfig{Timezone: "Asia/Jakarta"},
			job:      "any-job",
			wantZone: "Asia/Jakarta",
		},
		{
			name: "per-job overrides global",
			cfg: SchedulerConfig{
				Timezone: "Asia/Jakarta",
				Jobs:     map[string]SchedulerJobConfig{"todo-autocomplete-stale": {Timezone: "Europe/Berlin"}},
			},
			job:      "todo-autocomplete-stale",
			wantZone: "Europe/Berlin",
		},
		{
			name: "unlisted job still gets the global",
			cfg: SchedulerConfig{
				Timezone: "Asia/Jakarta",
				Jobs:     map[string]SchedulerJobConfig{"other-job": {Timezone: "Europe/Berlin"}},
			},
			job:      "todo-autocomplete-stale",
			wantZone: "Asia/Jakarta",
		},
		{
			name: "empty per-job timezone falls back to global",
			cfg: SchedulerConfig{
				Timezone: "Asia/Jakarta",
				Jobs:     map[string]SchedulerJobConfig{"todo-autocomplete-stale": {}},
			},
			job:      "todo-autocomplete-stale",
			wantZone: "Asia/Jakarta",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc, err := tt.cfg.Locations()
			require.NoError(t, err)
			require.Equal(t, tt.wantZone, loc(tt.job).String())
		})
	}
}

func TestSchedulerConfig_Locations_RejectsBadZones(t *testing.T) {
	tests := []struct {
		name string
		cfg  SchedulerConfig
	}{
		{"bad global", SchedulerConfig{Timezone: "Mars/Olympus"}},
		{"bad per-job", SchedulerConfig{Jobs: map[string]SchedulerJobConfig{"j": {Timezone: "Mars/Olympus"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.cfg.Locations()
			require.Error(t, err, "a bad IANA name must fail at boot, not silently fall back to UTC")
		})
	}
}

func TestSchedulerConfig_Locations_NilReceiverIsUTC(t *testing.T) {
	var cfg *SchedulerConfig
	loc, err := cfg.Locations()
	require.NoError(t, err)
	require.Equal(t, "UTC", loc("any").String())
}
