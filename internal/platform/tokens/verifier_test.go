package tokens_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/altalune-id/oidcrp/oidcrptest"

	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tokens"
)

const (
	apiAudience = "https://app.example.com/api"
	mcpAudience = "https://app.example.com/mcp"
)

func newVerifier(t *testing.T, idp *oidcrptest.IDP, cfg tokens.Config) tokens.Verifier {
	t.Helper()
	cfg.Issuer = idp.Issuer()
	if cfg.Audience == "" {
		cfg.Audience = apiAudience
	}
	v, err := tokens.NewVerifier(cfg, tokens.NewKeySource(cfg, idp.Client()))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestNewVerifier_Disabled(t *testing.T) {
	v, err := tokens.NewVerifier(tokens.Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(t.Context(), "aaa.bbb.ccc"); !tokens.IsInvalidTokenError(err) {
		t.Fatalf("disabled verifier: err = %v, want *InvalidTokenError", err)
	}
	if tokens.NewKeySource(tokens.Config{}, nil) != nil {
		t.Fatal("NewKeySource without an issuer must be nil")
	}
}

func TestNewVerifier_MissingAudience(t *testing.T) {
	if _, err := tokens.NewVerifier(tokens.Config{Issuer: "https://issuer.example.com"}, nil); err == nil {
		t.Fatal("expected an error: audience required")
	}
}

func TestVerify_MapsAnAccessTokenToATokenPrincipal(t *testing.T) {
	idp := oidcrptest.New(t)
	v := newVerifier(t, idp, tokens.Config{})
	raw := idp.AccessToken(map[string]any{"aud": apiAudience, "scope": "posts:read posts:write", "client_id": "cli", "email": "x@example.com"})

	p, err := v.Verify(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	want := session.Principal{Source: session.SourceToken, IDPIssuer: idp.Issuer(), IDPSubject: idp.User.Subject, ClientID: "cli"}
	if p.Source != want.Source || p.IDPIssuer != want.IDPIssuer || p.IDPSubject != want.IDPSubject || p.ClientID != want.ClientID {
		t.Fatalf("principal = %+v, want %+v", p, want)
	}
	if len(p.Scopes) != 2 || p.Scopes[0] != "posts:read" || p.Scopes[1] != "posts:write" {
		t.Fatalf("scopes = %v", p.Scopes)
	}
	if p.Email != "" || p.Name != "" {
		t.Fatalf("a bearer token must not supply the profile: %+v", p)
	}
}

func TestVerify_RefusesWhatIsNotAnAccessTokenForThisResource(t *testing.T) {
	idp := oidcrptest.New(t)
	v := newVerifier(t, idp, tokens.Config{})
	now := time.Now()
	tests := []struct {
		name string
		raw  string
	}{
		{"typ JWT with a valid aud", idp.IDToken(idp.AccessTokenClaims(map[string]any{"aud": apiAudience}))},
		{"an ID token used as an access token", idp.IDToken(map[string]any{
			"iss": idp.Issuer(), "sub": idp.User.Subject, "aud": "test-client", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
		})},
		{"an access token for another resource", idp.AccessToken(map[string]any{"aud": mcpAudience})},
		{"an access token from another issuer", idp.AccessToken(map[string]any{"aud": apiAudience, "iss": "https://other.example.com"})},
		{"not a token", "garbage"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := v.Verify(t.Context(), tt.raw); !tokens.IsInvalidTokenError(err) {
				t.Fatalf("err = %v, want *InvalidTokenError", err)
			}
		})
	}
}

func TestVerify_ExpiredTokenIsTyped(t *testing.T) {
	idp := oidcrptest.New(t)
	v := newVerifier(t, idp, tokens.Config{})
	past := time.Now().Add(-time.Hour)
	raw := idp.AccessToken(map[string]any{"aud": apiAudience, "iat": past.Add(-time.Minute).Unix(), "exp": past.Unix()})
	if _, err := v.Verify(t.Context(), raw); !tokens.IsExpiredTokenError(err) {
		t.Fatalf("err = %v, want *ExpiredTokenError", err)
	}
}

func TestVerify_PersonalTokenIsIntrospectedAndRevokedAtOnce(t *testing.T) {
	idp := oidcrptest.New(t)
	idp.Introspector("rs-api", "rs-secret")
	v := newVerifier(t, idp, tokens.Config{Introspection: tokens.IntrospectionConfig{ClientID: "rs-api", ClientSecret: "rs-secret"}})
	const pt = tokens.PersonalTokenPrefix + "abc"
	idp.Opaque(pt, map[string]any{"aud": apiAudience, "sub": idp.User.Subject, "scope": "posts:read", "exp": time.Now().Add(time.Hour).Unix()})

	p, err := v.Verify(t.Context(), pt)
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != session.SourceToken || p.IDPSubject != idp.User.Subject || len(p.Scopes) != 1 {
		t.Fatalf("principal = %+v", p)
	}
	idp.Revoke(pt)
	if _, err := v.Verify(t.Context(), pt); !tokens.IsInvalidTokenError(err) {
		t.Fatalf("a revoked personal token: err = %v, want *InvalidTokenError", err)
	}
	if got := idp.Introspections(); got != 2 {
		t.Fatalf("introspections = %d, want 2 (no cache)", got)
	}
}

func TestVerify_APIKeyIsNeverIntrospected(t *testing.T) {
	idp := oidcrptest.New(t)
	idp.Introspector("rs-api", "rs-secret")
	v := newVerifier(t, idp, tokens.Config{Introspection: tokens.IntrospectionConfig{ClientID: "rs-api", ClientSecret: "rs-secret"}})
	for _, raw := range []string{"altk_secretkeymaterial", "othra_other_abc"} {
		if _, err := v.Verify(t.Context(), raw); !tokens.IsInvalidTokenError(err) {
			t.Fatalf("Verify(%q) = %v, want *InvalidTokenError", raw, err)
		}
	}
	if got := idp.Introspections(); got != 0 {
		t.Fatalf("introspections = %d; an app credential must never reach the issuer", got)
	}
}

func TestVerify_PersonalTokenRefusedWithoutIntrospection(t *testing.T) {
	idp := oidcrptest.New(t)
	idp.Introspector("rs-api", "rs-secret")
	v := newVerifier(t, idp, tokens.Config{})
	if _, err := v.Verify(t.Context(), tokens.PersonalTokenPrefix+"abc"); !tokens.IsInvalidTokenError(err) {
		t.Fatalf("err = %v, want *InvalidTokenError", err)
	}
	if got := idp.Introspections(); got != 0 {
		t.Fatalf("introspections = %d, want 0", got)
	}
}

func TestVerify_AnIssuerOutageIsNotARefusal(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(down.Close)
	cfg := tokens.Config{
		Issuer: down.URL, Audience: apiAudience,
		Introspection: tokens.IntrospectionConfig{ClientID: "rs-api", ClientSecret: "rs-secret"},
	}
	v, err := tokens.NewVerifier(cfg, tokens.NewKeySource(cfg, down.Client()))
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"aaa.bbb.ccc", tokens.PersonalTokenPrefix + "abc"} {
		_, err := v.Verify(t.Context(), raw)
		if !authn.IsUnavailableError(err) {
			t.Fatalf("Verify(%q) = %v, want *authn.UnavailableError", raw, err)
		}
		if tokens.IsInvalidTokenError(err) || tokens.IsExpiredTokenError(err) {
			t.Fatalf("Verify(%q) = %v: an outage must not read as a bad token", raw, err)
		}
	}
}
