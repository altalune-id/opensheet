package authl

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClient_ZeroValue(t *testing.T) {
	_ = new(Client)
}

func TestNewClient_EmptyIssuerReturnsNilNil(t *testing.T) {
	c, err := NewClient(context.Background(), Config{})
	require.NoError(t, err)
	assert.Nil(t, c)
}

func TestNewClient_WhitespaceIssuerReturnsNilNil(t *testing.T) {
	c, err := NewClient(context.Background(), Config{Issuer: "   "})
	require.NoError(t, err)
	assert.Nil(t, c)
}

func TestNewClient_ValidationErrors(t *testing.T) {
	base32 := make([]byte, 32)
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "missing client id",
			cfg:  Config{Issuer: "https://x", RedirectURL: "https://x/cb", StateSecret: base32},
			want: "ClientID is required",
		},
		{
			name: "whitespace client id",
			cfg:  Config{Issuer: "https://x", ClientID: "  ", RedirectURL: "https://x/cb", StateSecret: base32},
			want: "ClientID is required",
		},
		{
			name: "missing redirect",
			cfg:  Config{Issuer: "https://x", ClientID: "cid", StateSecret: base32},
			want: "RedirectURL is required",
		},
		{
			name: "http non-loopback",
			cfg:  Config{Issuer: "https://x", ClientID: "cid", RedirectURL: "http://example.com/cb", StateSecret: base32},
			want: "loopback",
		},
		{
			name: "localhost rejected",
			cfg:  Config{Issuer: "https://x", ClientID: "cid", RedirectURL: "http://localhost/cb", StateSecret: base32},
			want: "loopback",
		},
		{
			name: "ftp scheme",
			cfg:  Config{Issuer: "https://x", ClientID: "cid", RedirectURL: "ftp://x/cb", StateSecret: base32},
			want: "loopback",
		},
		{
			name: "unparseable redirect",
			cfg:  Config{Issuer: "https://x", ClientID: "cid", RedirectURL: "://bad", StateSecret: base32},
			want: "parse",
		},
		{
			name: "short state secret",
			cfg:  Config{Issuer: "https://x", ClientID: "cid", RedirectURL: "https://x/cb", StateSecret: []byte("too-short")},
			want: "StateSecret",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewClient(context.Background(), tc.cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestValidateRedirectURL_AcceptsLoopbackAndHTTPS(t *testing.T) {
	cases := []string{
		"https://example.com/cb",
		"http://127.0.0.1/cb",
		"http://127.0.0.1:5555/cb",
		"http://[::1]:5555/cb",
	}
	for _, u := range cases {
		t.Run(u, func(t *testing.T) {
			assert.NoError(t, validateRedirectURL(u))
		})
	}
}

func TestValidateRedirectURL_Rejects(t *testing.T) {
	cases := []string{
		"http://example.com/cb",
		"http://localhost/cb",
		"ftp://example.com/cb",
		"://bad",
	}
	for _, u := range cases {
		t.Run(u, func(t *testing.T) {
			assert.Error(t, validateRedirectURL(u))
		})
	}
}

func TestGenerateStateSecret_LengthAndDistinct(t *testing.T) {
	a, err := GenerateStateSecret()
	require.NoError(t, err)
	require.Len(t, a, 32)

	b, err := GenerateStateSecret()
	require.NoError(t, err)
	assert.NotEqual(t, a, b)
}

func TestGenerateStateSecretBase64_Decodable(t *testing.T) {
	s, err := GenerateStateSecretBase64()
	require.NoError(t, err)
	require.NotEmpty(t, s)

	decoded, err := base64.RawURLEncoding.DecodeString(s)
	require.NoError(t, err)
	assert.Len(t, decoded, 32)
}

func TestLastKnownUser_ReturnsCookieValue(t *testing.T) {
	c := &Client{cfg: Config{LastUserCookie: "authl_last_user"}}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	assert.Empty(t, c.LastKnownUser(req))

	req.AddCookie(&http.Cookie{Name: "authl_last_user", Value: "alice@example.com"})
	assert.Equal(t, "alice@example.com", c.LastKnownUser(req))
}

func TestClearRememberedUser_DisabledIsNoop(t *testing.T) {
	c := &Client{cfg: Config{
		LastUserCookie:   "authl_last_user",
		RememberLastUser: false,
		CookiePath:       "/",
	}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	c.ClearRememberedUser(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	assert.Empty(t, res.Cookies())
}

func TestClearRememberedUser_EnabledClearsCookie(t *testing.T) {
	c := &Client{cfg: Config{
		LastUserCookie:   "authl_last_user",
		RememberLastUser: true,
		CookiePath:       "/",
	}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	c.ClearRememberedUser(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	cookies := res.Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, -1, cookies[0].MaxAge)
}

func TestEndSessionURL_EmptyEndpoint(t *testing.T) {
	c := &Client{cfg: Config{ClientID: "cid"}}
	assert.Empty(t, c.EndSessionURL("idt", "https://post-logout", "state123"))
}

func TestEndSessionURL_BuildsFullURL(t *testing.T) {
	c := &Client{
		cfg:                Config{ClientID: "cid"},
		endSessionEndpoint: "https://issuer.example/end",
	}
	got := c.EndSessionURL("idt", "https://post-logout/", "state123")

	assert.True(t, strings.HasPrefix(got, "https://issuer.example/end?"))
	assert.Contains(t, got, "id_token_hint=idt")
	assert.Contains(t, got, "post_logout_redirect_uri=")
	assert.Contains(t, got, "state=state123")
	assert.Contains(t, got, "client_id=cid")
}

func TestEndSessionURL_OmitsEmptyParams(t *testing.T) {
	c := &Client{
		cfg:                Config{},
		endSessionEndpoint: "https://issuer.example/end",
	}
	got := c.EndSessionURL("", "", "")
	assert.Equal(t, "https://issuer.example/end", got)
}

func TestNewClient_DiscoveryFailurePropagates(t *testing.T) {
	base32 := make([]byte, 32)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err := NewClient(ctx, Config{
		Issuer:      "https://127.0.0.1:1/does-not-exist",
		ClientID:    "cid",
		RedirectURL: "https://example.com/cb",
		StateSecret: base32,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "discover")
}
