package authn_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
)

func TestSchemeLooks(t *testing.T) {
	s := authn.Scheme{Prefix: "key_"}
	tests := []struct {
		name string
		raw  string
		want authn.Shape
	}{
		{"empty", "", authn.ShapeUnknown},
		{"api key", "key_abc123", authn.ShapeAPIKey},
		{"jwt", "aaa.bbb.ccc", authn.ShapeJWT},
		{"jwt with space is not a jwt", "aaa.bbb ccc", authn.ShapeUnknown},
		{"other prefix", "osk_abc", authn.ShapeUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := s.Looks(tt.raw); got != tt.want {
				t.Fatalf("Looks(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestSchemeLooksPersonalTokenOnlyWithItsPrefix(t *testing.T) {
	with := authn.Scheme{Prefix: "key_", TokenPrefix: "othra_pt_"}
	if got := with.Looks("othra_pt_abc"); got != authn.ShapePersonalToken {
		t.Fatalf("Looks(personal token) = %v, want ShapePersonalToken", got)
	}
	if got := with.Looks("key_abc"); got != authn.ShapeAPIKey {
		t.Fatalf("Looks(api key) = %v, want ShapeAPIKey", got)
	}
	without := authn.Scheme{Prefix: "key_"}
	if got := without.Looks("othra_pt_abc"); got != authn.ShapeUnknown {
		t.Fatalf("a personal token without introspection = %v, want ShapeUnknown", got)
	}
}

func TestZeroSchemeClassifiesJWTOnly(t *testing.T) {
	var s authn.Scheme
	if got := s.Looks("key_abc"); got != authn.ShapeUnknown {
		t.Fatalf("zero Scheme matched a prefix: %v", got)
	}
	if got := s.Looks("aaa.bbb.ccc"); got != authn.ShapeJWT {
		t.Fatalf("zero Scheme did not classify a JWT: %v", got)
	}
}

func TestCredentialFromHeader(t *testing.T) {
	tests := []struct{ name, header, value, want string }{
		{"bearer", "Authorization", "Bearer key_abc", "key_abc"},
		{"lowercase bearer", "Authorization", "bearer key_abc", "key_abc"},
		{"api key header", "X-API-Key", "key_abc", "key_abc"},
		{"basic is not taken", "Authorization", "Basic abc", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{}
			h.Set(tt.header, tt.value)
			if got := authn.CredentialFromHeader(h); got != tt.want {
				t.Fatalf("CredentialFromHeader = %q, want %q", got, tt.want)
			}
		})
	}
}

type failingAuth struct{ err error }

func (f failingAuth) Authenticate(context.Context, string) (session.Principal, error) {
	return session.Principal{}, f.err
}

func TestChainSeparatesAnOutageFromARefusal(t *testing.T) {
	outage := &authn.UnavailableError{Cause: errors.New("issuer unreachable")}
	refused := failingAuth{err: errors.New("bad signature")}
	tests := []struct {
		name            string
		chain           authn.Chain
		wantUnavailable bool
	}{
		{"every link refuses", authn.Chain{refused, refused}, false},
		{"one link is down", authn.Chain{refused, failingAuth{err: outage}}, true},
		{"a later link succeeds past an outage", authn.Chain{failingAuth{err: outage}, stubAuth{p: tokenPrincipal()}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.chain.Authenticate(t.Context(), "aaa.bbb.ccc")
			if got := authn.IsUnavailableError(err); got != tt.wantUnavailable {
				t.Fatalf("IsUnavailableError(%v) = %v, want %v", err, got, tt.wantUnavailable)
			}
			if tt.wantUnavailable && authn.IsUnauthorizedError(err) {
				t.Fatalf("an outage must not read as a refusal: %v", err)
			}
		})
	}
}
