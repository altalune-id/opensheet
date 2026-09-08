// Package spreadsheet is the registered-Google-document bounded context.
package spreadsheet

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// MaxFileIDLen bounds a Google file id.
const MaxFileIDLen = 128

// MaxTitleRunes bounds a document title.
const MaxTitleRunes = 200

//nolint:gochecknoglobals // compiled once; the file-id alphabet is a package fixture, not runtime state.
var fileIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Spreadsheet is the aggregate root: one Google document bound to one credential. Invariants live here.
type Spreadsheet struct {
	ID           uuid.UUID
	OrgID        uuid.UUID
	ProjectID    uuid.UUID
	CredentialID uuid.UUID
	GoogleFileID string
	Title        string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// New enforces creation invariants: a Google file-id alphabet, a bounded title and a non-nil credential.
func New(orgID, projectID, credentialID uuid.UUID, googleFileID, title string) (*Spreadsheet, error) {
	fileID, err := validateFileID(googleFileID)
	if err != nil {
		return nil, err
	}
	t, err := validateTitle(title)
	if err != nil {
		return nil, err
	}
	if err := validateCredentialID(credentialID); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	return &Spreadsheet{
		ID:           uuid.Must(uuid.NewV7()),
		OrgID:        orgID,
		ProjectID:    projectID,
		CredentialID: credentialID,
		GoogleFileID: fileID,
		Title:        t,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

// Retitle replaces the document title under the same invariant as New.
func (s *Spreadsheet) Retitle(title string) error {
	t, err := validateTitle(title)
	if err != nil {
		return err
	}
	s.Title = t
	s.UpdatedAt = time.Now().UTC()
	return nil
}

// Rebind points the document at another credential under the same invariant as New.
func (s *Spreadsheet) Rebind(credentialID uuid.UUID) error {
	if err := validateCredentialID(credentialID); err != nil {
		return err
	}
	s.CredentialID = credentialID
	s.UpdatedAt = time.Now().UTC()
	return nil
}

// NOTE: a pasted edit URL fails the alphabet, so it is rejected here rather than at Google call time.
func validateFileID(raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if id == "" {
		return "", &InvalidFileIDError{FileID: raw, Reason: "empty"}
	}
	if len(id) > MaxFileIDLen {
		return "", &InvalidFileIDError{FileID: raw, Reason: "over 128 characters"}
	}
	if !fileIDRe.MatchString(id) {
		return "", &InvalidFileIDError{FileID: raw, Reason: "not a Google file id: expected only letters, digits, underscore and dash"}
	}
	return id, nil
}

func validateTitle(raw string) (string, error) {
	t := strings.TrimSpace(raw)
	if utf8.RuneCountInString(t) > MaxTitleRunes {
		return "", &InvalidTitleError{Reason: "over 200 characters"}
	}
	return t, nil
}

func validateCredentialID(id uuid.UUID) error {
	if id == uuid.Nil {
		return &InvalidCredentialError{Reason: "empty"}
	}
	return nil
}
