package spreadsheet

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
)

// NotFoundError reports that no spreadsheet matched the lookup.
type NotFoundError struct {
	ID           string
	ProjectID    string
	GoogleFileID string
}

func (e *NotFoundError) Error() string {
	if e.ID != "" {
		return "spreadsheet: not found: id=" + e.ID
	}
	if e.GoogleFileID != "" {
		return fmt.Sprintf("spreadsheet: not found: project=%s google_file_id=%q", e.ProjectID, e.GoogleFileID)
	}
	return "spreadsheet: not found: no match"
}

// ToAppError converts the typed error into the wire envelope.
func (e *NotFoundError) ToAppError() *apperror.AppError {
	meta := map[string]string{}
	if e.ID != "" {
		meta["spreadsheet_id"] = e.ID
	}
	if e.ProjectID != "" {
		meta["project_id"] = e.ProjectID
	}
	if e.GoogleFileID != "" {
		meta["google_file_id"] = e.GoogleFileID
	}
	return apperror.New(
		apperror.CodeSpreadsheetNotFound,
		"Spreadsheet not found",
		codes.NotFound,
		&apperrorv1.ErrorDetail{Code: apperror.CodeSpreadsheetNotFound, Meta: meta},
	)
}

// IsNotFoundError reports whether err's chain contains a *NotFoundError.
func IsNotFoundError(err error) bool {
	_, ok := errors.AsType[*NotFoundError](err)
	return ok
}

// AlreadyExistsError reports that the document is already registered in the project.
type AlreadyExistsError struct {
	ProjectID    string
	GoogleFileID string
}

func (e *AlreadyExistsError) Error() string {
	if e.GoogleFileID == "" {
		return "spreadsheet: already registered: duplicate google file id"
	}
	return fmt.Sprintf("spreadsheet: already registered: google_file_id=%q in project %s", e.GoogleFileID, e.ProjectID)
}

// ToAppError converts the typed error into the wire envelope.
func (e *AlreadyExistsError) ToAppError() *apperror.AppError {
	meta := map[string]string{}
	if e.ProjectID != "" {
		meta["project_id"] = e.ProjectID
	}
	if e.GoogleFileID != "" {
		meta["google_file_id"] = e.GoogleFileID
	}
	return apperror.New(
		apperror.CodeSpreadsheetAlreadyExists,
		"This spreadsheet is already registered in the project",
		codes.AlreadyExists,
		&apperrorv1.ErrorDetail{Code: apperror.CodeSpreadsheetAlreadyExists, Meta: meta},
	)
}

// IsAlreadyExistsError reports whether err's chain contains an *AlreadyExistsError.
func IsAlreadyExistsError(err error) bool {
	_, ok := errors.AsType[*AlreadyExistsError](err)
	return ok
}

// InvalidFileIDError reports that a Google file id violates the creation invariant.
type InvalidFileIDError struct {
	FileID string
	Reason string
}

func (e *InvalidFileIDError) Error() string {
	reason := e.Reason
	if reason == "" {
		reason = "invalid"
	}
	return "spreadsheet: google file id: " + reason
}

// ToAppError converts the typed error into the wire envelope.
func (e *InvalidFileIDError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSpreadsheetInvalidFileID,
		"Invalid Google file id: paste the document id, not the full URL",
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSpreadsheetInvalidFileID,
			Meta: map[string]string{"google_file_id": e.FileID, "reason": e.Reason},
		},
	)
}

// IsInvalidFileIDError reports whether err's chain contains an *InvalidFileIDError.
func IsInvalidFileIDError(err error) bool {
	_, ok := errors.AsType[*InvalidFileIDError](err)
	return ok
}

// InvalidTitleError reports that a title violates the creation invariant.
type InvalidTitleError struct{ Reason string }

func (e *InvalidTitleError) Error() string {
	reason := e.Reason
	if reason == "" {
		reason = "invalid"
	}
	return "spreadsheet: title: " + reason
}

// ToAppError converts the typed error into the wire envelope.
func (e *InvalidTitleError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSpreadsheetInvalidTitle,
		"Invalid spreadsheet title: "+e.Reason,
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSpreadsheetInvalidTitle,
			Meta: map[string]string{"reason": e.Reason},
		},
	)
}

// IsInvalidTitleError reports whether err's chain contains an *InvalidTitleError.
func IsInvalidTitleError(err error) bool {
	_, ok := errors.AsType[*InvalidTitleError](err)
	return ok
}

// InvalidCredentialError reports that no credential was named for the document to read through.
type InvalidCredentialError struct{ Reason string }

func (e *InvalidCredentialError) Error() string {
	reason := e.Reason
	if reason == "" {
		reason = "invalid"
	}
	return "spreadsheet: credential id: " + reason
}

// ToAppError converts the typed error into the wire envelope.
func (e *InvalidCredentialError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeValidation,
		"A spreadsheet must be bound to a credential",
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeValidation,
			Meta: map[string]string{"field": "credential_id", "reason": e.Reason},
		},
	)
}

// IsInvalidCredentialError reports whether err's chain contains an *InvalidCredentialError.
func IsInvalidCredentialError(err error) bool {
	_, ok := errors.AsType[*InvalidCredentialError](err)
	return ok
}

// NotWritableError signals a structural change to a spreadsheet that was never opted in to writes.
type NotWritableError struct {
	ID           string
	GoogleFileID string
}

func (e *NotWritableError) Error() string {
	if e.GoogleFileID == "" {
		return "spreadsheet: not writable: id=" + e.ID
	}
	return fmt.Sprintf("spreadsheet: not writable: id=%s google_file_id=%q", e.ID, e.GoogleFileID)
}

// ToAppError maps NotWritableError to a PermissionDenied envelope.
// SECURITY: the envelope names no Google file id — the data plane's tabs routes are addressed by our
// UUID, so the id is not the caller's to learn. Error() keeps it for the log and the span.
func (e *NotWritableError) ToAppError() *apperror.AppError {
	meta := map[string]string{}
	if e.ID != "" {
		meta["spreadsheet_id"] = e.ID
	}
	return apperror.New(
		apperror.CodeSpreadsheetNotWritable,
		"This spreadsheet is not writable",
		codes.PermissionDenied,
		&apperrorv1.ErrorDetail{Code: apperror.CodeSpreadsheetNotWritable, Meta: meta},
	)
}

// IsNotWritableError reports whether err's chain contains a *NotWritableError.
func IsNotWritableError(err error) bool {
	_, ok := errors.AsType[*NotWritableError](err)
	return ok
}
