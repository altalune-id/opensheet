package tokens

import (
	"context"

	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
)

// Authenticator adapts a Verifier to authn.Authenticator, so JWT verification is one link in an authn.Chain.
type Authenticator struct {
	v Verifier
}

// NewAuthenticator returns an authn.Authenticator backed by v.
func NewAuthenticator(v Verifier) *Authenticator {
	return &Authenticator{v: v}
}

var _ authn.Authenticator = (*Authenticator)(nil)

// Authenticate implements authn.Authenticator by verifying raw as a bearer JWT.
func (a *Authenticator) Authenticate(ctx context.Context, raw string) (session.Principal, error) {
	return a.v.Verify(ctx, raw)
}
