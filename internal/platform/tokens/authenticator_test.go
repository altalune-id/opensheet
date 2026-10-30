package tokens_test

import (
	"context"
	"testing"

	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tokens"
)

type countingVerifier struct{ calls int }

func (c *countingVerifier) Verify(context.Context, string) (session.Principal, error) {
	c.calls++
	return session.Principal{Source: session.SourceToken}, nil
}

func TestSchemeLooksClassifiesEveryCredential(t *testing.T) {
	with := authn.Scheme{Prefix: "key_", TokenPrefix: tokens.PersonalTokenPrefix}
	without := authn.Scheme{Prefix: "key_"}
	tests := []struct {
		name   string
		scheme authn.Scheme
		raw    string
		want   authn.Shape
	}{
		{"api key", with, "key_abc", authn.ShapeAPIKey},
		{"api key with dots", with, "key_a.b.c", authn.ShapeAPIKey},
		{"personal token with introspection", with, tokens.PersonalTokenPrefix + "abc", authn.ShapePersonalToken},
		{"personal token without introspection", without, tokens.PersonalTokenPrefix + "abc", authn.ShapeUnknown},
		{"jwt", with, "aaa.bbb.ccc", authn.ShapeJWT},
		{"junk", with, "junk", authn.ShapeUnknown},
		{"empty", with, "", authn.ShapeUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.scheme.Looks(tt.raw); got != tt.want {
				t.Fatalf("Looks(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestAuthenticatorDispatchesByShape(t *testing.T) {
	with := authn.Scheme{Prefix: "key_", TokenPrefix: tokens.PersonalTokenPrefix}
	without := authn.Scheme{Prefix: "key_"}
	tests := []struct {
		name      string
		scheme    authn.Scheme
		raw       string
		wantCalls int
	}{
		{"api key", with, "key_secret", 0},
		{"dotted api key", with, "key_a.b.c", 0},
		{"junk", with, "junk", 0},
		{"jwt", with, "aaa.bbb.ccc", 1},
		{"personal token with introspection", with, tokens.PersonalTokenPrefix + "abc", 1},
		{"personal token without introspection", without, tokens.PersonalTokenPrefix + "abc", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &countingVerifier{}
			_, err := tokens.NewAuthenticator(v, tt.scheme).Authenticate(t.Context(), tt.raw)
			if v.calls != tt.wantCalls {
				t.Fatalf("verifier calls = %d, want %d: an API key must never reach the resource server", v.calls, tt.wantCalls)
			}
			if tt.wantCalls == 0 && !tokens.IsInvalidTokenError(err) {
				t.Fatalf("err = %v, want *InvalidTokenError", err)
			}
		})
	}
}
