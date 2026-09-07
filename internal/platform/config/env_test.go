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
		{"http.addr", "ALT_HTTP_ADDR"},
		{"http.baseURL", "ALT_HTTP_BASE_URL"},
		{"telemetry.otlp.endpoint", "ALT_TELEMETRY_OTLP_ENDPOINT"},
		{"tokens.jwksURL", "ALT_TOKENS_JWKS_URL"},
		{"db.autoMigrate", "ALT_DB_AUTO_MIGRATE"},
		{"db.AutoMigrate", "ALT_DB_AUTO_MIGRATE"},
		{"api.openapi.requireBasicAuth", "ALT_API_OPENAPI_REQUIRE_BASIC_AUTH"},
		{"observability.reporter.minSeverity", "ALT_OBSERVABILITY_REPORTER_MIN_SEVERITY"},
	}
	for _, tc := range tests {
		got := envVarName("ALT", tc.key)
		if got != tc.want {
			t.Errorf("envVarName(%q): want %q, got %q", tc.key, tc.want, got)
		}
	}
}

func TestLoad_NestedEnvOverride_OTLPEndpoint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ALT_TELEMETRY_OTLP_ENDPOINT", "http://collector:4318")
	t.Setenv("ALT_GENESIS_EMAIL", "root@example.com")
	t.Setenv("ALT_GENESIS_PASSWORD", "x")

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
	t.Setenv("ALT_GENESIS_EMAIL", "root@example.com")
	t.Setenv("ALT_GENESIS_PASSWORD", "x")
	t.Setenv("ALT_DB_AUTO_MIGRATE", "false")
	t.Setenv("ALT_OIDC_REDIRECT_PORT", "8123")
	t.Setenv("ALT_TOKENS_CLOCK_SKEW", "45s")

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
	keys := WalkEnvKeys("ALT")

	byEnv := map[string]EnvKey{}
	for _, k := range keys {
		byEnv[k.Key] = k
	}

	expect := []struct {
		env, yaml, kind string
	}{
		{"ALT_MODE", "mode", "string"},
		{"ALT_HTTP_ADDR", "http.addr", "string"},
		{"ALT_HTTP_BASE_URL", "http.baseURL", "string"},
		{"ALT_HTTP_STATE_SECRET", "http.stateSecret", "string"},
		{"ALT_TELEMETRY_OTLP_ENDPOINT", "telemetry.otlp.endpoint", "string"},
		{"ALT_TOKENS_CLOCK_SKEW", "tokens.clockSkew", "duration"},
		{"ALT_TOKENS_SUPPORTED_ALGS", "tokens.supportedAlgs", "[]string"},
		{"ALT_OIDC_REDIRECT_PORT", "oidc.redirectPort", "int"},
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
	keys := WalkEnvKeys("ALT")
	byEnv := map[string]EnvKey{}
	for _, k := range keys {
		byEnv[k.Key] = k
	}

	withDefaults := []struct {
		env, want string
	}{
		{"ALT_HTTP_ADDR", ":5150"},
		{"ALT_DB_DRIVER", "sqlite"},
		{"ALT_LOG_LEVEL", "info"},
		{"ALT_TOKENS_AUDIENCE", "urn:altempl:api"},
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
		"ALT_HTTP_STATE_SECRET",
		"ALT_TELEMETRY_OTLP_ENDPOINT",
		"ALT_OIDC_ISSUER",
		"ALT_MAIL_SMTP_PASS",
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
	keys := WalkEnvKeys("ALT")
	byEnv := map[string]EnvKey{}
	for _, k := range keys {
		byEnv[k.Key] = k
	}

	tests := []struct {
		env       string
		mustHave  []string
		mustMatch []string
	}{
		{env: "ALT_HTTP_STATE_SECRET", mustHave: []string{"required", "secret", "bootstrap"}},
		{env: "ALT_DB_DSN", mustHave: []string{"required", "secret"}},
		{env: "ALT_DB_DRIVER", mustHave: []string{"required", "bootstrap"}},
		{env: "ALT_OIDC_CLIENT_SECRET", mustHave: []string{"required", "mode:cloud", "secret"}},
		{env: "ALT_ONBOARD_SETUP_TOKEN", mustHave: []string{"secret"}},
		{env: "ALT_GENESIS_PASSWORD", mustHave: []string{"bootstrap", "secret"}},
		{env: "ALT_MAIL_SMTP_PASS", mustHave: []string{"secret"}},
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
	for _, k := range WalkEnvKeys("ALT") {
		byEnv[k.Key] = struct{}{}
	}
	_, hasSchedulerJobs := byEnv["ALT_SCHEDULER_JOBS"]
	require.False(t, hasSchedulerJobs,
		"struct-valued map config is not env-addressable and must not appear in .env.example")

	_, hasTelemetryHeaders := byEnv["ALT_TELEMETRY_OTLP_HEADERS"]
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

// db.schema defaults to public so postgres migrations render a qualified name; an empty env var
// cannot clear it, because AutomaticEnv without AllowEmptyEnv reads "" as unset.
func TestLoad_SchemaDefaultAndOptOut(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DB.Schema != "public" {
		t.Errorf("default schema = %q, want public", cfg.DB.Schema)
	}

	t.Setenv("ALT_DB_SCHEMA", "")
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
