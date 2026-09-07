package i18n_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"altalune.id/template/internal/i18n"
)

func TestSanitizeRedirect(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, want string
	}{
		{"/projects", "/projects"},
		{"/", "/"},
		{"", ""},
		{"//evil.com/path", ""},
		{"https://evil.com/", ""},
		{"http://evil.com/", ""},
		{"javascript:alert(1)", ""},
		{"/ok?redirect=/x", "/ok?redirect=/x"},
		{"/back\\slash", "/back/slash"},
		{"/with:colon", ""},
		{"/\\evil.com", ""},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := i18n.SanitizeRedirect(tc.in); got != tc.want {
				t.Errorf("SanitizeRedirect(%q)=%q want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSwitcher_SetsCookieAndRedirects(t *testing.T) {
	t.Parallel()
	b := i18n.NewEmbeddedBundle(i18n.EnUS)
	h := i18n.Switcher(i18n.SwitcherOpts{Bundle: b, CookieSecure: true, CookiePath: "/app"})

	form := url.Values{}
	form.Set("locale", "id-ID")
	form.Set("redirect", "/app/dashboard")
	req := httptest.NewRequest(http.MethodPost, "/locale", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("status=%d want 303", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/app/dashboard" {
		t.Errorf("Location=%q", loc)
	}
	setCookie := rr.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, "alt_locale=id-ID") {
		t.Errorf("Set-Cookie=%q", setCookie)
	}
	if !strings.Contains(setCookie, "Path=/app") {
		t.Errorf("Set-Cookie path missing: %q", setCookie)
	}
	if !strings.Contains(setCookie, "Secure") {
		t.Errorf("Set-Cookie should be Secure: %q", setCookie)
	}
	if !strings.Contains(setCookie, "SameSite=Lax") {
		t.Errorf("Set-Cookie SameSite missing: %q", setCookie)
	}
	if !strings.Contains(setCookie, "Max-Age=31536000") {
		t.Errorf("Set-Cookie max-age wrong: %q", setCookie)
	}
}

func TestSwitcher_InvalidLocaleReturns400(t *testing.T) {
	t.Parallel()
	b := i18n.NewEmbeddedBundle(i18n.EnUS)
	h := i18n.Switcher(i18n.SwitcherOpts{Bundle: b})
	form := url.Values{}
	form.Set("locale", "zz-ZZ")
	req := httptest.NewRequest(http.MethodPost, "/locale", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status=%d want 400", rr.Code)
	}
}

func TestSwitcher_OpenRedirectRejected(t *testing.T) {
	t.Parallel()
	b := i18n.NewEmbeddedBundle(i18n.EnUS)
	h := i18n.Switcher(i18n.SwitcherOpts{Bundle: b, Fallback: "/"})
	form := url.Values{}
	form.Set("locale", "en-US")
	form.Set("redirect", "//evil.com/attack")
	req := httptest.NewRequest(http.MethodPost, "/locale", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if got := rr.Header().Get("Location"); got != "/" {
		t.Errorf("Location=%q — attempted open redirect must fall back", got)
	}
}

func TestSwitcher_RejectsGET(t *testing.T) {
	t.Parallel()
	b := i18n.NewEmbeddedBundle(i18n.EnUS)
	h := i18n.Switcher(i18n.SwitcherOpts{Bundle: b})
	req := httptest.NewRequest(http.MethodGet, "/locale?locale=id-ID", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status=%d want 405", rr.Code)
	}
}

func TestSwitcher_CallsPersister(t *testing.T) {
	t.Parallel()
	b := i18n.NewEmbeddedBundle(i18n.EnUS)
	var got string
	persist := func(_ context.Context, tag string) error {
		got = tag
		return nil
	}
	h := i18n.Switcher(i18n.SwitcherOpts{Bundle: b, Persist: persist})
	form := url.Values{}
	form.Set("locale", "ja-JP")
	req := httptest.NewRequest(http.MethodPost, "/locale", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got != "ja-JP" {
		t.Errorf("persister got=%q want ja-JP", got)
	}
}

func TestSwitcher_NilBundlePanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic")
		}
	}()
	i18n.Switcher(i18n.SwitcherOpts{})
}
