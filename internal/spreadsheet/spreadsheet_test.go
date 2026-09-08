package spreadsheet

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	"altalune.id/opensheet/internal/apperror"
)

func TestNew(t *testing.T) {
	orgID := uuid.New()
	projectID := uuid.New()
	credentialID := uuid.New()

	tests := []struct {
		name       string
		fileID     string
		title      string
		wantFileID string
		wantTitle  string
		wantErrIs  func(error) bool
	}{
		{name: "accepts google file id alphabet", fileID: "1a-B_c9", title: "Q3 Budget", wantFileID: "1a-B_c9", wantTitle: "Q3 Budget"},
		{name: "trims surrounding whitespace on file id", fileID: "  ABC  ", title: "x", wantFileID: "ABC", wantTitle: "x"},
		{name: "accepts 128 char file id", fileID: strings.Repeat("a", 128), title: "x", wantFileID: strings.Repeat("a", 128), wantTitle: "x"},
		{name: "trims title", fileID: "ABC", title: "  Q3 Budget  ", wantFileID: "ABC", wantTitle: "Q3 Budget"},
		{name: "accepts empty title", fileID: "ABC", title: "   ", wantFileID: "ABC", wantTitle: ""},
		{name: "accepts 200 rune title", fileID: "ABC", title: strings.Repeat("é", 200), wantFileID: "ABC", wantTitle: strings.Repeat("é", 200)},

		{name: "rejects empty file id", fileID: "", title: "x", wantErrIs: IsInvalidFileIDError},
		{name: "rejects whitespace only file id", fileID: "   ", title: "x", wantErrIs: IsInvalidFileIDError},
		{name: "rejects file id over 128 chars", fileID: strings.Repeat("a", 129), title: "x", wantErrIs: IsInvalidFileIDError},
		{name: "rejects a pasted full url", fileID: "https://docs.google.com/spreadsheets/d/ABC/edit", title: "x", wantErrIs: IsInvalidFileIDError},
		{name: "rejects a slash", fileID: "AB/C", title: "x", wantErrIs: IsInvalidFileIDError},
		{name: "rejects a dot", fileID: "AB.C", title: "x", wantErrIs: IsInvalidFileIDError},
		{name: "rejects internal whitespace", fileID: "AB C", title: "x", wantErrIs: IsInvalidFileIDError},
		{name: "rejects title over 200 runes", fileID: "ABC", title: strings.Repeat("a", 201), wantErrIs: IsInvalidTitleError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := New(orgID, projectID, credentialID, tc.fileID, tc.title)
			if tc.wantErrIs != nil {
				if err == nil {
					t.Fatalf("want error, got spreadsheet=%+v", got)
				}
				if !tc.wantErrIs(err) {
					t.Fatalf("wrong error type: %T: %v", err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got.GoogleFileID != tc.wantFileID {
				t.Errorf("GoogleFileID = %q, want %q", got.GoogleFileID, tc.wantFileID)
			}
			if got.Title != tc.wantTitle {
				t.Errorf("Title = %q, want %q", got.Title, tc.wantTitle)
			}
			if got.OrgID != orgID || got.ProjectID != projectID || got.CredentialID != credentialID {
				t.Errorf("ids not carried through: %+v", got)
			}
			if got.ID == uuid.Nil {
				t.Error("ID unset")
			}
			if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
				t.Error("timestamps unset")
			}
			if !got.CreatedAt.Equal(got.UpdatedAt) {
				t.Errorf("CreatedAt %v != UpdatedAt %v on creation", got.CreatedAt, got.UpdatedAt)
			}
			if got.CreatedAt.Location() != time.UTC {
				t.Errorf("CreatedAt not UTC: %v", got.CreatedAt.Location())
			}
		})
	}
}

func TestNew_RejectsNilCredential(t *testing.T) {
	_, err := New(uuid.New(), uuid.New(), uuid.Nil, "ABC", "x")
	if !IsInvalidCredentialError(err) {
		t.Fatalf("want IsInvalidCredentialError, got %T: %v", err, err)
	}
}

func TestRetitle(t *testing.T) {
	tests := []struct {
		name      string
		title     string
		wantTitle string
		wantErrIs func(error) bool
	}{
		{name: "replaces and trims", title: "  new  ", wantTitle: "new"},
		{name: "accepts empty", title: "", wantTitle: ""},
		{name: "accepts 200 runes", title: strings.Repeat("a", 200), wantTitle: strings.Repeat("a", 200)},
		{name: "rejects over 200 runes", title: strings.Repeat("a", 201), wantErrIs: IsInvalidTitleError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := mustNew(t)
			before := s.UpdatedAt
			err := s.Retitle(tc.title)
			if tc.wantErrIs != nil {
				if !tc.wantErrIs(err) {
					t.Fatalf("wrong error type: %T: %v", err, err)
				}
				if s.Title != "old" {
					t.Errorf("Title mutated on rejection: %q", s.Title)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if s.Title != tc.wantTitle {
				t.Errorf("Title = %q, want %q", s.Title, tc.wantTitle)
			}
			if s.UpdatedAt.Before(before) {
				t.Error("UpdatedAt went backwards")
			}
		})
	}
}

func TestRebind(t *testing.T) {
	t.Run("replaces the credential", func(t *testing.T) {
		s := mustNew(t)
		before := s.UpdatedAt
		next := uuid.New()
		if err := s.Rebind(next); err != nil {
			t.Fatalf("Rebind: %v", err)
		}
		if s.CredentialID != next {
			t.Errorf("CredentialID = %v, want %v", s.CredentialID, next)
		}
		if s.UpdatedAt.Before(before) {
			t.Error("UpdatedAt went backwards")
		}
	})
	t.Run("rejects the nil credential", func(t *testing.T) {
		s := mustNew(t)
		was := s.CredentialID
		err := s.Rebind(uuid.Nil)
		if !IsInvalidCredentialError(err) {
			t.Fatalf("want IsInvalidCredentialError, got %T: %v", err, err)
		}
		if s.CredentialID != was {
			t.Error("CredentialID mutated on rejection")
		}
	})
}

func TestSetWritable(t *testing.T) {
	t.Run("registration is not writable", func(t *testing.T) {
		s := mustNew(t)
		if s.Writable {
			t.Fatal("registration must not imply write permission")
		}
	})
	t.Run("toggles both ways and advances UpdatedAt", func(t *testing.T) {
		s := mustNew(t)
		before := s.UpdatedAt
		s.SetWritable(true)
		if !s.Writable {
			t.Error("SetWritable(true) did not set the flag")
		}
		if s.UpdatedAt.Before(before) {
			t.Error("UpdatedAt went backwards")
		}
		s.SetWritable(false)
		if s.Writable {
			t.Error("SetWritable(false) did not clear the flag")
		}
	})
}

func mustNew(t *testing.T) *Spreadsheet {
	t.Helper()
	s, err := New(uuid.New(), uuid.New(), uuid.New(), "ABC", "old")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestNotFoundError(t *testing.T) {
	tests := []struct {
		name     string
		err      *NotFoundError
		wantText []string
	}{
		{name: "by id", err: &NotFoundError{ID: "abc"}, wantText: []string{"spreadsheet", "not found", "abc"}},
		{
			name:     "by google file id",
			err:      &NotFoundError{ProjectID: "proj", GoogleFileID: "FILE"},
			wantText: []string{"spreadsheet", "not found", "FILE"},
		},
		{name: "bare", err: &NotFoundError{}, wantText: []string{"spreadsheet", "not found"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, want := range tc.wantText {
				if !strings.Contains(tc.err.Error(), want) {
					t.Errorf("Error() = %q, missing %q", tc.err.Error(), want)
				}
			}
			ae := tc.err.ToAppError()
			if ae == nil {
				t.Fatal("ToAppError returned nil")
			}
			if ae.Code() != apperror.CodeSpreadsheetNotFound {
				t.Errorf("code = %q, want %q", ae.Code(), apperror.CodeSpreadsheetNotFound)
			}
			if ae.GRPCCode() != codes.NotFound {
				t.Errorf("grpc code = %v, want NotFound", ae.GRPCCode())
			}
			if !IsNotFoundError(tc.err) {
				t.Error("IsNotFoundError = false")
			}
		})
	}
}

func TestAlreadyExistsError(t *testing.T) {
	e := &AlreadyExistsError{ProjectID: "proj", GoogleFileID: "FILE"}
	if !strings.Contains(e.Error(), "FILE") {
		t.Errorf("Error() = %q, missing file id", e.Error())
	}
	if !strings.Contains(e.Error(), "spreadsheet: ") {
		t.Errorf("Error() = %q, missing module prefix", e.Error())
	}
	ae := e.ToAppError()
	if ae.Code() != apperror.CodeSpreadsheetAlreadyExists {
		t.Errorf("code = %q, want %q", ae.Code(), apperror.CodeSpreadsheetAlreadyExists)
	}
	if ae.GRPCCode() != codes.AlreadyExists {
		t.Errorf("grpc code = %v, want AlreadyExists", ae.GRPCCode())
	}
	if !IsAlreadyExistsError(e) {
		t.Error("IsAlreadyExistsError = false")
	}
	bare := &AlreadyExistsError{}
	if !strings.Contains(bare.Error(), "already registered") {
		t.Errorf("bare Error() = %q", bare.Error())
	}
	if bare.ToAppError() == nil {
		t.Error("bare ToAppError returned nil")
	}
}

func TestInvalidFileIDError(t *testing.T) {
	e := &InvalidFileIDError{FileID: "https://docs.google.com/x", Reason: "not a google file id"}
	if !strings.Contains(e.Error(), "not a google file id") {
		t.Errorf("Error() = %q, missing reason", e.Error())
	}
	ae := e.ToAppError()
	if ae.Code() != apperror.CodeSpreadsheetInvalidFileID {
		t.Errorf("code = %q, want %q", ae.Code(), apperror.CodeSpreadsheetInvalidFileID)
	}
	if ae.GRPCCode() != codes.InvalidArgument {
		t.Errorf("grpc code = %v, want InvalidArgument", ae.GRPCCode())
	}
	if !IsInvalidFileIDError(e) {
		t.Error("IsInvalidFileIDError = false")
	}
	bare := &InvalidFileIDError{}
	if !strings.Contains(bare.Error(), "invalid") {
		t.Errorf("bare Error() = %q", bare.Error())
	}
}

func TestInvalidTitleError(t *testing.T) {
	e := &InvalidTitleError{Reason: "over 200 characters"}
	if e.Error() != "spreadsheet: title: over 200 characters" {
		t.Errorf("Error() = %q", e.Error())
	}
	ae := e.ToAppError()
	if ae.Code() != apperror.CodeSpreadsheetInvalidTitle {
		t.Errorf("code = %q, want %q", ae.Code(), apperror.CodeSpreadsheetInvalidTitle)
	}
	if ae.GRPCCode() != codes.InvalidArgument {
		t.Errorf("grpc code = %v, want InvalidArgument", ae.GRPCCode())
	}
	if !IsInvalidTitleError(e) {
		t.Error("IsInvalidTitleError = false")
	}
	bare := &InvalidTitleError{}
	if !strings.Contains(bare.Error(), "invalid") {
		t.Errorf("bare Error() = %q", bare.Error())
	}
}

func TestInvalidCredentialError(t *testing.T) {
	e := &InvalidCredentialError{Reason: "empty"}
	if e.Error() != "spreadsheet: credential id: empty" {
		t.Errorf("Error() = %q", e.Error())
	}
	ae := e.ToAppError()
	if ae.Code() != apperror.CodeValidation {
		t.Errorf("code = %q, want %q", ae.Code(), apperror.CodeValidation)
	}
	if ae.GRPCCode() != codes.InvalidArgument {
		t.Errorf("grpc code = %v, want InvalidArgument", ae.GRPCCode())
	}
	if !IsInvalidCredentialError(e) {
		t.Error("IsInvalidCredentialError = false")
	}
	bare := &InvalidCredentialError{}
	if !strings.Contains(bare.Error(), "invalid") {
		t.Errorf("bare Error() = %q", bare.Error())
	}
}

func TestPredicates_RejectForeignErrors(t *testing.T) {
	other := errors.New("boom")
	for name, pred := range map[string]func(error) bool{
		"IsNotFoundError":          IsNotFoundError,
		"IsAlreadyExistsError":     IsAlreadyExistsError,
		"IsInvalidFileIDError":     IsInvalidFileIDError,
		"IsInvalidTitleError":      IsInvalidTitleError,
		"IsInvalidCredentialError": IsInvalidCredentialError,
	} {
		if pred(other) {
			t.Errorf("%s matched a foreign error", name)
		}
		if pred(nil) {
			t.Errorf("%s matched nil", name)
		}
	}
}

func TestPredicates_UnwrapThroughAChain(t *testing.T) {
	wrapped := fmt.Errorf("outer: %w", &NotFoundError{ID: "abc"})
	if !IsNotFoundError(wrapped) {
		t.Error("IsNotFoundError did not walk the chain")
	}
}
