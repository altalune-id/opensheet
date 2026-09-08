package gworkspace

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

const (
	testRefreshToken = "1//refresh-token-do-not-log"
	testAccountEmail = "picker@example.com"
)

type tokenResponse struct {
	status       int
	refreshToken string
	email        string
	noIDToken    bool
	rawIDToken   string
	scope        string
	noScope      bool
}

func idToken(t *testing.T, email string) string {
	t.Helper()
	enc := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal id_token part: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	header := enc(map[string]string{"alg": "RS256", "kid": "test"})
	claims := enc(map[string]string{"iss": "https://accounts.google.com", "email": email})
	return header + "." + claims + ".not-a-real-signature"
}

func tokenEndpoint(t *testing.T, resp tokenResponse) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if resp.status != 0 && resp.status != http.StatusOK {
			w.WriteHeader(resp.status)
			_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
		body := map[string]any{
			"access_token": "access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		}
		if resp.refreshToken != "" {
			body["refresh_token"] = resp.refreshToken
		}
		if resp.rawIDToken != "" {
			body["id_token"] = resp.rawIDToken
		}
		if !resp.noIDToken && resp.rawIDToken == "" {
			body["id_token"] = idToken(t, resp.email)
		}
		if !resp.noScope {
			body["scope"] = resp.scope
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Errorf("encode token response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func oauthConfig(tokenURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     "client-id.apps.googleusercontent.com",
		ClientSecret: "client-secret",
		RedirectURL:  "https://opensheet.test/app/credentials/google/callback",
		Scopes:       []string{"https://www.googleapis.com/auth/spreadsheets.readonly"},
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
			TokenURL: tokenURL,
		},
	}
}

func newConnector(t *testing.T, resp tokenResponse, scopes []string, opts ...Option) *Connector {
	t.Helper()
	if resp.email == "" {
		resp.email = testAccountEmail
	}
	if resp.refreshToken == "" && resp.status == 0 {
		resp.refreshToken = testRefreshToken
	}
	if resp.scope == "" && !resp.noScope {
		resp.scope = strings.Join(Scopes(), " ")
	}
	c, err := NewConnector(oauthConfig(tokenEndpoint(t, resp).URL), scopes, opts...)
	if err != nil {
		t.Fatalf("NewConnector: %v", err)
	}
	return c
}

func TestScopes(t *testing.T) {
	got := Scopes()
	want := []string{"https://www.googleapis.com/auth/drive.file", "email"}
	if !slices.Equal(got, want) {
		t.Fatalf("Scopes() = %v, want %v", got, want)
	}
	got[0] = "mutated"
	if Scopes()[0] != want[0] {
		t.Fatal("Scopes() hands out a shared slice")
	}
}

func TestNewConnector_RejectsAnUnusableClient(t *testing.T) {
	tests := map[string]*oauth2.Config{
		"nil config":   nil,
		"no client id": {ClientSecret: "client-secret"},
		"no secret":    {ClientID: "client-id.apps.googleusercontent.com"},
	}
	for name, cfg := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := NewConnector(cfg, nil); err == nil {
				t.Fatal("NewConnector accepted an unusable oauth client")
			}
		})
	}
}

func TestConnector_AuthCodeURL(t *testing.T) {
	c := newConnector(t, tokenResponse{}, nil)

	raw := c.AuthCodeURL("opaque|state")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if u.Host != "accounts.google.com" || u.Path != "/o/oauth2/v2/auth" {
		t.Errorf("auth endpoint = %s", u)
	}
	q := u.Query()
	if got := q.Get("state"); got != "opaque|state" {
		t.Errorf("state = %q, want it carried verbatim", got)
	}
	if got := q.Get("scope"); got != "https://www.googleapis.com/auth/drive.file email" {
		t.Errorf("scope = %q", got)
	}
	if got := q.Get("access_type"); got != "offline" {
		t.Errorf("access_type = %q, want offline", got)
	}
	if got := q.Get("prompt"); got != "consent" {
		t.Errorf("prompt = %q, want consent", got)
	}
	if got := q.Get("response_type"); got != "code" {
		t.Errorf("response_type = %q", got)
	}
	if got := q.Get("redirect_uri"); got != "https://opensheet.test/app/credentials/google/callback" {
		t.Errorf("redirect_uri = %q", got)
	}
	if strings.Contains(q.Get("scope"), "spreadsheets") {
		t.Error("the sensitive spreadsheets scope leaked in from the oauth config")
	}
}

func TestConnector_InjectedScopesArePinnedOnTheAuthorizeURL(t *testing.T) {
	scopeParam := func(t *testing.T, c *Connector) string {
		t.Helper()
		u, err := url.Parse(c.AuthCodeURL("state"))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		return u.Query().Get("scope")
	}

	wider := append(Scopes(), "https://www.googleapis.com/auth/documents.readonly")

	if got := scopeParam(t, newConnector(t, tokenResponse{}, wider)); got != strings.Join(wider, " ") {
		t.Errorf("scope = %q, want the injected set", got)
	}
	if got := scopeParam(t, newConnector(t, tokenResponse{}, nil)); got != strings.Join(Scopes(), " ") {
		t.Errorf("scope = %q, want the default set when nothing is injected", got)
	}

	c := newConnector(t, tokenResponse{}, wider)
	wider[0] = "https://www.googleapis.com/auth/drive"
	if got := scopeParam(t, c); !strings.HasPrefix(got, "https://www.googleapis.com/auth/drive.file ") {
		t.Errorf("scope = %q; mutating the caller's slice changed the pinned set", got)
	}
}

func TestConnector_Exchange(t *testing.T) {
	// SECURITY: the refresh token must reach the caller only through Grant, never through an error or a URL.
	t.Run("reports the whole grant", func(t *testing.T) {
		c := newConnector(t, tokenResponse{}, nil)

		g, err := c.Exchange(t.Context(), "auth-code")
		if err != nil {
			t.Fatalf("Exchange: %v", err)
		}
		if g.RefreshToken != testRefreshToken {
			t.Errorf("RefreshToken = %q", g.RefreshToken)
		}
		if g.AccessToken != "access-token" {
			t.Errorf("AccessToken = %q", g.AccessToken)
		}
		if g.Expiry.IsZero() {
			t.Error("Expiry is zero; the token lifetime was dropped")
		}
		if g.AccountEmail != testAccountEmail {
			t.Errorf("AccountEmail = %q", g.AccountEmail)
		}
		if !slices.Equal(g.GrantedScopes, Scopes()) {
			t.Errorf("GrantedScopes = %v, want %v", g.GrantedScopes, Scopes())
		}
	})

	t.Run("surfaces the scopes the user actually granted", func(t *testing.T) {
		c := newConnector(t, tokenResponse{scope: "email  extra"}, nil)

		g, err := c.Exchange(t.Context(), "auth-code")
		if err != nil {
			t.Fatalf("Exchange: %v", err)
		}
		if !slices.Equal(g.GrantedScopes, []string{"email", "extra"}) {
			t.Errorf("GrantedScopes = %v", g.GrantedScopes)
		}
	})

	t.Run("a response with no scope field grants nothing observable", func(t *testing.T) {
		c := newConnector(t, tokenResponse{noScope: true}, nil)

		g, err := c.Exchange(t.Context(), "auth-code")
		if err != nil {
			t.Fatalf("Exchange: %v", err)
		}
		if g.GrantedScopes != nil {
			t.Errorf("GrantedScopes = %v, want nil", g.GrantedScopes)
		}
	})

	t.Run("a blank refresh token is reported as absent", func(t *testing.T) {
		c := newConnector(t, tokenResponse{refreshToken: " ", status: http.StatusOK}, nil)

		g, err := c.Exchange(t.Context(), "auth-code")
		if err != nil {
			t.Fatalf("Exchange: %v", err)
		}
		if g.RefreshToken != "" {
			t.Errorf("RefreshToken = %q, want empty", g.RefreshToken)
		}
	})

	t.Run("a missing or unreadable id_token still grants", func(t *testing.T) {
		tests := map[string]tokenResponse{
			"no id_token":        {noIDToken: true},
			"not a jwt":          {rawIDToken: "opaque-token"},
			"payload not base64": {rawIDToken: "aGVhZGVy.!!!not-base64!!!.sig"},
			"payload not json": {
				rawIDToken: "aGVhZGVy." + base64.RawURLEncoding.EncodeToString([]byte("nope")) + ".sig",
			},
		}
		for name, resp := range tests {
			t.Run(name, func(t *testing.T) {
				g, err := newConnector(t, resp, nil).Exchange(t.Context(), "auth-code")
				if err != nil {
					t.Fatalf("Exchange: %v", err)
				}
				if g.AccountEmail != "" {
					t.Errorf("AccountEmail = %q, want empty", g.AccountEmail)
				}
				if g.RefreshToken != testRefreshToken {
					t.Error("a missing account must not cost the refresh token")
				}
			})
		}
	})

	t.Run("google rejecting the code is unavailable, not a leak", func(t *testing.T) {
		c := newConnector(t, tokenResponse{status: http.StatusBadRequest}, nil)

		_, err := c.Exchange(t.Context(), "stale-code")
		if !IsUnavailableError(err) {
			t.Fatalf("error = %T %v, want *UnavailableError", err, err)
		}
		if strings.Contains(err.Error(), "client-secret") {
			t.Error("the client secret leaked into the error")
		}
	})

	t.Run("the exchange is bounded by its timeout", func(t *testing.T) {
		c := newConnector(t, tokenResponse{}, nil, WithTimeout(time.Nanosecond))

		if _, err := c.Exchange(t.Context(), "auth-code"); !IsUnavailableError(err) {
			t.Fatalf("error = %T %v, want *UnavailableError", err, err)
		}
	})
}

func TestNewConnector_DefaultsToTheExchangeTimeout(t *testing.T) {
	c := newConnector(t, tokenResponse{}, nil)
	if c.timeout != exchangeTimeout {
		t.Fatalf("timeout = %v, want %v", c.timeout, exchangeTimeout)
	}
	if got := newConnector(t, tokenResponse{}, nil, WithTimeout(time.Second)).timeout; got != time.Second {
		t.Fatalf("timeout = %v, want 1s", got)
	}
}
