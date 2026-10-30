package tokens

import (
	"context"

	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
)

// Authenticator adapts a Verifier to authn.Authenticator, so JWT verification is one link in an authn.Chain.
type Authenticator struct {
	v      Verifier
	scheme authn.Scheme
}

// NewAuthenticator returns an authn.Authenticator backed by v that dispatches by scheme's classification of each credential.
func NewAuthenticator(v Verifier, scheme authn.Scheme) *Authenticator {
	return &Authenticator{v: v, scheme: scheme}
}

var _ authn.Authenticator = (*Authenticator)(nil)

// Authenticate implements authn.Authenticator by verifying raw as an access token or personal token. SECURITY: only a JWT or a personal token reaches the Verifier, so an app API key never leaves the process.
func (a *Authenticator) Authenticate(ctx context.Context, raw string) (session.Principal, error) {
	if shape := a.scheme.Looks(raw); shape != authn.ShapeJWT && shape != authn.ShapePersonalToken {
		return session.Principal{}, &InvalidTokenError{Reason: "neither an access token nor a personal token"}
	}
	return a.v.Verify(ctx, raw)
}
