package tokens_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"altalune.id/template/internal/platform/tokens"
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
