package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/web/handlers"
)

func TestOnboardingGate_RedirectsWhenRequired(t *testing.T) {
	t.Parallel()
	req := &atomic.Bool{}
	req.Store(true)
	gate := handlers.OnboardingGate("", req)
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("next should not be called while onboarding required")
	})
	h := gate(next)

	for _, path := range []string{"/", "/dashboard", "/orgs"} {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		h.ServeHTTP(rec, r)
		assert.Equal(t, http.StatusSeeOther, rec.Code, "path=%q", path)
		assert.Equal(t, "/onboard", rec.Header().Get("Location"), "path=%q", path)
	}
}

func TestOnboardingGate_AllowsOnboardAndHealth(t *testing.T) {
	t.Parallel()
	req := &atomic.Bool{}
	req.Store(true)
	gate := handlers.OnboardingGate("", req)
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := gate(next)

	for _, path := range []string{"/onboard", "/onboard/local", "/onboard/oidc", "/healthz", "/readyz", "/robots.txt", "/static/app.css", "/oauth/callback", "/login/oidc"} {
		called = false
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		h.ServeHTTP(rec, r)
		assert.True(t, called, "path=%q: next not called", path)
		assert.Less(t, rec.Code, 400, "path=%q: status=%d", path, rec.Code)
	}
}

func TestOnboardingGate_NoRedirectWhenNotRequired(t *testing.T) {
	t.Parallel()
	req := &atomic.Bool{}
	gate := handlers.OnboardingGate("", req)
	called := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	})
	h := gate(next)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/anything", nil)
	h.ServeHTTP(rec, r)
	require.True(t, called, "next must be called when onboarding not required")
	assert.NotEqual(t, http.StatusSeeOther, rec.Code, "must not redirect when onboarding not required")
}

func TestOnboardingGate_HonoursBasePath(t *testing.T) {
	t.Parallel()
	req := &atomic.Bool{}
	req.Store(true)
	gate := handlers.OnboardingGate("/app", req)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := gate(next)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/app/orgs", nil)
	h.ServeHTTP(rec, r)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	loc := rec.Header().Get("Location")
	assert.True(t, strings.HasPrefix(loc, "/app/onboard"), "Location=%q must start with /app/onboard", loc)
}
