// Package user models human identity records for opensheet.
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

// User is the identity aggregate for a human that logs into opensheet.
type User struct {
	ID              uuid.UUID
	Email           string
	Name            string
	Source          string
	IDPIssuer       string
	IDPSubject      string
	PasswordHash    string
	IsAdmin         bool
	Locale          string
	TermsAcceptedAt *time.Time
	CreatedAt       time.Time
}

// New builds a User with the given email, name, and source; invariants apply.
func New(email, name, source string) (*User, error) {
	e, err := NormalizeEmail(email)
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

// ChangeEmail updates the address; the same invariants New applies.
func (u *User) ChangeEmail(email string) error {
	e, err := NormalizeEmail(email)
	if err != nil {
		return err
	}
	u.Email = e
	return nil
}

// AcceptTerms records the moment the user accepted the ToS.
func (u *User) AcceptTerms(now time.Time) {
	t := now.UTC()
	u.TermsAcceptedAt = &t
}

// NormalizeEmail trims and lowercases s and refuses an address without a local part and a domain.
func NormalizeEmail(s string) (string, error) {
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

func defaultNameFromEmail(email string) string {
	e := strings.TrimSpace(email)
	local, _, ok := strings.Cut(e, "@")
	if !ok || strings.TrimSpace(local) == "" {
		return e
	}
	return strings.TrimSpace(local)
}
