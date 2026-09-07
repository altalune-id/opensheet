package authl

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestApplyConfigDefaults_FillsZeroFields(t *testing.T) {
	cfg := Config{}
	applyConfigDefaults(&cfg)

	assert.Equal(t, []string{"openid", "profile", "email"}, cfg.Scopes)
	assert.Equal(t, "authl_last_user", cfg.LastUserCookie)
	assert.Equal(t, 90*24*time.Hour, cfg.LastUserMaxAge)
	assert.Equal(t, "authl_state", cfg.StateCookie)
	assert.Equal(t, 10*time.Minute, cfg.StateMaxAge)
	assert.Equal(t, "/", cfg.CookiePath)
	assert.Equal(t, oauth2.AuthStyleInHeader, cfg.TokenAuthStyle)
}

func TestApplyConfigDefaults_PreservesSetFields(t *testing.T) {
	cfg := Config{
		Scopes:         []string{"custom"},
		LastUserCookie: "u",
		LastUserMaxAge: 5 * time.Minute,
		StateCookie:    "s",
		StateMaxAge:    5 * time.Minute,
		CookiePath:     "/app",
		TokenAuthStyle: oauth2.AuthStyleInParams,
	}
	applyConfigDefaults(&cfg)

	assert.Equal(t, []string{"custom"}, cfg.Scopes)
	assert.Equal(t, "u", cfg.LastUserCookie)
	assert.Equal(t, 5*time.Minute, cfg.LastUserMaxAge)
	assert.Equal(t, "s", cfg.StateCookie)
	assert.Equal(t, 5*time.Minute, cfg.StateMaxAge)
	assert.Equal(t, "/app", cfg.CookiePath)
	assert.Equal(t, oauth2.AuthStyleInParams, cfg.TokenAuthStyle)
}

func TestMustParseURL_ParsesValid(t *testing.T) {
	u := mustParseURL("https://example.com/end?a=b")
	require.NotNil(t, u)
	assert.Equal(t, "https", u.Scheme)
	assert.Equal(t, "example.com", u.Host)
}

func TestMustParseURL_PanicsOnBadURL(t *testing.T) {
	assert.Panics(t, func() { mustParseURL("://bad url") })
}

func TestReadCookie_Missing(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	assert.Empty(t, readCookie(r, "missing"))
}

func TestReadCookie_Present(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "k", Value: "v"})
	assert.Equal(t, "v", readCookie(r, "k"))
}

func TestApplyConfigDefaults_FillsHTTPClient(t *testing.T) {
	cfg := Config{}
	applyConfigDefaults(&cfg)

	require.NotNil(t, cfg.HTTPClient)
	assert.Positive(t, cfg.HTTPClient.Timeout, "http.DefaultClient has no timeout, so a stalled OP would hang discovery forever")
}

func TestApplyConfigDefaults_PreservesHTTPClient(t *testing.T) {
	own := &http.Client{Timeout: time.Second}
	cfg := Config{HTTPClient: own}
	applyConfigDefaults(&cfg)

	assert.Same(t, own, cfg.HTTPClient)
}

func TestApplyConfigDefaults_HTTPClientReachesLoopbackOP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := Config{}
	applyConfigDefaults(&cfg)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
	require.NoError(t, err)
	resp, err := cfg.HTTPClient.Do(req)
	require.NoError(t, err, "a Dex or Keycloak on localhost must stay reachable")
	_ = resp.Body.Close()
}
