package authl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestSanitizeReturnTo(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"/", "/"},
		{"/dashboard", "/dashboard"},
		{"//evil.example", ""},
		{"/\\evil.example", ""},
		{"http://evil.example", ""},
		{"/path\\to", "/path/to"},
		{"relative", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, sanitizeReturnTo(tc.in))
		})
	}
}

func TestResponseWritten(t *testing.T) {
	rec := httptest.NewRecorder()
	assert.False(t, responseWritten(rec))
	rec.Header().Set("Content-Type", "text/plain")
	assert.True(t, responseWritten(rec))

	rec2 := httptest.NewRecorder()
	rec2.Header().Set("Location", "/x")
	assert.True(t, responseWritten(rec2))
}

func handlerTestClient(t *testing.T) *Client {
	t.Helper()
	c := newTestClient(t)
	c.oauth = &oauth2.Config{
		ClientID:    "cid",
		Endpoint:    oauth2.Endpoint{AuthURL: "https://issuer.example/auth"},
		Scopes:      []string{"openid"},
		RedirectURL: "https://example.com/cb",
	}
	return c
}

func TestAuthorizeURL_IncludesPKCEAndParams(t *testing.T) {
	c := handlerTestClient(t)
	c.cfg.Resource = "https://api.example"
	p, err := newPKCE()
	require.NoError(t, err)

	got := c.authorizeURL(p, "select_account", "alice@example.com")
	u, err := url.Parse(got)
	require.NoError(t, err)
	q := u.Query()

	assert.Equal(t, p.state, q.Get("state"))
	assert.Equal(t, p.challenge, q.Get("code_challenge"))
	assert.Equal(t, "S256", q.Get("code_challenge_method"))
	assert.Equal(t, p.nonce, q.Get("nonce"))
	assert.Equal(t, "select_account", q.Get("prompt"))
	assert.Equal(t, "alice@example.com", q.Get("login_hint"))
	assert.Equal(t, "https://api.example", q.Get("resource"))
}

func TestAuthorizeURL_OmitsEmptyOptionals(t *testing.T) {
	c := handlerTestClient(t)
	p, err := newPKCE()
	require.NoError(t, err)

	got := c.authorizeURL(p, "  ", "")
	u, err := url.Parse(got)
	require.NoError(t, err)
	q := u.Query()

	assert.Empty(t, q.Get("prompt"))
	assert.Empty(t, q.Get("login_hint"))
	assert.Empty(t, q.Get("resource"))
}

func TestStartHandler_SetsCookieAndRedirects(t *testing.T) {
	c := handlerTestClient(t)
	h := c.StartHandler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/start?return_to=/home&switch=1", nil)

	h.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	assert.Equal(t, http.StatusFound, res.StatusCode)

	loc := res.Header.Get("Location")
	assert.True(t, strings.HasPrefix(loc, "https://issuer.example/auth"), "loc = %q", loc)
	u, err := url.Parse(loc)
	require.NoError(t, err)
	assert.Equal(t, "select_account", u.Query().Get("prompt"))

	cookies := res.Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, "authl_state", cookies[0].Name)
	assert.NotEmpty(t, cookies[0].Value)
}

func TestStartHandler_ForcesPromptWhenNoLastUser(t *testing.T) {
	c := handlerTestClient(t)
	c.cfg.RememberLastUser = true
	h := c.StartHandler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/start", nil)
	h.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	loc := res.Header.Get("Location")
	u, err := url.Parse(loc)
	require.NoError(t, err)
	assert.Equal(t, "select_account", u.Query().Get("prompt"))
}

func TestStartHandler_UsesLoginHint(t *testing.T) {
	c := handlerTestClient(t)
	c.cfg.RememberLastUser = true
	h := c.StartHandler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/start", nil)
	req.AddCookie(&http.Cookie{Name: "authl_last_user", Value: "alice@example.com"})
	h.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	loc := res.Header.Get("Location")
	u, err := url.Parse(loc)
	require.NoError(t, err)
	assert.Equal(t, "alice@example.com", u.Query().Get("login_hint"))
	assert.Empty(t, u.Query().Get("prompt"))
}

func TestCallbackHandler_PanicsOnNilOnComplete(t *testing.T) {
	c := handlerTestClient(t)
	assert.Panics(t, func() { c.CallbackHandler(nil) })
}

func noopOnComplete(_ context.Context, _ http.ResponseWriter, _ *http.Request, _ *Identity) error {
	return nil
}

func TestCallbackHandler_MissingStateCookie(t *testing.T) {
	c := handlerTestClient(t)
	h := c.CallbackHandler(noopOnComplete)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/cb?code=x&state=y", nil)
	h.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestCallbackHandler_InvalidStateCookie(t *testing.T) {
	c := handlerTestClient(t)
	h := c.CallbackHandler(noopOnComplete)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/cb?code=x&state=y", nil)
	req.AddCookie(&http.Cookie{Name: "authl_state", Value: "garbage"})
	h.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
	assert.NotEmpty(t, res.Cookies())
}

func TestCallbackHandler_StateMismatch(t *testing.T) {
	c := handlerTestClient(t)
	p, err := newPKCE()
	require.NoError(t, err)
	cookie, err := c.encodeStateCookie(p, "")
	require.NoError(t, err)

	h := c.CallbackHandler(noopOnComplete)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/cb?code=x&state=nope", nil)
	req.AddCookie(&http.Cookie{Name: "authl_state", Value: cookie})
	h.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestCallbackHandler_IssMismatch(t *testing.T) {
	c := handlerTestClient(t)
	c.cfg.Issuer = "https://issuer.example"
	c.issParameterSupported = true
	p, err := newPKCE()
	require.NoError(t, err)
	cookie, err := c.encodeStateCookie(p, "")
	require.NoError(t, err)

	h := c.CallbackHandler(noopOnComplete)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/cb?code=x&state="+p.state+"&iss=https://other", nil)
	req.AddCookie(&http.Cookie{Name: "authl_state", Value: cookie})
	h.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestCallbackHandler_ProviderError(t *testing.T) {
	c := handlerTestClient(t)
	p, err := newPKCE()
	require.NoError(t, err)
	cookie, err := c.encodeStateCookie(p, "")
	require.NoError(t, err)

	h := c.CallbackHandler(noopOnComplete)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/cb?state="+p.state+"&error=access_denied&error_description=nope", nil)
	req.AddCookie(&http.Cookie{Name: "authl_state", Value: cookie})
	h.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestCallbackHandler_MissingCode(t *testing.T) {
	c := handlerTestClient(t)
	p, err := newPKCE()
	require.NoError(t, err)
	cookie, err := c.encodeStateCookie(p, "")
	require.NoError(t, err)

	h := c.CallbackHandler(noopOnComplete)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/cb?state="+p.state, nil)
	req.AddCookie(&http.Cookie{Name: "authl_state", Value: cookie})
	h.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestLogoutHandler_RedirectsToDefault(t *testing.T) {
	c := handlerTestClient(t)
	h := c.LogoutHandler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/logout", nil)
	h.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	assert.Equal(t, http.StatusSeeOther, res.StatusCode)
	assert.Equal(t, "/", res.Header.Get("Location"))
}

func TestLogoutHandler_RedirectsToSanitizedReturnTo(t *testing.T) {
	c := handlerTestClient(t)
	h := c.LogoutHandler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/logout?return_to=/home", nil)
	h.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	assert.Equal(t, "/home", res.Header.Get("Location"))
}

func TestLogoutHandler_RejectsExternalReturnTo(t *testing.T) {
	c := handlerTestClient(t)
	h := c.LogoutHandler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/logout?return_to=http://evil.example", nil)
	h.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	assert.Equal(t, "/", res.Header.Get("Location"))
}

func TestWriteErr_WritesRedactedBody(t *testing.T) {
	c := handlerTestClient(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	c.writeErr(rec, req, http.StatusBadGateway, "test-title", assert.AnError)

	res := rec.Result()
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadGateway, res.StatusCode)
	assert.Contains(t, rec.Body.String(), "Sign-in failed")
	assert.NotContains(t, rec.Body.String(), assert.AnError.Error())
}
