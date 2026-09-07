// Package project models the project aggregate: a named workspace inside an Org.
package project

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	slugMinLen = 1
	slugMaxLen = 63
	nameMinLen = 1
	nameMaxLen = 200
)

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Project is the aggregate root. Invariants live here.
type Project struct {
	ID        uuid.UUID
	OrgID     uuid.UUID
	Slug      string
	Name      string
	CreatedAt time.Time
	// System marks a bootstrap-owned project that cannot be renamed or deleted.
	System bool
}

// New enforces creation invariants: slug format + name length; returns typed errors.
func New(orgID uuid.UUID, slug, name string) (*Project, error) {
	if err := validateSlug(slug); err != nil {
		return nil, err
	}
	n, err := validateName(name)
	if err != nil {
		return nil, err
	}
	return &Project{
		ID:        uuid.Must(uuid.NewV7()),
		OrgID:     orgID,
		Slug:      slug,
		Name:      n,
		CreatedAt: time.Now().UTC(),
	}, nil
}

// Rename updates the display name; slug is immutable.
func (p *Project) Rename(name string) error {
	n, err := validateName(name)
	if err != nil {
		return err
	}
	p.Name = n
	return nil
}

func validateSlug(s string) error {
	if len(s) < slugMinLen || len(s) > slugMaxLen {
		return &InvalidSlugError{Slug: s, Reason: "length out of range"}
	}
	if !slugRe.MatchString(s) {
		return &InvalidSlugError{Slug: s, Reason: "must be lowercase alphanumeric with dashes"}
	}
	if reservedSlug(s) {
		return &InvalidSlugError{Slug: s, Reason: "reserved word"}
	}
	return nil
}

// NOTE: ServeMux prefers a literal pattern over the wildcard beside it, so a row slugged like a
// literal segment under /orgs/{org}/projects/ would be unreachable.
func reservedSlug(s string) bool {
	return s == "new"
}

func validateName(s string) (string, error) {
	n := strings.TrimSpace(s)
	if n == "" {
		return "", &InvalidNameError{Reason: "empty"}
	}
	if utf8.RuneCountInString(n) < nameMinLen || utf8.RuneCountInString(n) > nameMaxLen {
		return "", &InvalidNameError{Reason: "length out of range"}
	}
	return n, nil
}
