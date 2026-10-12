// Package apikey mints and verifies the machine credentials the control and data planes accept.
package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"slices"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/authn"
)

// DefaultPrefix marks a template API key when api.keyPrefix is unset.
const DefaultPrefix = "key_"

const secretBytes = 32

// Scheme is the one prefix a deployment mints keys under and recognizes them by; its zero value is DefaultPrefix.
type Scheme struct{ prefix string }

// NewScheme returns the Scheme for prefix, falling back to DefaultPrefix when prefix is empty.
func NewScheme(prefix string) Scheme { return Scheme{prefix: prefix} }

// Prefix returns the literal every plaintext secret minted under this Scheme starts with.
func (sc Scheme) Prefix() string {
	if sc.prefix == "" {
		return DefaultPrefix
	}
	return sc.prefix
}

// Authn returns the shape gate a surface boundary must apply to credentials minted under this Scheme.
func (sc Scheme) Authn() authn.Scheme { return authn.Scheme{Prefix: sc.Prefix()} }

// Kind names what an API key is bound to.
type Kind string

// The key kinds. A project key is bound to one project for life; an org key and a personal token reach the projects their grant names.
const (
	KindProject  Kind = "project"
	KindOrg      Kind = "org"
	KindPersonal Kind = "personal"
)

const secretHintLen = 4

// MaxLifetime is the longest a newly minted key may live: one calendar year, leap day included.
const MaxLifetime = 366 * 24 * time.Hour

// SECURITY: checked at mint only; existing keys may have no expiry.
func validateLifetime(expiresAt *time.Time, now time.Time) error {
	if expiresAt == nil {
		return &ExpiryRequiredError{}
	}
	if !expiresAt.After(now) {
		return &ExpiryInPastError{}
	}
	if expiresAt.Sub(now) > MaxLifetime {
		return &ExpiryTooLongError{}
	}
	return nil
}

// ProjectGrant is the set of projects an org key or personal token reaches: every project of the org, or the named ones.
type ProjectGrant struct {
	All        bool
	ProjectIDs []uuid.UUID
}

func (g ProjectGrant) validate() error {
	if g.All && len(g.ProjectIDs) > 0 {
		return &GrantConflictError{}
	}
	if !g.All && len(g.ProjectIDs) == 0 {
		return &EmptyGrantError{}
	}
	return nil
}

// APIKey is a machine credential bound to one project, or to an org and the projects its grant names; a personal one acts for its owner.
type APIKey struct {
	ID          uuid.UUID
	OrgID       uuid.UUID
	ProjectID   uuid.UUID
	Kind        Kind
	AllProjects bool
	ProjectIDs  []uuid.UUID
	Name        string
	SecretHash  [32]byte
	SecretHint  string
	Scopes      []string
	ResourceIDs []uuid.UUID
	CreatedBy   uuid.UUID
	CreatedAt   time.Time
	ExpiresAt   *time.Time
	RevokedAt   *time.Time
	LastUsedAt  *time.Time
}

// Mint returns a new project key and its plaintext secret, which is never recoverable afterwards.
func (sc Scheme) Mint(orgID, projectID uuid.UUID, name string, scopes []string, resourceIDs []uuid.UUID, expiresAt *time.Time, now time.Time) (*APIKey, string, error) {
	for _, s := range scopes {
		if level, ok := authn.LevelOf(s); ok && level != authn.LevelProject {
			return nil, "", &ScopeLevelError{Scope: s}
		}
	}
	k, plaintext, err := sc.mint(orgID, name, scopes, expiresAt, now)
	if err != nil {
		return nil, "", err
	}
	k.Kind = KindProject
	k.ProjectID = projectID
	k.ResourceIDs = slices.Clone(resourceIDs)
	return k, plaintext, nil
}

// MintOrg returns a new org key reaching grant, and its plaintext secret.
func (sc Scheme) MintOrg(orgID uuid.UUID, name string, scopes []string, grant ProjectGrant, expiresAt *time.Time, now time.Time) (*APIKey, string, error) {
	if err := grant.validate(); err != nil {
		return nil, "", err
	}
	k, plaintext, err := sc.mint(orgID, name, scopes, expiresAt, now)
	if err != nil {
		return nil, "", err
	}
	k.Kind = KindOrg
	k.AllProjects = grant.All
	k.ProjectIDs = dedupe(grant.ProjectIDs)
	return k, plaintext, nil
}

// MintPersonal returns a new personal access token owned by ownerID, reaching grant inside orgID, and its plaintext secret.
func (sc Scheme) MintPersonal(orgID, ownerID uuid.UUID, name string, scopes []string, grant ProjectGrant, expiresAt *time.Time, now time.Time) (*APIKey, string, error) {
	k, plaintext, err := sc.MintOrg(orgID, name, scopes, grant, expiresAt, now)
	if err != nil {
		return nil, "", err
	}
	k.Kind = KindPersonal
	k.CreatedBy = ownerID
	return k, plaintext, nil
}

func (sc Scheme) mint(orgID uuid.UUID, name string, scopes []string, expiresAt *time.Time, now time.Time) (*APIKey, string, error) {
	for _, s := range scopes {
		if !authn.Valid(s) {
			return nil, "", &UnknownScopeError{Scope: s}
		}
	}
	buf := make([]byte, secretBytes)
	if _, err := rand.Read(buf); err != nil {
		return nil, "", err
	}
	plaintext := sc.Prefix() + base64.RawURLEncoding.EncodeToString(buf)
	return &APIKey{
		ID:         uuid.New(),
		OrgID:      orgID,
		Name:       name,
		SecretHash: sha256.Sum256([]byte(plaintext)),
		SecretHint: plaintext[len(plaintext)-secretHintLen:],
		Scopes:     slices.Clone(scopes),
		CreatedAt:  now,
		ExpiresAt:  expiresAt,
	}, plaintext, nil
}

// GrantProjects widens a selected-projects org key or personal token by projectIDs. SECURITY: a grant only ever widens; no verb narrows it.
func (k *APIKey) GrantProjects(projectIDs []uuid.UUID) error {
	if err := k.promotable(); err != nil {
		return err
	}
	if k.AllProjects {
		return &AlreadyAllProjectsError{}
	}
	if len(projectIDs) == 0 {
		return &EmptyGrantError{}
	}
	k.ProjectIDs = dedupe(append(slices.Clone(k.ProjectIDs), projectIDs...))
	return nil
}

// GrantAllProjects promotes an org key or personal token to every project of its org, including ones created later; there is no way back.
func (k *APIKey) GrantAllProjects() error {
	if err := k.promotable(); err != nil {
		return err
	}
	if k.AllProjects {
		return &AlreadyAllProjectsError{}
	}
	k.AllProjects = true
	k.ProjectIDs = nil
	return nil
}

func (k *APIKey) promotable() error {
	if !k.HasGrant() {
		return &BoundToProjectError{}
	}
	if k.RevokedAt != nil {
		return &RevokedError{}
	}
	return nil
}

// ReachableProjects returns the projects the key's principal reaches when it is not granted all of them.
func (k *APIKey) ReachableProjects() []uuid.UUID {
	if k.HasGrant() {
		return slices.Clone(k.ProjectIDs)
	}
	return []uuid.UUID{k.ProjectID}
}

// HasGrant reports whether the key reaches projects through a grant rather than one bound project.
func (k *APIKey) HasGrant() bool { return k.Kind == KindOrg || k.Kind == KindPersonal }

func dedupe(ids []uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id != uuid.Nil && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// Matches reports whether plaintext hashes to this key's secret, in constant time.
func (k *APIKey) Matches(plaintext string) bool {
	sum := sha256.Sum256([]byte(plaintext))
	return subtle.ConstantTimeCompare(sum[:], k.SecretHash[:]) == 1
}

// Usable reports whether the key is neither revoked nor expired at now.
func (k *APIKey) Usable(now time.Time) bool {
	if k.RevokedAt != nil && !k.RevokedAt.After(now.UTC()) {
		return false
	}
	if k.ExpiresAt != nil && !k.ExpiresAt.After(now.UTC()) {
		return false
	}
	return true
}
