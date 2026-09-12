package apikey

import (
	"context"
	"slices"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
)

var _ authn.Authenticator = (*Authenticator)(nil)

// Authenticator turns a presented API key into a session.Principal.
type Authenticator struct {
	svc   *Service
	usage UsageRecorder
}

// NewAuthenticator binds the authenticator to the service and the usage recorder.
func NewAuthenticator(svc *Service, usage UsageRecorder) *Authenticator {
	return &Authenticator{svc: svc, usage: usage}
}

// Authenticate resolves raw into a Principal carrying the key's tenant and scopes.
func (a *Authenticator) Authenticate(ctx context.Context, raw string) (session.Principal, error) {
	k, err := a.resolve(ctx, raw)
	if err != nil {
		return session.Principal{}, err
	}
	return principalFor(k), nil
}

// Authorize resolves raw and additionally requires the key to grant scope on sheetID inside orgID and projectID.
// SECURITY: this is the per-sheet seam. A session.Principal cannot carry SheetIDs, so without this check every
// sheet-restricted key would act project-wide, and a key from another project would act on a foreign sheet.
func (a *Authenticator) Authorize(ctx context.Context, raw, scope string, orgID, projectID, sheetID uuid.UUID) (session.Principal, error) {
	k, err := a.resolve(ctx, raw)
	if err != nil {
		return session.Principal{}, err
	}
	if k.OrgID != orgID || k.ProjectID != projectID {
		return session.Principal{}, &UnauthorizedError{}
	}
	if !k.Allows(scope, sheetID) {
		return session.Principal{}, &UnauthorizedError{}
	}
	return principalFor(k), nil
}

// AuthorizeProject resolves raw and requires the key to grant scope project-wide inside orgID and projectID.
// SECURITY: for routes that name no sheet. A sheet-restricted key is refused outright rather than being
// checked against a sheet id it was never given.
func (a *Authenticator) AuthorizeProject(ctx context.Context, raw, scope string, orgID, projectID uuid.UUID) (session.Principal, error) {
	k, err := a.resolve(ctx, raw)
	if err != nil {
		return session.Principal{}, err
	}
	if k.OrgID != orgID || k.ProjectID != projectID {
		return session.Principal{}, &UnauthorizedError{}
	}
	if !k.AllowsProject(scope) {
		return session.Principal{}, &UnauthorizedError{}
	}
	return principalFor(k), nil
}

// SECURITY: the shape gate runs before the service, so a JWT never reaches a database lookup, and every
// rejection collapses to the module's own opaque *UnauthorizedError — including an unexpected store failure.
func (a *Authenticator) resolve(ctx context.Context, raw string) (*APIKey, error) {
	if authn.Looks(raw) != authn.ShapeAPIKey {
		return nil, &UnauthorizedError{}
	}
	k, err := a.svc.Authenticate(ctx, raw)
	if err != nil {
		return nil, &UnauthorizedError{}
	}
	if a.usage != nil {
		a.usage.Record(k.OrgID, k.ProjectID, k.ID, time.Now().UTC())
	}
	return k, nil
}

// SECURITY: a key is not a person, so UserID stays nil, and neither the plaintext nor SecretHash travels with the principal.
// NOTE: interceptor.Tenant copies ActiveProjectID through without re-checking that it belongs to ActiveOrgID; that is safe
// for a key principal only because the key row carries both ids and Authorize re-checks them against the resolved resource.
func principalFor(k *APIKey) session.Principal {
	return session.Principal{
		UserID:          uuid.Nil,
		Name:            k.Name,
		Source:          session.SourceAPIKey,
		Scopes:          slices.Clone(k.Scopes),
		ActiveOrgID:     k.OrgID,
		ActiveProjectID: k.ProjectID,
		IssuedAt:        k.CreatedAt,
	}
}
