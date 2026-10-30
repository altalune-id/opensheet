// Package tokens verifies the Othra access tokens of one resource server through oauthrs.
package tokens

import (
	"context"
	"errors"
	"net/http"

	"github.com/altalune-id/oauthrs"
	"github.com/altalune-id/oidcrp"

	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
)

// PersonalTokenPrefix starts every Othra personal token, the only opaque token sent to introspection.
const PersonalTokenPrefix = "othra_pt_" //nolint:gosec // G101: a public token prefix, not a credential.

// Verifier verifies a bearer token and returns the authenticated Principal.
type Verifier interface {
	Verify(ctx context.Context, bearer string) (session.Principal, error)
}

// NewKeySource returns the issuer's discovery and key cache, shared by the app's resource servers; nil without an issuer.
func NewKeySource(cfg Config, client *http.Client) *oidcrp.KeySource {
	if cfg.Issuer == "" {
		return nil
	}
	return oidcrp.NewKeySource(cfg.Issuer, client)
}

// NewVerifier builds the Verifier for cfg over keys, which nil builds; an empty Issuer refuses every token.
func NewVerifier(cfg Config, keys *oidcrp.KeySource) (Verifier, error) {
	if cfg.Issuer == "" {
		return disabledVerifier{}, nil
	}
	if cfg.Audience == "" {
		return nil, errors.New("tokens: audience required when issuer set")
	}
	rsCfg := oauthrs.Config{Issuer: cfg.Issuer, Audience: cfg.Audience, Keys: keys, ClockSkew: cfg.ClockSkew}
	if cfg.Introspection.Enabled() {
		// SECURITY: a negative cache TTL asks Othra on every call, so a revoked personal token stops at once.
		rsCfg.Introspection = &oauthrs.Introspection{
			ClientID: cfg.Introspection.ClientID, ClientSecret: cfg.Introspection.ClientSecret, TokenPrefix: PersonalTokenPrefix, CacheTTL: -1,
		}
	}
	rs, err := oauthrs.New(rsCfg)
	if err != nil {
		return nil, err
	}
	return &verifier{rs: rs}, nil
}

type verifier struct {
	rs *oauthrs.Verifier
}

func (v *verifier) Verify(ctx context.Context, raw string) (session.Principal, error) {
	tok, err := v.rs.Verify(ctx, raw)
	if err != nil {
		return session.Principal{}, refusal(err)
	}
	return session.Principal{
		Source:     session.SourceToken,
		IDPIssuer:  tok.Issuer,
		IDPSubject: tok.Subject,
		ClientID:   tok.ClientID,
		Scopes:     tok.Scopes,
	}, nil
}

// NOTE: a failure oauthrs does not type as a token problem is an issuer outage, so a client keeps its token.
func refusal(err error) error {
	switch {
	case oauthrs.IsExpiredTokenError(err):
		return &ExpiredTokenError{Cause: err}
	case oauthrs.IsInvalidTokenError(err), oauthrs.IsMissingTokenError(err), oauthrs.IsMalformedRequestError(err):
		return &InvalidTokenError{Reason: "refused by the resource server", Cause: err}
	}
	return &authn.UnavailableError{Cause: err}
}

type disabledVerifier struct{}

func (disabledVerifier) Verify(_ context.Context, _ string) (session.Principal, error) {
	return session.Principal{}, &InvalidTokenError{Reason: "verifier disabled (tokens.issuer empty)"}
}
