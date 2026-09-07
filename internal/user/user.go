// Package user models human identity records for altempl.
package user

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// Source classifies where a User record originated.
const (
	SourceGenesis = "genesis"
	SourceOIDC    = "oidc"
	SourceLocal   = "local"
)

// User is the identity aggregate for a human that logs into altempl.
type User struct {
	ID              uuid.UUID
	Email           string
	Name            string
	Source          string
	PasswordHash    string
	IsAdmin         bool
	Locale          string
	TermsAcceptedAt *time.Time
	CreatedAt       time.Time
}

// New builds a User with the given email, name, and source; invariants apply.
func New(email, name, source string) (*User, error) {
	e, err := normalizeEmail(email)
	if err != nil {
		return nil, err
	}
	n, err := normalizeName(name)
	if err != nil {
		return nil, err
	}
	s := strings.TrimSpace(source)
	if s == "" {
		s = SourceGenesis
	}
	return &User{
		ID:        uuid.New(),
		Email:     e,
		Name:      n,
		Source:    s,
		CreatedAt: time.Now().UTC(),
	}, nil
}

// Rename updates the display name; empty/whitespace rejected.
func (u *User) Rename(name string) error {
	n, err := normalizeName(name)
	if err != nil {
		return err
	}
	u.Name = n
	return nil
}

// AcceptTerms records the moment the user accepted the ToS.
func (u *User) AcceptTerms(now time.Time) {
	t := now.UTC()
	u.TermsAcceptedAt = &t
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

func normalizeName(s string) (string, error) {
	n := strings.TrimSpace(s)
	if n == "" {
		return "", &InvalidNameError{Reason: "empty"}
	}
	return n, nil
}

// defaultNameFromEmail derives a passable display name from the local part of an email address, for OIDC users whose IdP omits the `name` claim; falls back to the trimmed email if the local part is empty.
func defaultNameFromEmail(email string) string {
	e := strings.TrimSpace(email)
	local, _, ok := strings.Cut(e, "@")
	if !ok || strings.TrimSpace(local) == "" {
		return e
	}
	return strings.TrimSpace(local)
}
