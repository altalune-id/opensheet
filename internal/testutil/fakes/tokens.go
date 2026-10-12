package fakes

import (
	"context"
	"slices"
	"sync"

	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tokens"
)

// TokenVerifier is an in-memory tokens.Verifier for tests, accepting a minted token only when its audience matches.
type TokenVerifier struct {
	mu       sync.Mutex
	audience string
	minted   map[string]grant
}

type grant struct {
	audience  string
	principal session.Principal
}

// NewTokenVerifier returns a TokenVerifier that accepts only tokens minted for audience.
func NewTokenVerifier(audience string) *TokenVerifier {
	return &TokenVerifier{audience: audience, minted: map[string]grant{}}
}

var _ tokens.Verifier = (*TokenVerifier)(nil)

// Mint records raw as a token for audience carrying scopes, as tokens.Verifier would report it.
func (f *TokenVerifier) Mint(raw, audience string, scopes ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.minted[raw] = grant{
		audience: audience,
		principal: session.Principal{
			Source:     session.SourceToken,
			IDPIssuer:  "https://issuer.example.com",
			IDPSubject: raw,
			Scopes:     slices.Clone(scopes),
		},
	}
}

// Verify implements tokens.Verifier.
func (f *TokenVerifier) Verify(_ context.Context, raw string) (session.Principal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g, ok := f.minted[raw]
	if !ok {
		return session.Principal{}, &tokens.InvalidTokenError{Reason: "unknown token"}
	}
	if g.audience != f.audience {
		return session.Principal{}, &tokens.InvalidTokenError{Reason: "audience mismatch"}
	}
	p := g.principal
	p.Scopes = slices.Clone(p.Scopes)
	return p, nil
}
