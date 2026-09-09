package config

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/db"
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

	t.Setenv("OPENSHEET_HTTP_ADDR", ":8080")

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
		c.Security.EncryptionKey = "0123456789abcdef"
		c.HTTP.StateSecret = testStateSecret
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

const testStateSecret = "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"

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
	t.Setenv("OPENSHEET_GENESIS_EMAIL", "root@example.com")
	t.Setenv("OPENSHEET_GENESIS_PASSWORD", "x")
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

func TestValidate_CloudRequiresEncryptionKey(t *testing.T) {
	c := validCloudConfig(t)
	c.Security.EncryptionKey = ""
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "security.encryptionKey") {
		t.Fatalf("Validate() = %v, want an error naming security.encryptionKey", err)
	}
}

func TestValidate_PostgresSessionStoreRequiresAnEncryptionKey(t *testing.T) {
	c := validSelfhostedConfig(t)
	c.DB.Driver = db.DriverPostgres
	c.DB.DSN = "postgres://localhost/opensheet"
	c.Security.EncryptionKey = ""
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "OPENSHEET_SECURITY_ENCRYPTION_KEY") {
		t.Fatalf("Validate() = %v, want an error naming OPENSHEET_SECURITY_ENCRYPTION_KEY", err)
	}
}

func TestValidate_PostgresSessionStoreRequiresAStateSecret(t *testing.T) {
	c := validSelfhostedConfig(t)
	c.DB.Driver = db.DriverPostgres
	c.DB.DSN = "postgres://localhost/opensheet"
	c.Security.EncryptionKey = "0123456789abcdef"
	c.HTTP.StateSecret = ""
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "OPENSHEET_HTTP_STATE_SECRET") {
		t.Fatalf("Validate() = %v, want an error naming OPENSHEET_HTTP_STATE_SECRET", err)
	}
}

// TestValidate_StateSecretMustDecodeToThirtyTwoBytes proves config-time validation raises the failure its own message describes, instead of deferring the length check to boot.
func TestValidate_StateSecretMustDecodeToThirtyTwoBytes(t *testing.T) {
	tests := []struct {
		name    string
		secret  string
		wantSub string
	}{
		{"hex too short", "0f1e2d3c4b5a6978", "decodes to 8 bytes"},
		{"base64 too short", "c2hvcnQ", "decodes to 5 bytes"},
		{"not hex nor base64", "not-hex-or-base64!!$", "neither hex nor base64"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validSelfhostedConfig(t)
			c.HTTP.StateSecret = tt.secret
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("Validate() = %v, want an error mentioning %q", err, tt.wantSub)
			}
			if !strings.Contains(err.Error(), "OPENSHEET_HTTP_STATE_SECRET") {
				t.Errorf("Validate() = %v, want an error naming OPENSHEET_HTTP_STATE_SECRET", err)
			}
		})
	}
}

func TestValidate_StateSecretAcceptsHexAndBase64(t *testing.T) {
	tests := []struct {
		name   string
		secret string
	}{
		{"hex", testStateSecret},
		{"base64url", "D_HtPEtaaXiHlqW0w9Lh8A_x7TxLWml4h5altMPS4fA"},
		{"base64std", "D/HtPEtaaXiHlqW0w9Lh8A/x7TxLWml4h5altMPS4fA="},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validSelfhostedConfig(t)
			c.HTTP.StateSecret = tt.secret
			if err := c.Validate(); err != nil {
				t.Fatalf("Validate() with a %s state secret = %v, want nil", tt.name, err)
			}
		})
	}
}

func TestValidate_GoogleClientSecretWithoutClientID(t *testing.T) {
	c := validSelfhostedConfig(t)
	c.Google.OAuth.ClientSecret = "shhh"
	c.Google.OAuth.ClientID = ""
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "google.oauth.clientID") {
		t.Fatalf("Validate() = %v, want an error naming google.oauth.clientID", err)
	}
}

func TestValidate_PostgresCacheRejectsSQLite(t *testing.T) {
	c := validSelfhostedConfig(t)
	c.DB.Driver = db.DriverSQLite
	c.Cache.Driver = CacheDriverPostgres
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() = nil, want an error for cache.driver=postgres with db.driver=sqlite")
	}
}

func TestValidate_ValidConfigsPass(t *testing.T) {
	if err := validSelfhostedConfig(t).Validate(); err != nil {
		t.Fatalf("validSelfhostedConfig: %v", err)
	}
	if err := validCloudConfig(t).Validate(); err != nil {
		t.Fatalf("validCloudConfig: %v", err)
	}
}

func TestCacheConfig_Resolve(t *testing.T) {
	tests := []struct {
		driver   CacheDriver
		dbDriver db.Driver
		want     CacheDriver
	}{
		{CacheDriverAuto, db.DriverPostgres, CacheDriverPostgres},
		{CacheDriverAuto, db.DriverSQLite, CacheDriverMemory},
		{CacheDriverMemory, db.DriverPostgres, CacheDriverMemory},
		{CacheDriverPostgres, db.DriverPostgres, CacheDriverPostgres},
	}
	for _, tc := range tests {
		got := CacheConfig{Driver: tc.driver}.Resolve(tc.dbDriver)
		if got != tc.want {
			t.Errorf("Resolve(%q, %q) = %q, want %q", tc.driver, tc.dbDriver, got, tc.want)
		}
	}
}

func TestDefaults_OpensheetKeys(t *testing.T) {
	cfg := Defaults()
	if cfg.Cache.Driver != CacheDriverAuto {
		t.Errorf("cache.driver = %q, want %q", cfg.Cache.Driver, CacheDriverAuto)
	}
	if cfg.Cache.DefaultTTL != 30*time.Second {
		t.Errorf("cache.defaultTTL = %s, want 30s", cfg.Cache.DefaultTTL)
	}
	if cfg.Cache.MaxBytes != 67108864 {
		t.Errorf("cache.maxBytes = %d, want 67108864", cfg.Cache.MaxBytes)
	}
	if cfg.Sheets.MaxPayloadBytes != 8388608 {
		t.Errorf("sheets.maxPayloadBytes = %d, want 8388608", cfg.Sheets.MaxPayloadBytes)
	}
	if cfg.Google.Timeout != 15*time.Second {
		t.Errorf("google.timeout = %s, want 15s", cfg.Google.Timeout)
	}
}

func TestDefaults_PublicSheetsOffInBothModes(t *testing.T) {
	for _, mode := range []Mode{ModeSelfhosted, ModeCloud} {
		t.Run(string(mode), func(t *testing.T) {
			c := loadWithMode(t, mode)
			if c.Sheets.PublicEnabled {
				t.Fatal("sheets.publicEnabled defaulted true; public sheets must be opt-in")
			}
		})
	}
}

func TestEnvKeys_NewFieldsAreBound(t *testing.T) {
	var keys []string
	for _, k := range WalkEnvKeys("OPENSHEET") {
		keys = append(keys, k.Key)
	}
	for _, want := range []string{
		"OPENSHEET_SECURITY_ENCRYPTION_KEY",
		"OPENSHEET_GOOGLE_OAUTH_CLIENT_ID",
		"OPENSHEET_GOOGLE_OAUTH_CLIENT_SECRET",
		"OPENSHEET_GOOGLE_PICKER_API_KEY",
		"OPENSHEET_CACHE_DRIVER",
		"OPENSHEET_SHEETS_PUBLIC_ENABLED",
	} {
		if !slices.Contains(keys, want) {
			t.Errorf("WalkEnvKeys missing %q", want)
		}
	}
}

func TestLoad_SecurityAndGoogleFromEnv(t *testing.T) {
	t.Setenv("OPENSHEET_SECURITY_ENCRYPTION_KEY", "0123456789abcdef")
	t.Setenv("OPENSHEET_GOOGLE_OAUTH_CLIENT_ID", "cid")
	t.Setenv("OPENSHEET_GOOGLE_OAUTH_CLIENT_SECRET", "csecret")
	t.Setenv("OPENSHEET_GOOGLE_PICKER_API_KEY", "pkey")
	t.Setenv("OPENSHEET_SHEETS_PUBLIC_ENABLED", "true")

	cfg := loadWithMode(t, ModeSelfhosted)
	require.Equal(t, "0123456789abcdef", cfg.Security.EncryptionKey)
	require.Equal(t, "cid", cfg.Google.OAuth.ClientID)
	require.Equal(t, "csecret", cfg.Google.OAuth.ClientSecret)
	require.Equal(t, "pkey", cfg.Google.Picker.APIKey)
	require.True(t, cfg.Sheets.PublicEnabled)
}

func validSelfhostedConfig(t *testing.T) *Config {
	t.Helper()
	c := &Config{
		Mode:    ModeSelfhosted,
		DB:      validDB(),
		Genesis: GenesisConfig{Email: "root@example.com", Password: "x"},
	}
	c.Tenant.SingletonOrg.Slug = "default"
	c.Tenant.SingletonOrg.Name = "Default Organization"
	c.Cache.Driver = CacheDriverAuto
	return c
}

func validCloudConfig(t *testing.T) *Config {
	t.Helper()
	c := validSelfhostedConfig(t)
	c.Mode = ModeCloud
	c.DB.Driver = db.DriverPostgres
	c.DB.DSN = "postgres://localhost/opensheet"
	c.OIDC = OIDCConfig{Issuer: "https://iss.example.com", ClientID: "cid", ClientSecret: "csecret"}
	c.Genesis.BreakGlass = true
	c.Security.EncryptionKey = "0123456789abcdef"
	c.HTTP.StateSecret = testStateSecret
	return c
}

func loadWithMode(t *testing.T, mode Mode) *Config {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	t.Setenv("OPENSHEET_MODE", string(mode))
	t.Setenv("OPENSHEET_GENESIS_EMAIL", "root@example.com")
	if mode == ModeCloud {
		t.Setenv("OPENSHEET_DB_DRIVER", string(db.DriverPostgres))
		t.Setenv("OPENSHEET_DB_DSN", "postgres://localhost/opensheet")
		t.Setenv("OPENSHEET_DB_MIGRATOR_DSN", "postgres://migrator@localhost/opensheet")
		t.Setenv("OPENSHEET_OIDC_ISSUER", "https://iss.example.com")
		t.Setenv("OPENSHEET_OIDC_CLIENT_ID", "cid")
		t.Setenv("OPENSHEET_OIDC_CLIENT_SECRET", "csecret")
		t.Setenv("OPENSHEET_SECURITY_ENCRYPTION_KEY", "0123456789abcdef")
		t.Setenv("OPENSHEET_HTTP_STATE_SECRET", testStateSecret)
	}
	cfg, err := Load("")
	require.NoError(t, err)
	return cfg
}
