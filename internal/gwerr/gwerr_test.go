package gwerr_test

import (
	"errors"
	"testing"
	"time"

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
			message: `Google spreadsheet "F" not found`,
			pred:    gworkspace.IsNotFoundError,
		},
		{
			name:    "permission denied",
			err:     &gworkspace.PermissionDeniedError{FileID: "F"},
			code:    apperror.CodeGooglePermissionDenied,
			message: `Google denied access to spreadsheet "F"; share it with this credential`,
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
