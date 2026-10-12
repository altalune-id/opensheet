// Package session models the authenticated caller (Principal) and the store that persists it.
package session

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Source string

const (
	SourceGenesis Source = "genesis"
	SourceOIDC    Source = "oidc"
	SourceToken   Source = "token"
	SourceLocal   Source = "local"
	SourceAPIKey  Source = "apikey"
)

type Principal struct {
	UserID uuid.UUID
	// KeyID is the API key id when this principal is a key rather than a person.
	// SECURITY: a key principal keeps UserID == uuid.Nil, so it is never mistaken for a signed-in human.
	KeyID           uuid.UUID
	Email           string
	Name            string
	Source          Source
	IDPIssuer       string
	IDPSubject      string
	IDToken         string
	Scopes          []string
	ActiveOrgID     uuid.UUID
	ActiveProjectID uuid.UUID
	// ProjectIDs lists the projects a key principal reaches. SECURITY: rebuilt from the credential on every request, never persisted.
	ProjectIDs []uuid.UUID `json:"-"`
	// AllProjects grants a key principal every project of its org, including ones created later.
	AllProjects bool `json:"-"`
	// ResourceIDs narrows a key principal to these resources; empty means every resource in its projects.
	ResourceIDs []uuid.UUID `json:"-"`
	// ClaimedOrgID is the org_id a bearer token asserted.
	// SECURITY: a hint, never authority; only a membership check may promote it to ActiveOrgID.
	ClaimedOrgID    uuid.UUID `json:"-"`
	IsAdmin         bool
	Locale          string
	TermsAcceptedAt time.Time
	IssuedAt        time.Time
}

type principalKey struct{}

func PrincipalInto(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

func PrincipalFrom(ctx context.Context) Principal {
	p, _ := ctx.Value(principalKey{}).(Principal)
	return p
}

// Store persists sessions by opaque id.
type Store interface {
	Save(ctx context.Context, sid string, p Principal, exp time.Time) error
	Load(ctx context.Context, sid string) (Principal, bool, error)
	Delete(ctx context.Context, sid string) error
	// DeleteExpired removes sessions whose expiry has passed and reports how many went.
	DeleteExpired(ctx context.Context) (int, error)
}
