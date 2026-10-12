package controlplane

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// SECURITY: the app-wide CSP has no unpkg.com and no nonce for this page, so without its own policy every <script> here is refused and the docs render blank.
func TestDocsHandler_NoncesEveryScriptAndShipsItsOwnCSP(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	docsHandler("/api/openapi.json").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs", nil))

	policy := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "https://unpkg.com") {
		t.Fatalf("docs policy must allow its asset origin: %q", policy)
	}
	if strings.Contains(policy, "'unsafe-inline'") && strings.Contains(policy, "script-src") {
		scriptSrc := ""
		for _, d := range strings.Split(policy, ";") {
			if strings.HasPrefix(strings.TrimSpace(d), "script-src ") {
				scriptSrc = d
			}
		}
		if strings.Contains(scriptSrc, "'unsafe-inline'") {
			t.Fatalf("script-src must not carry 'unsafe-inline': %q", scriptSrc)
		}
	}

	nonce := regexp.MustCompile(`'nonce-([A-Za-z0-9+/]+)'`).FindStringSubmatch(policy)
	if nonce == nil {
		t.Fatalf("policy carries no nonce: %q", policy)
	}
	body := rec.Body.String()
	scripts := regexp.MustCompile(`<script[^>]*>`).FindAllString(body, -1)
	if len(scripts) == 0 {
		t.Fatal("no <script> tags in the docs page — the template changed")
	}
	for _, tag := range scripts {
		if !strings.Contains(tag, `nonce="`+nonce[1]+`"`) {
			t.Errorf("script tag has no matching nonce, so the browser will refuse it: %s", tag)
		}
	}
}

func TestDocsHandler_NonceIsPerRequest(t *testing.T) {
	t.Parallel()

	h := docsHandler("/api/openapi.json")
	first, second := httptest.NewRecorder(), httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/api/docs", nil))
	h.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/api/docs", nil))
	if first.Header().Get("Content-Security-Policy") == second.Header().Get("Content-Security-Policy") {
		t.Fatal("the docs nonce was reused across requests")
	}
}
