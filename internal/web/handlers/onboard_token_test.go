package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/user"
	"altalune.id/template/internal/web/handlers"
)

const setupTokenForTest = "correct-horse-battery-staple"

func newTokenGatedOnboardMux(t *testing.T, f *handlerFixture, token string) *http.ServeMux {
	t.Helper()
	req := &atomicBoolWrapper{}
	req.b.Store(true)
	h := handlers.NewOnboardHandler(f.Deps, f.Users, f.Orgs, f.Projects, f.Onboards, &req.b, token)
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}

func TestOnboard_TokenGate_GetOnboard(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		supplied   string
		wantStatus int
	}{
		{"correct token", setupTokenForTest, http.StatusOK},
		{"missing token", "", http.StatusForbidden},
		{"wrong token", "nope", http.StatusForbidden},
		{"prefix of correct token", setupTokenForTest[:5], http.StatusForbidden},
		{"longer than correct token", setupTokenForTest + "x", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			mux := newTokenGatedOnboardMux(t, f, setupTokenForTest)

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/onboard?token="+url.QueryEscape(tc.supplied), nil))
			assert.Equal(t, tc.wantStatus, rec.Code)
		})
	}
}

func TestOnboard_TokenGate_CoversEveryRoute(t *testing.T) {
	t.Parallel()
	routes := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"GetOnboard", http.MethodGet, "/onboard", ""},
		{"PostLocal", http.MethodPost, "/onboard/local", "email=a@b.co&name=A&password=secret12&org_slug=acme&org_name=Acme"},
		{"GetOIDCStart", http.MethodGet, "/onboard/oidc", ""},
		{"GetOIDCComplete", http.MethodGet, "/onboard/complete", ""},
		{"PostOIDCComplete", http.MethodPost, "/onboard/complete", "org_slug=acme&org_name=Acme"},
	}
	for _, rt := range routes {
		t.Run(rt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			mux := newTokenGatedOnboardMux(t, f, setupTokenForTest)

			rec := httptest.NewRecorder()
			var r *http.Request
			if rt.body == "" {
				r = httptest.NewRequest(rt.method, rt.path, nil)
			} else {
				r = httptest.NewRequest(rt.method, rt.path, strings.NewReader(rt.body))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			mux.ServeHTTP(rec, r)
			require.Equal(t, http.StatusForbidden, rec.Code, "%s must refuse an unauthenticated setup request", rt.path)
			assert.Zero(t, f.UserStore.Len(), "a refused setup request must not create a user")
			stillRequired, rErr := f.Onboards.Required(t.Context())
			require.NoError(t, rErr)
			assert.True(t, stillRequired, "a refused setup request must not close onboarding")
		})
	}
}

func TestOnboard_TokenGate_OpenWhenUnset(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	mux := newTokenGatedOnboardMux(t, f, "")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/onboard", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestOnboard_TokenGate_CookieCarriesTokenAcrossOIDCRoundTrip(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u, err := f.Users.Create(t.Context(), user.CreateRequest{Email: "a@b.co", Name: "A", Source: user.SourceOIDC})
	require.NoError(t, err)
	mux := newTokenGatedOnboardMux(t, f, setupTokenForTest)

	start := httptest.NewRecorder()
	mux.ServeHTTP(start, httptest.NewRequest(http.MethodGet, "/onboard/oidc?token="+url.QueryEscape(setupTokenForTest), nil))
	require.Equal(t, http.StatusSeeOther, start.Code)

	var setup *http.Cookie
	for _, c := range start.Result().Cookies() {
		if c.Name == handlers.SetupCookieName {
			setup = c
		}
	}
	require.NotNil(t, setup, "the OIDC hand-off must remember the token for the return leg")

	back := f.authedRequest(t, http.MethodGet, "/onboard/complete", "", session.Principal{UserID: u.ID, Email: u.Email})
	back.AddCookie(setup)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, back)
	assert.Equal(t, http.StatusOK, rec.Code, "the return leg carries no query token; the cookie must satisfy the gate")
}

func TestOnboard_TokenGate_ForgedCookieRejected(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	mux := newTokenGatedOnboardMux(t, f, setupTokenForTest)

	r := httptest.NewRequest(http.MethodGet, "/onboard", nil)
	r.AddCookie(&http.Cookie{Name: handlers.SetupCookieName, Value: setupTokenForTest})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	assert.Equal(t, http.StatusForbidden, rec.Code, "an unsigned cookie must not pass the gate")
}

func TestOnboard_TokenGate_HiddenFieldCarriesTokenToPost(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	mux := newTokenGatedOnboardMux(t, f, setupTokenForTest)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/onboard?token="+url.QueryEscape(setupTokenForTest), nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `name="token"`, "the rendered form must carry the token to the POST")
	assert.Contains(t, rec.Body.String(), setupTokenForTest)
}
