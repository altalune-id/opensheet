package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEnvVarName(t *testing.T) {
	tests := []struct {
		key, want string
	}{
		{"http.addr", "OPENSHEET_HTTP_ADDR"},
		{"http.baseURL", "OPENSHEET_HTTP_BASE_URL"},
		{"telemetry.otlp.endpoint", "OPENSHEET_TELEMETRY_OTLP_ENDPOINT"},
		{"tokens.introspection.clientID", "OPENSHEET_TOKENS_INTROSPECTION_CLIENT_ID"},
		{"db.autoMigrate", "OPENSHEET_DB_AUTO_MIGRATE"},
		{"db.AutoMigrate", "OPENSHEET_DB_AUTO_MIGRATE"},
		{"api.openapi.requireBasicAuth", "OPENSHEET_API_OPENAPI_REQUIRE_BASIC_AUTH"},
		{"observability.reporter.minSeverity", "OPENSHEET_OBSERVABILITY_REPORTER_MIN_SEVERITY"},
	}
	for _, tc := range tests {
		got := envVarName("OPENSHEET", tc.key)
		if got != tc.want {
			t.Errorf("envVarName(%q): want %q, got %q", tc.key, tc.want, got)
		}
	}
}

func TestLoad_NestedEnvOverride_OTLPEndpoint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENSHEET_TELEMETRY_OTLP_ENDPOINT", "http://collector:4318")
	t.Setenv("OPENSHEET_GENESIS_EMAIL", "root@example.com")
	t.Setenv("OPENSHEET_GENESIS_PASSWORD", "x")

	dir := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Telemetry.OTLP.Endpoint != "http://collector:4318" {
		t.Fatalf("want %q, got %q", "http://collector:4318", cfg.Telemetry.OTLP.Endpoint)
	}
}

func TestLoad_TypedEnvConversions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENSHEET_GENESIS_EMAIL", "root@example.com")
	t.Setenv("OPENSHEET_GENESIS_PASSWORD", "x")
	t.Setenv("OPENSHEET_DB_AUTO_MIGRATE", "false")
	t.Setenv("OPENSHEET_OIDC_REDIRECT_PORT", "8123")
	t.Setenv("OPENSHEET_TOKENS_CLOCK_SKEW", "45s")

	dir := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DB.AutoMigrate {
		t.Fatalf("bool env override failed: want false, got true")
	}
	if cfg.OIDC.RedirectPort != 8123 {
		t.Fatalf("int env override failed: got %d", cfg.OIDC.RedirectPort)
	}
	if cfg.Tokens.ClockSkew != 45*time.Second {
		t.Fatalf("duration env override failed: got %v", cfg.Tokens.ClockSkew)
	}
}

func TestWalkEnvKeys_IncludesKnownLeaves(t *testing.T) {
	keys := WalkEnvKeys("OPENSHEET")

	byEnv := map[string]EnvKey{}
	for _, k := range keys {
		byEnv[k.Key] = k
	}

	expect := []struct {
		env, yaml, kind string
	}{
		{"OPENSHEET_MODE", "mode", "string"},
		{"OPENSHEET_HTTP_ADDR", "http.addr", "string"},
		{"OPENSHEET_HTTP_BASE_URL", "http.baseURL", "string"},
		{"OPENSHEET_HTTP_STATE_SECRET", "http.stateSecret", "string"},
		{"OPENSHEET_TELEMETRY_OTLP_ENDPOINT", "telemetry.otlp.endpoint", "string"},
		{"OPENSHEET_TOKENS_CLOCK_SKEW", "tokens.clockSkew", "duration"},
		{"OPENSHEET_TOKENS_INTROSPECTION_CLIENT_SECRET", "tokens.introspection.clientSecret", "string"},
		{"OPENSHEET_OIDC_CLI_CLIENT_ID", "oidc.cliClientID", "string"},
		{"OPENSHEET_OIDC_REDIRECT_PORT", "oidc.redirectPort", "int"},
	}
	for _, want := range expect {
		got, ok := byEnv[want.env]
		if !ok {
			t.Errorf("missing env key %q", want.env)
			continue
		}
		if got.YAML != want.yaml {
			t.Errorf("%s: yaml want %q, got %q", want.env, want.yaml, got.YAML)
		}
		if got.Kind != want.kind {
			t.Errorf("%s: kind want %q, got %q", want.env, want.kind, got.Kind)
		}
	}
}

func TestWalkEnvKeys_Defaults(t *testing.T) {
	keys := WalkEnvKeys("OPENSHEET")
	byEnv := map[string]EnvKey{}
	for _, k := range keys {
		byEnv[k.Key] = k
	}

	withDefaults := []struct {
		env, want string
	}{
		{"OPENSHEET_HTTP_ADDR", ":5150"},
		{"OPENSHEET_DB_DRIVER", "sqlite"},
		{"OPENSHEET_LOG_LEVEL", "info"},
		{"OPENSHEET_TOKENS_CLOCK_SKEW", "60s"},
		{"OPENSHEET_OIDC_END_SESSION_ON_LOGOUT", "true"},
	}
	for _, tc := range withDefaults {
		got, ok := byEnv[tc.env]
		if !ok {
			t.Errorf("missing env key %q", tc.env)
			continue
		}
		if got.Default == nil {
			t.Errorf("%s: Default is nil, want %q", tc.env, tc.want)
			continue
		}
		if formatted := got.FormatDefault(); formatted != tc.want {
			t.Errorf("%s: FormatDefault want %q, got %q", tc.env, tc.want, formatted)
		}
	}

	withoutDefaults := []string{
		"OPENSHEET_HTTP_STATE_SECRET",
		"OPENSHEET_TELEMETRY_OTLP_ENDPOINT",
		"OPENSHEET_OIDC_ISSUER",
		"OPENSHEET_MAIL_SMTP_PASS",
	}
	for _, env := range withoutDefaults {
		got, ok := byEnv[env]
		if !ok {
			t.Errorf("missing env key %q", env)
			continue
		}
		if got.Default != nil {
			t.Errorf("%s: Default want nil, got %#v", env, got.Default)
		}
		if got.FormatDefault() != "" {
			t.Errorf("%s: FormatDefault want empty, got %q", env, got.FormatDefault())
		}
	}
}

func TestEnvKey_FormatDefault_TypeCoverage(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"string", "hello", "hello"},
		{"bool-true", true, "true"},
		{"bool-false", false, "false"},
		{"int", 42, "42"},
		{"int64", int64(9000000000), "9000000000"},
		{"duration", 30 * time.Second, "30s"},
		{"[]string", []string{"a", "b", "c"}, "a,b,c"},
		{"[]any", []any{"x", 2, true}, "x,2,true"},
		{"nil", nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := EnvKey{Default: tc.in}.FormatDefault()
			if got != tc.want {
				t.Errorf("want %q, got %q", tc.want, got)
			}
		})
	}
}

func TestWalkEnvKeys_Awareness(t *testing.T) {
	keys := WalkEnvKeys("OPENSHEET")
	byEnv := map[string]EnvKey{}
	for _, k := range keys {
		byEnv[k.Key] = k
	}

	tests := []struct {
		env       string
		mustHave  []string
		mustMatch []string
	}{
		{env: "OPENSHEET_HTTP_STATE_SECRET", mustHave: []string{"required", "secret", "bootstrap"}},
		{env: "OPENSHEET_DB_DSN", mustHave: []string{"required", "secret"}},
		{env: "OPENSHEET_DB_DRIVER", mustHave: []string{"required", "bootstrap"}},
		{env: "OPENSHEET_OIDC_CLIENT_SECRET", mustHave: []string{"required", "mode:cloud", "secret"}},
		{env: "OPENSHEET_ONBOARD_SETUP_TOKEN", mustHave: []string{"secret"}},
		{env: "OPENSHEET_GENESIS_PASSWORD", mustHave: []string{"bootstrap", "secret"}},
		{env: "OPENSHEET_MAIL_SMTP_PASS", mustHave: []string{"secret"}},
	}
	for _, tc := range tests {
		got, ok := byEnv[tc.env]
		if !ok {
			t.Errorf("missing env key %q", tc.env)
			continue
		}
		if !containsAll(got.Awareness, tc.mustHave) {
			t.Errorf("%s: awareness want superset of %v, got %v", tc.env, tc.mustHave, got.Awareness)
		}
	}
}

func TestLoad_RequireFile_PathProvided(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	missing := filepath.Join(t.TempDir(), "nope.yaml")
	_, err := Load(missing, WithRequireFile())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "nope.yaml") && !strings.Contains(err.Error(), "required") {
		t.Fatalf("expected required-file error, got %v", err)
	}
}

func TestWalkEnvKeys_SkipsStructValuedMapsOnly(t *testing.T) {
	byEnv := map[string]struct{}{}
	for _, k := range WalkEnvKeys("OPENSHEET") {
		byEnv[k.Key] = struct{}{}
	}
	_, hasSchedulerJobs := byEnv["OPENSHEET_SCHEDULER_JOBS"]
	require.False(t, hasSchedulerJobs,
		"struct-valued map config is not env-addressable and must not appear in .env.example")

	_, hasTelemetryHeaders := byEnv["OPENSHEET_TELEMETRY_OTLP_HEADERS"]
	require.True(t, hasTelemetryHeaders,
		"scalar-valued map config is env-addressable and must still appear in .env.example")
}

func containsAll(haystack, needles []string) bool {
	set := make(map[string]struct{}, len(haystack))
	for _, h := range haystack {
		set[h] = struct{}{}
	}
	for _, n := range needles {
		if _, ok := set[n]; !ok {
			return false
		}
	}
	return true
}

// TestLoad_SchemaDefaultAndOptOut covers db.schema's default, which an empty env var cannot clear.
func TestLoad_SchemaDefaultAndOptOut(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DB.Schema != "public" {
		t.Errorf("default schema = %q, want public", cfg.DB.Schema)
	}

	t.Setenv("OPENSHEET_DB_SCHEMA", "")
	envCfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if envCfg.DB.Schema != "public" {
		t.Errorf("empty env schema = %q; an empty env var must not be mistaken for an opt-out", envCfg.DB.Schema)
	}

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("db:\n  schema: \"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fileCfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if fileCfg.DB.Schema != "" {
		t.Errorf("file schema = %q, want empty; the config file is the only working opt-out", fileCfg.DB.Schema)
	}
}

// NOTE: with a config type set, viper would match the extensionless compiled binary and parse it as YAML.
func TestLoad_SearchPathIgnoresExtensionlessBinary(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Chdir(dir)

	if err := os.WriteFile(filepath.Join(dir, "opensheet"), []byte("\x7fELF\x02\x01\x01not yaml"), 0o600); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	if _, err := Load(""); err != nil {
		t.Fatalf("Load must ignore an extensionless file named opensheet, got %v", err)
	}
}

func TestLoad_SearchPathStillFindsExtensionedConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Chdir(dir)

	if err := os.WriteFile(filepath.Join(dir, "opensheet.yaml"), []byte("mode: selfhosted\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Mode != ModeSelfhosted {
		t.Fatalf("Mode = %q, want %q — the extensioned config was not read", cfg.Mode, ModeSelfhosted)
	}
}
