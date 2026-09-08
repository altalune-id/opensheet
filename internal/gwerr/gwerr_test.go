package gwerr_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/gworkspace"
	"altalune.id/opensheet/gworkspace/gsheet"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/gwerr"
)

type errStub struct{}

func (errStub) Error() string { return "stub" }

func TestAppError_MapsEveryProviderError(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		code    string
		message string
		pred    func(error) bool
	}{
		{
			name:    "not found",
			err:     &gworkspace.NotFoundError{FileID: "F"},
			code:    apperror.CodeGoogleNotFound,
			message: "The Google spreadsheet behind this sheet was not found; it may have been deleted or unshared",
			pred:    gworkspace.IsNotFoundError,
		},
		{
			name:    "permission denied",
			err:     &gworkspace.PermissionDeniedError{FileID: "F"},
			code:    apperror.CodeGooglePermissionDenied,
			message: "Google denied access to this spreadsheet; share it with this credential",
			pred:    gworkspace.IsPermissionDeniedError,
		},
		{
			name:    "quota exceeded",
			err:     &gworkspace.QuotaExceededError{RetryAfter: 30 * time.Second},
			code:    apperror.CodeGoogleQuotaExceeded,
			message: "Google Sheets rate limit exceeded; try again shortly",
			pred:    gworkspace.IsQuotaExceededError,
		},
		{
			name:    "unavailable",
			err:     &gworkspace.UnavailableError{Cause: errStub{}},
			code:    apperror.CodeGoogleUnavailable,
			message: "Google Sheets is unavailable; try again shortly",
			pred:    gworkspace.IsUnavailableError,
		},
		{
			name:    "auth expired",
			err:     &gworkspace.AuthExpiredError{Cause: errStub{}},
			code:    apperror.CodeCredentialReauthNeeded,
			message: "Google rejected this credential; reconnect it",
			pred:    gworkspace.IsAuthExpiredError,
		},
		{
			name:    "tab not found",
			err:     &gsheet.TabNotFoundError{Tab: "Q1"},
			code:    apperror.CodeSheetTabNotFound,
			message: `Tab "Q1" not found in this spreadsheet`,
			pred:    gsheet.IsTabNotFoundError,
		},
		{
			name:    "invalid tab title",
			err:     &gsheet.InvalidTabTitleError{Title: "   "},
			code:    apperror.CodeSheetInvalidTabTitle,
			message: "A tab title must be 1 to 100 characters",
			pred:    gsheet.IsInvalidTabTitleError,
		},
		{
			name:    "invalid row index",
			err:     &gsheet.InvalidRowIndexError{Row: 0},
			code:    apperror.CodeSheetInvalidRowIndex,
			message: "A row number must be a positive integer",
			pred:    gsheet.IsInvalidRowIndexError,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := gwerr.AppError(tc.err)

			ae, ok := apperror.AsAppError(wrapped)
			if !ok {
				t.Fatalf("AsAppError did not recognise %T", wrapped)
			}
			if ae.Code() != tc.code {
				t.Errorf("Code() = %q, want %q", ae.Code(), tc.code)
			}
			if ae.Message() != tc.message {
				t.Errorf("Message() = %q, want %q", ae.Message(), tc.message)
			}
			if !tc.pred(wrapped) {
				t.Error("the typed predicate no longer matches the wrapped error")
			}
			if !errors.Is(wrapped, tc.err) {
				t.Error("the wrapped error does not unwrap to the original")
			}
			if wrapped.Error() != tc.err.Error() {
				t.Errorf("Error() = %q, want the provider message %q", wrapped.Error(), tc.err.Error())
			}
		})
	}
}

func TestAppError_QuotaCarriesRetryAfterInTheDetail(t *testing.T) {
	ae, ok := apperror.AsAppError(gwerr.AppError(&gworkspace.QuotaExceededError{RetryAfter: 30 * time.Second}))
	if !ok {
		t.Fatal("AsAppError did not recognise the quota error")
	}
	if len(ae.Details()) != 1 {
		t.Fatalf("Details() = %v, want one detail", ae.Details())
	}
}

func TestAppError_LeavesForeignErrorsAlone(t *testing.T) {
	stub := errStub{}
	if got := gwerr.AppError(stub); !errors.Is(got, stub) {
		t.Fatalf("AppError(errStub) = %v, want it returned unchanged", got)
	}
	if _, ok := apperror.AsAppError(gwerr.AppError(stub)); ok {
		t.Error("a foreign error was given an envelope")
	}
}

func TestAppError_NilIsANoOp(t *testing.T) {
	if err := gwerr.AppError(nil); err != nil {
		t.Fatalf("AppError(nil) = %v, want nil", err)
	}
}

// SECURITY: the data plane returns messages verbatim, so a caller holding only sheets:write must not
// learn the Google file id backing a sheet from a refusal.
func TestAppError_PermissionDeniedNamesNoGoogleFileID(t *testing.T) {
	ae, ok := apperror.AsAppError(gwerr.AppError(&gworkspace.PermissionDeniedError{FileID: "1SecretFileID"}))
	if !ok {
		t.Fatal("AsAppError did not recognise the permission error")
	}
	if strings.Contains(ae.Message(), "1SecretFileID") {
		t.Errorf("Message() = %q, want no Google file id", ae.Message())
	}
	if ae.Code() != apperror.CodeGooglePermissionDenied {
		t.Errorf("Code() = %q, want %q", ae.Code(), apperror.CodeGooglePermissionDenied)
	}
	if got := ae.HTTPStatus(); got != http.StatusForbidden {
		t.Errorf("HTTPStatus() = %d, want 403", got)
	}
	detail, ok := ae.Details()[0].(*apperrorv1.ErrorDetail)
	if !ok {
		t.Fatalf("Details()[0] = %T, want *apperrorv1.ErrorDetail", ae.Details()[0])
	}
	if detail.GetMeta()["file_id"] != "1SecretFileID" {
		t.Errorf("Meta[file_id] = %q, want the id kept for the log and the control plane", detail.GetMeta()["file_id"])
	}
}

// SECURITY: the data plane returns code and message only, so an id in the message reaches
// an API-key holder who supplied our UUID and never knew the Google id.
func TestAppError_NotFoundNamesNoGoogleFileID(t *testing.T) {
	ae, ok := apperror.AsAppError(gwerr.AppError(&gworkspace.NotFoundError{FileID: "1SecretFileID"}))
	if !ok {
		t.Fatal("AsAppError did not recognise the not-found error")
	}
	if strings.Contains(ae.Message(), "1SecretFileID") {
		t.Errorf("Message() = %q, want no Google file id", ae.Message())
	}
	if ae.Code() != apperror.CodeGoogleNotFound {
		t.Errorf("Code() = %q, want %q", ae.Code(), apperror.CodeGoogleNotFound)
	}
	detail, ok := ae.Details()[0].(*apperrorv1.ErrorDetail)
	if !ok {
		t.Fatalf("Details()[0] = %T, want *apperrorv1.ErrorDetail", ae.Details()[0])
	}
	if detail.GetMeta()["file_id"] != "1SecretFileID" {
		t.Errorf("Meta[file_id] = %q, want the id kept for the log and the control plane", detail.GetMeta()["file_id"])
	}
}

// Without these mappings a blank tab title and a bad row number reach the data plane as a 500.
func TestAppError_WriterValidationFailuresAre400(t *testing.T) {
	for _, err := range []error{
		&gsheet.InvalidTabTitleError{Title: ""},
		&gsheet.InvalidRowIndexError{Row: -1},
	} {
		ae, ok := apperror.AsAppError(gwerr.AppError(err))
		if !ok {
			t.Fatalf("AsAppError did not recognise %T", err)
		}
		if got := ae.HTTPStatus(); got != http.StatusBadRequest {
			t.Errorf("%T: HTTPStatus() = %d, want 400", err, got)
		}
	}
}
