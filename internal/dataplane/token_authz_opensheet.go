package dataplane

import (
	"context"
	"net/http"
	"slices"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/platform/tokens"
)

type identity interface {
	Authenticate(ctx context.Context, raw string) (session.Principal, error)
}

type members interface {
	RequireMember(ctx context.Context, orgID, userID uuid.UUID) error
}

type verifiedKey struct{}

type verifiedToken struct {
	raw string
	p   session.Principal
}

type credentialAuthorizer struct {
	keys    Authorizer
	tokens  identity
	members members
	scheme  authn.Scheme
}

// NewCredentialAuthorizer returns an Authorizer that sends API keys to keys and access tokens to people; a nil people refuses every token.
func NewCredentialAuthorizer(keys Authorizer, people identity, m members, keyScheme authn.Scheme) Authorizer {
	return &credentialAuthorizer{keys: keys, tokens: people, members: m, scheme: keyScheme}
}

func (a *credentialAuthorizer) Authenticate(ctx context.Context, raw string) (session.Principal, error) {
	if a.scheme.Looks(raw) == authn.ShapeAPIKey {
		return a.keys.Authenticate(ctx, raw)
	}
	return a.person(ctx, raw)
}

func (a *credentialAuthorizer) Authorize(ctx context.Context, raw, scope string, orgID, projectID, resourceID uuid.UUID) (session.Principal, error) {
	if a.scheme.Looks(raw) == authn.ShapeAPIKey {
		return a.keys.Authorize(ctx, raw, scope, orgID, projectID, resourceID)
	}
	return a.authorizeToken(ctx, raw, scope, orgID, projectID)
}

func (a *credentialAuthorizer) AuthorizeProject(ctx context.Context, raw, scope string, orgID, projectID uuid.UUID) (session.Principal, error) {
	if a.scheme.Looks(raw) == authn.ShapeAPIKey {
		return a.keys.AuthorizeProject(ctx, raw, scope, orgID, projectID)
	}
	return a.authorizeToken(ctx, raw, scope, orgID, projectID)
}

// SECURITY: membership of the path's org admits the person and the token's scopes cap it; a token reaches every sheet of the project, since resourceID narrows only keys.
func (a *credentialAuthorizer) authorizeToken(ctx context.Context, raw, scope string, orgID, projectID uuid.UUID) (session.Principal, error) {
	p, err := a.person(ctx, raw)
	if err != nil {
		return session.Principal{}, err
	}
	if err := a.members.RequireMember(tenant.WithOrg(ctx, orgID), orgID, p.UserID); err != nil {
		return session.Principal{}, membershipRefusal(err)
	}
	if !slices.Contains(p.Scopes, scope) {
		return session.Principal{}, &authn.InsufficientScopeError{Scope: scope, Source: p.Source}
	}
	p.ActiveOrgID, p.ActiveProjectID = orgID, projectID
	return p, nil
}

// SECURITY: a JWT only; a personal token never reaches introspection from here, so forged ones cannot spend the issuer's budget.
func (a *credentialAuthorizer) person(ctx context.Context, raw string) (session.Principal, error) {
	if a.tokens == nil || a.scheme.Looks(raw) != authn.ShapeJWT {
		return session.Principal{}, &authn.UnauthorizedError{}
	}
	if v, ok := ctx.Value(verifiedKey{}).(verifiedToken); ok && v.raw == raw {
		return v.p, nil
	}
	return a.tokens.Authenticate(ctx, raw)
}

// SECURITY: a bearer JWT is verified before any path resolves, so an invalid, expired or unverifiable token gets one answer whatever the slug.
func (h *sheetHandler) verifyBearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := credentialOf(r)
		if (authn.Scheme{}).Looks(raw) != authn.ShapeJWT {
			next.ServeHTTP(w, r)
			return
		}
		p, err := h.authz.Authenticate(r.Context(), raw)
		switch {
		case err == nil:
			r = r.WithContext(context.WithValue(r.Context(), verifiedKey{}, verifiedToken{raw: raw, p: p}))
		case tokens.IsInvalidTokenError(err), tokens.IsExpiredTokenError(err):
			if h.challenge != "" {
				w.Header().Set("WWW-Authenticate", h.challenge)
			}
			h.fail(w, r, err)
			return
		case authn.IsUnavailableError(err):
			h.fail(w, r, refusal(err))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func membershipRefusal(err error) error {
	if ae, ok := apperror.AsAppError(err); ok && ae.Code() == apperror.CodeOrgMembershipMissing {
		return &authn.UnauthorizedError{}
	}
	return &authn.UnavailableError{Cause: err}
}
