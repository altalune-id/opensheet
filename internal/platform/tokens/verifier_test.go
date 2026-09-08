package tokens_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tokens"
)

func TestNewVerifier_Disabled(t *testing.T) {
	v, err := tokens.NewVerifier(context.Background(), tokens.Config{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = v.Verify(context.Background(), "anything")
	if err == nil {
		t.Fatal("disabled verifier must reject all tokens")
	}
	if !tokens.IsInvalidTokenError(err) {
		t.Fatalf("disabled verifier should return *InvalidTokenError, got %T: %v", err, err)
	}
}

func TestNewVerifier_MissingAudience(t *testing.T) {
	_, err := tokens.NewVerifier(context.Background(), tokens.Config{Issuer: "https://x", Audience: ""})
	if err == nil {
		t.Fatal("expected error: audience required")
	}
}

func TestNewVerifier_UnreachableIssuer(t *testing.T) {
	_, err := tokens.NewVerifier(context.Background(), tokens.Config{Issuer: "https://127.0.0.1:1", Audience: "aud"})
	if err == nil {
		t.Fatal("expected discovery error against unreachable issuer")
	}
}

func TestNewVerifier_HappyDiscovery(t *testing.T) {
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"issuer":%q,"jwks_uri":%q,"id_token_signing_alg_values_supported":["EdDSA"]}`,
			srv.URL, srv.URL+"/jwks")
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"keys":[]}`)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	v, err := tokens.NewVerifier(context.Background(), tokens.Config{
		Issuer:      srv.URL,
		Audience:    "urn:test",
		ClockSkew:   5 * time.Second,
		AcceptRS256: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = v.Verify(context.Background(), "not-a-jwt")
	if err == nil {
		t.Fatal("expected verification failure for malformed token")
	}
	if !tokens.IsInvalidTokenError(err) {
		t.Fatalf("malformed token should surface as *InvalidTokenError, got %T: %v", err, err)
	}
}

func TestVerify_CarriesOrgIDClaimIntoPrincipal(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"issuer":%q,"jwks_uri":%q,"id_token_signing_alg_values_supported":["EdDSA"]}`,
			srv.URL, srv.URL+"/jwks")
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"keys":[{"kty":"OKP","crv":"Ed25519","alg":"EdDSA","use":"sig","kid":"k1","x":%q}]}`,
			base64.RawURLEncoding.EncodeToString(pub))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	v, err := tokens.NewVerifier(t.Context(), tokens.Config{Issuer: srv.URL, Audience: "urn:test"})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	claims := map[string]any{
		"iss":    srv.URL,
		"aud":    "urn:test",
		"sub":    "sub-1",
		"exp":    now.Add(time.Hour).Unix(),
		"iat":    now.Unix(),
		"email":  "member@example.com",
		"name":   "Member",
		"org_id": "9b2b1a3e-0000-7000-8000-000000000001",
		"scope":  "sheets:read sheets:write",
	}
	p, err := v.Verify(t.Context(), signEdDSA(t, priv, claims))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if p.IDPOrgID != "9b2b1a3e-0000-7000-8000-000000000001" {
		t.Errorf("IDPOrgID=%q want the org_id claim", p.IDPOrgID)
	}
	if p.IDPIssuer != srv.URL || p.IDPSubject != "sub-1" {
		t.Errorf("idp fields=%q/%q", p.IDPIssuer, p.IDPSubject)
	}
	if p.Email != "member@example.com" || p.Source != session.SourceToken {
		t.Errorf("principal=%+v", p)
	}
	if len(p.Scopes) != 2 {
		t.Errorf("Scopes=%v want the two space-separated scopes", p.Scopes)
	}
}

func TestVerify_AbsentOrgIDClaimLeavesIDPOrgIDEmpty(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"issuer":%q,"jwks_uri":%q,"id_token_signing_alg_values_supported":["EdDSA"]}`,
			srv.URL, srv.URL+"/jwks")
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"keys":[{"kty":"OKP","crv":"Ed25519","alg":"EdDSA","use":"sig","kid":"k1","x":%q}]}`,
			base64.RawURLEncoding.EncodeToString(pub))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	v, err := tokens.NewVerifier(t.Context(), tokens.Config{Issuer: srv.URL, Audience: "urn:test"})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	p, err := v.Verify(t.Context(), signEdDSA(t, priv, map[string]any{
		"iss":    srv.URL,
		"aud":    "urn:test",
		"sub":    "sub-2",
		"exp":    now.Add(time.Hour).Unix(),
		"iat":    now.Unix(),
		"scopes": []string{"cache:purge"},
	}))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if p.IDPOrgID != "" {
		t.Errorf("IDPOrgID=%q want empty", p.IDPOrgID)
	}
	if len(p.Scopes) != 1 || p.Scopes[0] != "cache:purge" {
		t.Errorf("Scopes=%v want the scopes array claim", p.Scopes)
	}
}

func signEdDSA(t *testing.T, priv ed25519.PrivateKey, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "JWT", "kid": "k1"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	return input + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, []byte(input)))
}
