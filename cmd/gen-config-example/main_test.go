package main

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func TestGenerator_ProducesOutput(t *testing.T) {
	out := runGenerator(t)
	if len(out) < 100 {
		t.Fatalf("output too short: %q", out)
	}
	if !strings.Contains(out, "ALT_") {
		t.Error("expected ALT_ env vars in output")
	}
	if !strings.Contains(out, "[") {
		t.Error("expected awareness markers")
	}
}

func TestGenerator_HasBootstrapAndRuntimeSections(t *testing.T) {
	out := runGenerator(t)
	if !strings.Contains(out, "BOOTSTRAP") {
		t.Error("expected BOOTSTRAP section marker in output")
	}
	if !strings.Contains(out, "RUNTIME") {
		t.Error("expected RUNTIME section marker in output")
	}
	// Order matters: bootstrap must precede runtime.
	if strings.Index(out, "BOOTSTRAP") > strings.Index(out, "RUNTIME") {
		t.Error("BOOTSTRAP section should appear before RUNTIME section")
	}
}

func TestGenerator_EmitsNonSecretDefaults(t *testing.T) {
	out := runGenerator(t)
	// At least one field with a non-empty default should be present verbatim.
	wants := []string{
		"ALT_DB_DRIVER=sqlite",
		"ALT_HTTP_ADDR=:5150",
		"ALT_LOG_LEVEL=info",
		"ALT_LOG_FORMAT=json",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("expected output to contain %q", w)
		}
	}
}

func TestGenerator_SecretsHaveNoRHS(t *testing.T) {
	out := runGenerator(t)
	// Secret fields must appear with empty RHS regardless of any default.
	secretVars := []string{
		"ALT_HTTP_STATE_SECRET",
		"ALT_DB_DSN",
		"ALT_GENESIS_PASSWORD",
		"ALT_OIDC_CLIENT_SECRET",
		"ALT_MAIL_SMTP_PASS",
		"ALT_API_OPENAPI_BASIC_AUTH_PASSWORD",
		"ALT_TELEMETRY_OTLP_HEADERS",
	}
	for _, name := range secretVars {
		re := regexp.MustCompile("(?m)^" + regexp.QuoteMeta(name) + "=(.*)$")
		m := re.FindStringSubmatch(out)
		if m == nil {
			t.Errorf("secret var %q not present in output", name)
			continue
		}
		if m[1] != "" {
			t.Errorf("secret var %q must have empty RHS, got %q", name, m[1])
		}
	}
}

func runGenerator(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "run", ".", "--output", "-").Output()
	if err != nil {
		t.Fatalf("run generator: %v", err)
	}
	return string(out)
}
