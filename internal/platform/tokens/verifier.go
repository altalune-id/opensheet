// Package tokens verifies the Othra access tokens of one resource server through oauthrs.
package tokens

import (
	"context"
	"crypto/subtle"
	"errors"
	"hash/crc32"
	"net/http"
	"strings"

	"github.com/altalune-id/oauthrs"
	"github.com/altalune-id/oidcrp"

	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
)

// PersonalTokenPrefix starts every Othra personal token, the only opaque token sent to introspection.
const PersonalTokenPrefix = "othra_pt_" //nolint:gosec // G101: a public token prefix, not a credential.

const (
	base62           = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	personalTokenLen = 43
	personalSumLen   = 6
)

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
			Precheck: personalTokenShape, MaxPerMinute: cfg.Introspection.MaxPerMinute,
		}
	}
	rs, err := oauthrs.New(rsCfg)
	if err != nil {
		return nil, err
	}
	return &verifier{rs: rs}, nil
}

func personalTokenShape(token string) bool {
	body, ok := strings.CutPrefix(token, PersonalTokenPrefix)
	if !ok || len(body) != personalTokenLen+personalSumLen || strings.Trim(body, base62) != "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(body[personalTokenLen:]), []byte(personalTokenSum(body[:personalTokenLen]))) == 1
}

func personalTokenSum(body string) string {
	n := crc32.ChecksumIEEE([]byte(body))
	out := make([]byte, personalSumLen)
	for i := personalSumLen - 1; i >= 0; i-- {
		out[i] = base62[n%62]
		n /= 62
	}
	return string(out)
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
	case errors.Is(err, oauthrs.ErrIntrospectionBudget):
		return &authn.UnavailableError{Cause: err}
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
