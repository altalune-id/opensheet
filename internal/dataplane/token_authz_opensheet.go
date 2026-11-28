package dataplane

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/platform/tokens"
)

type identity interface {
	Authenticate(ctx context.Context, raw string) (session.Principal, error)
}

type binder interface {
	Bind(ctx context.Context, p session.Principal, tc tenant.Context) (context.Context, error)
}

type verifiedKey struct{}

type verifiedToken struct {
	raw string
	p   session.Principal
}

type credentialAuthorizer struct {
	keys   identity
	tokens identity
	bind   binder
	scheme authn.Scheme
}

// NewCredentialAuthorizer returns an Authorizer that sends API keys to keys and access tokens to people, then binds either to the path's tenant; a nil people refuses every token.
func NewCredentialAuthorizer(keys, people identity, b binder, keyScheme authn.Scheme) Authorizer {
	return &credentialAuthorizer{keys: keys, tokens: people, bind: b, scheme: keyScheme}
}

func (a *credentialAuthorizer) Authenticate(ctx context.Context, raw string) (session.Principal, error) {
	if a.scheme.Looks(raw) == authn.ShapeAPIKey {
		return a.keys.Authenticate(ctx, raw)
	}
	return a.person(ctx, raw)
}

func (a *credentialAuthorizer) Authorize(ctx context.Context, raw, scope string, orgID, projectID, resourceID uuid.UUID) (context.Context, session.Principal, error) {
	return a.authorize(ctx, raw, scope, tenant.Context{OrgID: orgID, ProjectID: projectID}, func(p session.Principal) bool {
		return p.ReachesResource(orgID, projectID, resourceID)
	})
}

func (a *credentialAuthorizer) AuthorizeProject(ctx context.Context, raw, scope string, orgID, projectID uuid.UUID) (context.Context, session.Principal, error) {
	return a.authorize(ctx, raw, scope, tenant.Context{OrgID: orgID, ProjectID: projectID}, func(p session.Principal) bool {
		return p.ReachesWholeProject(orgID, projectID)
	})
}

// SECURITY: the bound principal decides: its role in the path's org intersected with its scopes, and a key's own project and resource grant; resourceID narrows only keys.
func (a *credentialAuthorizer) authorize(ctx context.Context, raw, scope string, tc tenant.Context, reaches func(session.Principal) bool) (context.Context, session.Principal, error) {
	p, err := a.Authenticate(ctx, raw)
	if err != nil {
		return nil, session.Principal{}, err
	}
	tc.UserID = p.UserID
	bctx, err := a.bind.Bind(ctx, p, tc)
	if err != nil {
		return nil, session.Principal{}, err
	}
	p = session.PrincipalFrom(bctx)
	if !p.ReachesProject(tc.OrgID, tc.ProjectID) {
		return nil, session.Principal{}, &SheetNotFoundError{}
	}
	perm, ok := authz.PermissionFor(scope)
	if !ok || !reaches(p) {
		return nil, session.Principal{}, &authn.InsufficientScopeError{Scope: scope, Source: p.Source}
	}
	if err := authz.Can(bctx, perm); err != nil {
		return nil, session.Principal{}, err
	}
	return bctx, p, nil
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
