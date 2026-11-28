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
	KeyID uuid.UUID
	// KeyOwnerID is the person a key acts for: its creator, or a personal token's owner. SECURITY: used only for role checks, never as an identity.
	KeyOwnerID uuid.UUID `json:"-"`
	Email      string
	Name       string
	Source     Source
	IDPIssuer  string
	IDPSubject string
	IDToken    string
	// ClientID is the OAuth client a token principal's access token was issued to.
	ClientID        string `json:"-"`
	Scopes          []string
	ActiveOrgID     uuid.UUID
	ActiveProjectID uuid.UUID
	// ProjectIDs lists the projects a key principal reaches. SECURITY: rebuilt from the credential on every request, never persisted.
	ProjectIDs []uuid.UUID `json:"-"`
	// AllProjects grants a key principal every project of its org, including ones created later.
	AllProjects bool `json:"-"`
	// ResourceIDs narrows a key principal to these resources; empty means every resource in its projects.
	ResourceIDs []uuid.UUID `json:"-"`
	// Held is the permission set resolved when the request's tenant scope was bound. SECURITY: never persisted; every request resolves it again.
	Held []string `json:"-"`
	// HeldOrg is the display name of the org Held was resolved in, for refusal messages.
	HeldOrg string `json:"-"`
	// HeldOrgID is the org Held was resolved in. SECURITY: Held answers for this org only.
	HeldOrgID uuid.UUID `json:"-"`
	// HeldRole is the role Held came from.
	HeldRole string `json:"-"`
	// Member is the project grant of the person the principal acts for. SECURITY: set only by authz Bind; the zero value reaches nothing.
	Member          Grant `json:"-"`
	IsAdmin         bool
	Locale          string
	TermsAcceptedAt time.Time
	IssuedAt        time.Time
}

// Grant names the projects of OrgID a member reaches: every one when All, else ProjectIDs.
type Grant struct {
	OrgID      uuid.UUID
	All        bool
	ProjectIDs []uuid.UUID
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
