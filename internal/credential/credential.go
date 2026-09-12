// Package credential is the Google credentials bounded context.
package credential

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// MaxNameRunes bounds a credential name.
const MaxNameRunes = 100

// Kind is how a credential authorizes against Google.
type Kind string

// Kind values.
const (
	KindServiceAccount Kind = "service_account"
	KindGoogleOAuth    Kind = "google_oauth"
)

// Status is whether a credential still works.
type Status string

// Status values.
const (
	StatusActive       Status = "active"
	StatusReauthNeeded Status = "reauth_needed"
)

// Credential is the aggregate root. Sealed holds ciphertext only.
type Credential struct {
	ID                 uuid.UUID
	OrgID              uuid.UUID
	ProjectID          uuid.UUID
	Name               string
	Kind               Kind
	Status             Status
	AuthorizedByUserID uuid.UUID
	GoogleAccountEmail string
	Sealed             []byte
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// SealAAD composes the additional authenticated data binding a ciphertext to one tenant and one row.
// SECURITY: this is the only composer; seal and open must both go through it or a stolen ciphertext replays.
func SealAAD(orgID, projectID, credentialID uuid.UUID) []byte {
	return []byte(orgID.String() + "|" + projectID.String() + "|" + credentialID.String())
}

// New enforces creation invariants; a nil id is generated so callers that seal against the id first can supply it.
func New(
	id, orgID, projectID, actorID uuid.UUID,
	name string,
	kind Kind,
	accountEmail string,
	sealed []byte,
) (*Credential, error) {
	name, err := validName(name)
	if err != nil {
		return nil, err
	}
	if !kind.Valid() {
		return nil, &InvalidKindError{Kind: string(kind)}
	}
	if len(sealed) == 0 {
		return nil, &NotSealedError{Situation: "create"}
	}
	if id == uuid.Nil {
		id = uuid.Must(uuid.NewV7())
	}
	now := time.Now().UTC()
	return &Credential{
		ID:                 id,
		OrgID:              orgID,
		ProjectID:          projectID,
		Name:               name,
		Kind:               kind,
		Status:             StatusActive,
		AuthorizedByUserID: actorID,
		GoogleAccountEmail: strings.TrimSpace(accountEmail),
		Sealed:             sealed,
		CreatedAt:          now,
		UpdatedAt:          now,
	}, nil
}

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool {
	return k == KindServiceAccount || k == KindGoogleOAuth
}

// Valid reports whether s is a known status.
func (s Status) Valid() bool {
	return s == StatusActive || s == StatusReauthNeeded
}

// Rotate replaces the sealed ciphertext and the Google account it belongs to, and reactivates the credential.
func (c *Credential) Rotate(sealed []byte, accountEmail string) error {
	if len(sealed) == 0 {
		return &NotSealedError{Situation: "rotate"}
	}
	c.Sealed = sealed
	c.GoogleAccountEmail = strings.TrimSpace(accountEmail)
	c.Status = StatusActive
	c.UpdatedAt = time.Now().UTC()
	return nil
}

// MarkReauthNeeded records that Google rejected the credential and a human must reconnect it.
func (c *Credential) MarkReauthNeeded() {
	c.Status = StatusReauthNeeded
	c.UpdatedAt = time.Now().UTC()
}

// MarkActive records that the credential works again.
func (c *Credential) MarkActive() {
	c.Status = StatusActive
	c.UpdatedAt = time.Now().UTC()
}

func validName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", &InvalidNameError{Reason: "empty"}
	}
	if utf8.RuneCountInString(name) > MaxNameRunes {
		return "", &InvalidNameError{Reason: "over 100 characters"}
	}
	return name, nil
}
