package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"altalune.id/opensheet/internal/web/middleware"
)

func serveWithCSP(t *testing.T, opts middleware.CSPOptions) (*httptest.ResponseRecorder, string) {
	t.Helper()

	var nonce string
	h := middleware.CSP(opts)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		nonce = middleware.NonceFrom(r.Context())
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	return rec, nonce
}

func TestCSP_SetsNoncedPolicy(t *testing.T) {
	t.Parallel()

	rec, nonce := serveWithCSP(t, middleware.CSPOptions{Enabled: true})
	if nonce == "" {
		t.Fatal("no nonce reached the handler")
	}
	policy := rec.Header().Get("Content-Security-Policy")
	if policy == "" {
		t.Fatal("Content-Security-Policy header was not set")
	}
	if !strings.Contains(policy, "'nonce-"+nonce+"'") {
		t.Fatalf("policy does not carry the handler's nonce: %q", policy)
	}
	for _, want := range []string{
		"default-src 'self'",
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
	} {
		if !strings.Contains(policy, want) {
			t.Errorf("policy is missing %q: %q", want, policy)
		}
	}
}

// SECURITY: 'unsafe-inline' in script-src would re-enable the injected-handler path the nonce exists to close.
func TestCSP_ScriptSrcRefusesInlineWithoutNonce(t *testing.T) {
	t.Parallel()

	rec, _ := serveWithCSP(t, middleware.CSPOptions{Enabled: true})
	policy := rec.Header().Get("Content-Security-Policy")
	scriptSrc := ""
	for _, d := range strings.Split(policy, ";") {
		if strings.HasPrefix(strings.TrimSpace(d), "script-src ") {
			scriptSrc = strings.TrimSpace(d)
		}
	}
	if scriptSrc == "" {
		t.Fatalf("no script-src directive: %q", policy)
	}
	if strings.Contains(scriptSrc, "'unsafe-inline'") {
		t.Fatalf("script-src must not carry 'unsafe-inline': %q", scriptSrc)
	}
}

func TestCSP_NonceIsPerRequest(t *testing.T) {
	t.Parallel()

	_, first := serveWithCSP(t, middleware.CSPOptions{Enabled: true})
	_, second := serveWithCSP(t, middleware.CSPOptions{Enabled: true})
	if first == second {
		t.Fatalf("nonce was reused across requests: %q", first)
	}
}

func TestCSP_ReportOnlyUsesTheReportHeader(t *testing.T) {
	t.Parallel()

	rec, _ := serveWithCSP(t, middleware.CSPOptions{Enabled: true, ReportOnly: true, ReportURI: "/csp-report"})
	if rec.Header().Get("Content-Security-Policy") != "" {
		t.Error("report-only must not set the enforcing header")
	}
	policy := rec.Header().Get("Content-Security-Policy-Report-Only")
	if !strings.Contains(policy, "report-uri /csp-report") {
		t.Errorf("report-uri missing: %q", policy)
	}
}

func TestCSP_DisabledStillMintsANonce(t *testing.T) {
	t.Parallel()

	rec, nonce := serveWithCSP(t, middleware.CSPOptions{Enabled: false})
	if rec.Header().Get("Content-Security-Policy") != "" {
		t.Error("disabled CSP must not set the header")
	}
	if nonce == "" {
		t.Error("templates render a nonce attribute either way, so one must still be minted")
	}
}

func TestCSP_ExtraSourcesAreAppended(t *testing.T) {
	t.Parallel()

	rec, _ := serveWithCSP(t, middleware.CSPOptions{
		Enabled:        true,
		ExtraScriptSrc: []string{"https://cdn.example"},
		ExtraStyleSrc:  []string{"https://styles.example"},
	})
	policy := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "https://cdn.example") || !strings.Contains(policy, "https://styles.example") {
		t.Fatalf("extra sources were not appended: %q", policy)
	}
}

func TestCSP_ExtraFormActionIsAppended(t *testing.T) {
	t.Parallel()

	rec, _ := serveWithCSP(t, middleware.CSPOptions{Enabled: true, ExtraFormAction: []string{"https://id.example"}})
	policy := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "form-action 'self' https://id.example;") {
		t.Fatalf("form-action did not carry the extra origin: %q", policy)
	}
}
