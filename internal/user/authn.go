package user

import (
	"context"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
)

type orgMemberships interface {
	ListForUser(ctx context.Context, userID uuid.UUID) ([]*OrgRef, error)
}

type projectLister interface {
	ListByOrg(ctx context.Context, orgID uuid.UUID) ([]*ProjectRef, error)
}

// Authenticator wraps another authn.Authenticator and resolves a token principal's IdP identity to its local user, filling UserID and the fields only the local record can authoritatively supply.
type Authenticator struct {
	next     authn.Authenticator
	store    Store
	orgs     orgMemberships
	projects projectLister
}

// AuthenticatorOption configures an Authenticator.
type AuthenticatorOption func(*Authenticator)

// WithTenantResolution resolves a token principal's active org and project from the local user's memberships, the same way the web login path does.
func WithTenantResolution(orgs orgMemberships, projects projectLister) AuthenticatorOption {
	return func(a *Authenticator) {
		a.orgs = orgs
		a.projects = projects
	}
}

// NewAuthenticator binds an Authenticator to the authn.Authenticator it wraps and the Store it resolves against.
func NewAuthenticator(next authn.Authenticator, store Store, opts ...AuthenticatorOption) *Authenticator {
	a := &Authenticator{next: next, store: store}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

var _ authn.Authenticator = (*Authenticator)(nil)

// Authenticate implements authn.Authenticator. SECURITY: an unknown IdP identity is the opaque *authn.UnauthorizedError; a store failure is *authn.UnavailableError, so an outage never reads as a bad token.
func (a *Authenticator) Authenticate(ctx context.Context, raw string) (session.Principal, error) {
	p, err := a.next.Authenticate(ctx, raw)
	if err != nil {
		return session.Principal{}, err
	}
	if !needsIdentity(p) {
		return p, nil
	}
	u, err := a.store.ByIDP(ctx, p.IDPIssuer, p.IDPSubject)
	if IsNotFoundError(err) {
		return session.Principal{}, &authn.UnauthorizedError{}
	}
	if err != nil {
		return session.Principal{}, &authn.UnavailableError{Cause: err}
	}
	p.UserID = u.ID
	p.Email = u.Email
	p.Name = u.Name
	p.IsAdmin = u.IsAdmin
	p.Locale = u.Locale
	if u.TermsAcceptedAt != nil {
		p.TermsAcceptedAt = *u.TermsAcceptedAt
	}
	return a.resolveTenant(ctx, p)
}

// SECURITY: membership is the only authority for tenancy; a token never names its org.
func (a *Authenticator) resolveTenant(ctx context.Context, p session.Principal) (session.Principal, error) {
	if a.orgs == nil || p.ActiveOrgID != uuid.Nil {
		return p, nil
	}
	orgs, err := a.orgs.ListForUser(ctx, p.UserID)
	if err != nil {
		return session.Principal{}, &authn.UnavailableError{Cause: err}
	}
	orgID := pickOrg(orgs)
	if orgID == uuid.Nil {
		return p, nil
	}
	p.ActiveOrgID = orgID
	p.ActiveProjectID = a.pickProject(ctx, orgID)
	return p, nil
}

// NOTE: several memberships leave the principal untenanted, failing later with tenant.UnscopedError.
func pickOrg(orgs []*OrgRef) uuid.UUID {
	if len(orgs) == 1 {
		return orgs[0].ID
	}
	return uuid.Nil
}

// SECURITY: the principal holds no tenant scope yet, so bind to the org just resolved.
func (a *Authenticator) pickProject(ctx context.Context, orgID uuid.UUID) uuid.UUID {
	if a.projects == nil {
		return uuid.Nil
	}
	list, err := a.projects.ListByOrg(tenant.WithOrg(ctx, orgID), orgID)
	if err != nil || len(list) == 0 {
		return uuid.Nil
	}
	return list[0].ID
}

func needsIdentity(p session.Principal) bool {
	if p.Source == session.SourceAPIKey {
		return false
	}
	if p.UserID != uuid.Nil {
		return false
	}
	return p.IDPIssuer != "" && p.IDPSubject != ""
}
