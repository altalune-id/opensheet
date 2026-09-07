// Package tokens verifies OAuth 2.0 access tokens (JWT) issued by altalune-auth.
package tokens

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"altalune.id/template/internal/platform/session"
)

// Verifier verifies a bearer token and returns the authenticated Principal.
type Verifier interface {
	Verify(ctx context.Context, bearer string) (session.Principal, error)
}

type oidcVerifier struct {
	v        *oidc.IDTokenVerifier
	audience string
}

// NewVerifier constructs a Verifier from cfg. Empty Issuer disables verification.
func NewVerifier(ctx context.Context, cfg Config) (Verifier, error) {
	if cfg.Issuer == "" {
		return disabledVerifier{}, nil
	}
	if cfg.Audience == "" {
		return nil, errors.New("tokens: audience required when issuer set")
	}
	prov, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("tokens: discover %s: %w", cfg.Issuer, err)
	}
	algs := cfg.SupportedAlgs
	if len(algs) == 0 {
		algs = []string{"EdDSA"}
	}
	if cfg.AcceptRS256 {
		algs = append(algs, "RS256")
	}
	oidcCfg := &oidc.Config{
		ClientID:             cfg.Audience,
		SupportedSigningAlgs: algs,
	}
	if cfg.ClockSkew > 0 {
		skew := cfg.ClockSkew
		oidcCfg.Now = func() time.Time { return time.Now().Add(-skew) }
	}
	return &oidcVerifier{
		v:        prov.Verifier(oidcCfg),
		audience: cfg.Audience,
	}, nil
}

func (o *oidcVerifier) Verify(ctx context.Context, raw string) (session.Principal, error) {
	tok, err := o.v.Verify(ctx, raw)
	if err != nil {
		return session.Principal{}, classifyVerifyError(err)
	}
	var claims struct {
		Email  string   `json:"email"`
		Name   string   `json:"name"`
		Scope  string   `json:"scope"`
		OrgID  string   `json:"org_id"`
		Scopes []string `json:"scopes"`
	}
	if err := tok.Claims(&claims); err != nil {
		return session.Principal{}, &InvalidTokenError{Reason: "malformed claims", Cause: err}
	}
	scopes := claims.Scopes
	if len(scopes) == 0 && claims.Scope != "" {
		scopes = splitScope(claims.Scope)
	}
	return session.Principal{
		Email:      claims.Email,
		Name:       claims.Name,
		Source:     session.SourceToken,
		IDPIssuer:  tok.Issuer,
		IDPSubject: tok.Subject,
		Scopes:     scopes,
		IssuedAt:   tok.IssuedAt,
	}, nil
}

type disabledVerifier struct{}

func (disabledVerifier) Verify(_ context.Context, _ string) (session.Principal, error) {
	return session.Principal{}, &InvalidTokenError{Reason: "verifier disabled (tokens.issuer empty)"}
}

func classifyVerifyError(err error) error {
	var exp *oidc.TokenExpiredError
	if errors.As(err, &exp) {
		return &ExpiredTokenError{Cause: err}
	}
	return &InvalidTokenError{Reason: err.Error(), Cause: err}
}

func splitScope(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' {
			if start < i {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
