package tokens_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

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

type stubIssuer struct {
	url  string
	priv ed25519.PrivateKey
}

func newStubIssuer(t *testing.T) *stubIssuer {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	iss := &stubIssuer{priv: priv}

	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"issuer":%q,"jwks_uri":%q,"id_token_signing_alg_values_supported":["EdDSA"]}`,
			srv.URL, srv.URL+"/jwks")
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"keys":[{"kty":"OKP","crv":"Ed25519","kid":"k1","alg":"EdDSA","use":"sig","x":%q}]}`,
			base64.RawURLEncoding.EncodeToString(pub))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	iss.url = srv.URL
	return iss
}

func (i *stubIssuer) mint(t *testing.T, audience string, claims map[string]any) string {
	t.Helper()

	payload := map[string]any{
		"iss": i.url,
		"aud": audience,
		"sub": "sub-1",
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	maps.Copy(payload, claims)

	seg := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	signing := seg(map[string]any{"alg": "EdDSA", "typ": "JWT", "kid": "k1"}) + "." + seg(payload)
	return signing + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(i.priv, []byte(signing)))
}

func TestVerify_OrgClaimIsAHintNotAuthority(t *testing.T) {
	iss := newStubIssuer(t)
	v, err := tokens.NewVerifier(t.Context(), tokens.Config{Issuer: iss.url, Audience: "urn:test"})
	if err != nil {
		t.Fatal(err)
	}
	orgID := uuid.New()

	tests := []struct {
		name        string
		claims      map[string]any
		wantErr     bool
		wantClaimed uuid.UUID
	}{
		{
			name:        "org_id lands on ClaimedOrgID and leaves ActiveOrgID zero",
			claims:      map[string]any{"org_id": orgID.String()},
			wantClaimed: orgID,
		},
		{
			name:        "absent org_id claims nothing",
			claims:      nil,
			wantClaimed: uuid.Nil,
		},
		{
			name:    "org_id that is not a uuid is refused",
			claims:  map[string]any{"org_id": "acme-corp"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := v.Verify(t.Context(), iss.mint(t, "urn:test", tt.claims))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected refusal, got principal %+v", p)
				}
				if !tokens.IsInvalidTokenError(err) {
					t.Fatalf("want *InvalidTokenError, got %T: %v", err, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if p.ClaimedOrgID != tt.wantClaimed {
				t.Fatalf("ClaimedOrgID = %v, want %v", p.ClaimedOrgID, tt.wantClaimed)
			}
			if p.ActiveOrgID != uuid.Nil {
				t.Fatalf("ActiveOrgID = %v; a token must not name the active tenant", p.ActiveOrgID)
			}
		})
	}
}
