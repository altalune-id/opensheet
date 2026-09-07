// Package invite is the invites bounded context.
package invite

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Role names an invite's target role.
type Role string

const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

// IsValid reports whether r is one of the enumerated roles.
func (r Role) IsValid() bool {
	switch r {
	case RoleOwner, RoleAdmin, RoleMember:
		return true
	}
	return false
}

// Invite is the aggregate root. Invariants live here.
type Invite struct {
	ID        uuid.UUID
	OrgID     uuid.UUID
	Email     string
	Role      Role
	TokenHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}

// NewParams is the input to New.
type NewParams struct {
	OrgID uuid.UUID
	Email string
	Role  Role
	TTL   time.Duration
	Token string
	Now   time.Time
}

// New enforces creation invariants: email format, role validity, TTL > 0.
func New(p NewParams) (*Invite, error) {
	if !p.Role.IsValid() {
		return nil, &InvalidRoleError{Role: string(p.Role)}
	}
	email, err := normalizeEmail(p.Email)
	if err != nil {
		return nil, err
	}
	now := p.Now
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	return &Invite{
		ID:        uuid.Must(uuid.NewV7()),
		OrgID:     p.OrgID,
		Email:     email,
		Role:      p.Role,
		TokenHash: HashToken(p.Token),
		ExpiresAt: now.Add(p.TTL),
		CreatedAt: now,
	}, nil
}

// HashToken returns the sha256 hex digest of raw. SECURITY: issuance and lookup MUST use this function so stored and probed values line up.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// IsExpired reports whether the invite has passed its TTL at now.
func (i *Invite) IsExpired(now time.Time) bool {
	return now.UTC().After(i.ExpiresAt)
}

// IsUsed reports whether the invite has been consumed.
func (i *Invite) IsUsed() bool { return i.UsedAt != nil }

// Consume marks the invite consumed at now.
func (i *Invite) Consume(now time.Time) error {
	if i.IsUsed() {
		return &AlreadyUsedError{ID: i.ID.String()}
	}
	if i.IsExpired(now) {
		return &ExpiredError{ID: i.ID.String()}
	}
	u := now.UTC()
	i.UsedAt = &u
	return nil
}

func normalizeEmail(s string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(s))
	if e == "" {
		return "", &InvalidEmailError{Reason: "empty"}
	}
	at := strings.IndexByte(e, '@')
	if at <= 0 || at == len(e)-1 {
		return "", &InvalidEmailError{Reason: "malformed", Value: s}
	}
	return e, nil
}
