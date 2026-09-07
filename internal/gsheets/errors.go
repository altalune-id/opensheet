package gsheets

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"google.golang.org/api/googleapi"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
)

// NotFoundError reports that no spreadsheet matches FileID.
type NotFoundError struct{ FileID string }

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("gsheets: spreadsheet %q: not found", e.FileID)
}

// ToAppError converts the typed error into the wire envelope.
func (e *NotFoundError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeGoogleNotFound,
		fmt.Sprintf("Google spreadsheet %q not found", e.FileID),
		codes.NotFound,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeGoogleNotFound,
			Meta: map[string]string{"file_id": e.FileID},
		},
	)
}

// IsNotFoundError reports whether err's chain contains a *NotFoundError.
func IsNotFoundError(err error) bool {
	_, ok := errors.AsType[*NotFoundError](err)
	return ok
}

// PermissionDeniedError reports that the credential may not read FileID.
type PermissionDeniedError struct{ FileID string }

func (e *PermissionDeniedError) Error() string {
	return fmt.Sprintf("gsheets: spreadsheet %q: permission denied", e.FileID)
}

// ToAppError converts the typed error into the wire envelope.
func (e *PermissionDeniedError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeGooglePermissionDenied,
		fmt.Sprintf("Google denied access to spreadsheet %q; share it with this credential", e.FileID),
		codes.PermissionDenied,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeGooglePermissionDenied,
			Meta: map[string]string{"file_id": e.FileID},
		},
	)
}

// IsPermissionDeniedError reports whether err's chain contains a *PermissionDeniedError.
func IsPermissionDeniedError(err error) bool {
	_, ok := errors.AsType[*PermissionDeniedError](err)
	return ok
}

// QuotaExceededError reports that Google is rate limiting this credential.
type QuotaExceededError struct{ RetryAfter time.Duration }

func (e *QuotaExceededError) Error() string {
	if e.RetryAfter <= 0 {
		return "gsheets: quota: exceeded"
	}
	return "gsheets: quota: exceeded, retry after " + e.RetryAfter.String()
}

// ToAppError converts the typed error into the wire envelope.
func (e *QuotaExceededError) ToAppError() *apperror.AppError {
	meta := map[string]string{}
	if e.RetryAfter > 0 {
		meta["retry_after_seconds"] = strconv.Itoa(int(e.RetryAfter.Seconds()))
	}
	return apperror.New(
		apperror.CodeGoogleQuotaExceeded,
		"Google Sheets rate limit exceeded; try again shortly",
		codes.ResourceExhausted,
		&apperrorv1.ErrorDetail{Code: apperror.CodeGoogleQuotaExceeded, Meta: meta},
	)
}

// IsQuotaExceededError reports whether err's chain contains a *QuotaExceededError.
func IsQuotaExceededError(err error) bool {
	_, ok := errors.AsType[*QuotaExceededError](err)
	return ok
}

// UnavailableError reports that the Sheets API could not be reached or failed unexpectedly.
type UnavailableError struct{ Cause error }

func (e *UnavailableError) Error() string {
	if e.Cause == nil {
		return "gsheets: google api: unavailable"
	}
	return "gsheets: google api: " + e.Cause.Error()
}

func (e *UnavailableError) Unwrap() error { return e.Cause }

// ToAppError converts the typed error into the wire envelope.
func (*UnavailableError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeGoogleUnavailable,
		"Google Sheets is unavailable; try again shortly",
		codes.Unavailable,
		&apperrorv1.ErrorDetail{Code: apperror.CodeGoogleUnavailable},
	)
}

// IsUnavailableError reports whether err's chain contains an *UnavailableError.
func IsUnavailableError(err error) bool {
	_, ok := errors.AsType[*UnavailableError](err)
	return ok
}

// AuthExpiredError reports that the credential was rejected and needs reauthorization.
type AuthExpiredError struct{ Cause error }

func (e *AuthExpiredError) Error() string {
	if e.Cause == nil {
		return "gsheets: credential: expired"
	}
	return "gsheets: credential: " + e.Cause.Error()
}

func (e *AuthExpiredError) Unwrap() error { return e.Cause }

// ToAppError converts the typed error into the wire envelope.
func (*AuthExpiredError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeCredentialReauthNeeded,
		"Google rejected this credential; reconnect it",
		codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{Code: apperror.CodeCredentialReauthNeeded},
	)
}

// IsAuthExpiredError reports whether err's chain contains an *AuthExpiredError.
func IsAuthExpiredError(err error) bool {
	_, ok := errors.AsType[*AuthExpiredError](err)
	return ok
}

// TabNotFoundError reports that the spreadsheet has no tab named Tab.
type TabNotFoundError struct{ Tab string }

func (e *TabNotFoundError) Error() string {
	return fmt.Sprintf("gsheets: tab %q: not found", e.Tab)
}

// ToAppError converts the typed error into the wire envelope.
func (e *TabNotFoundError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetTabNotFound,
		fmt.Sprintf("Tab %q not found in this spreadsheet", e.Tab),
		codes.NotFound,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetTabNotFound,
			Meta: map[string]string{"tab": e.Tab},
		},
	)
}

// IsTabNotFoundError reports whether err's chain contains a *TabNotFoundError.
func IsTabNotFoundError(err error) bool {
	_, ok := errors.AsType[*TabNotFoundError](err)
	return ok
}

func translate(err error, fileID string) error {
	var g *googleapi.Error
	if !errors.As(err, &g) {
		return &UnavailableError{Cause: err}
	}
	switch g.Code {
	case http.StatusNotFound:
		return &NotFoundError{FileID: fileID}
	case http.StatusForbidden:
		return &PermissionDeniedError{FileID: fileID}
	case http.StatusTooManyRequests:
		return &QuotaExceededError{RetryAfter: retryAfter(g)}
	case http.StatusUnauthorized:
		return &AuthExpiredError{Cause: err}
	}
	return &UnavailableError{Cause: err}
}

func translateRange(err error, fileID, tab string) error {
	var g *googleapi.Error
	if errors.As(err, &g) && g.Code == http.StatusBadRequest {
		return &TabNotFoundError{Tab: tab}
	}
	return translate(err, fileID)
}

// https://developers.google.com/workspace/sheets/api/limits
func retryAfter(g *googleapi.Error) time.Duration {
	raw := g.Header.Get("Retry-After")
	if raw == "" {
		return 0
	}
	if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if at, err := http.ParseTime(raw); err == nil {
		if d := time.Until(at); d > 0 {
			return d
		}
	}
	return 0
}
