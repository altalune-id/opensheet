package gworkspace

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"google.golang.org/api/googleapi"
)

// NotFoundError reports that no Google document matches FileID.
type NotFoundError struct{ FileID string }

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("gworkspace: document %q: not found", e.FileID)
}

// IsNotFoundError reports whether err's chain contains a *NotFoundError.
func IsNotFoundError(err error) bool {
	_, ok := errors.AsType[*NotFoundError](err)
	return ok
}

// PermissionDeniedError reports that the credential may not read FileID.
type PermissionDeniedError struct{ FileID string }

func (e *PermissionDeniedError) Error() string {
	return fmt.Sprintf("gworkspace: document %q: permission denied", e.FileID)
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
		return "gworkspace: quota: exceeded"
	}
	return "gworkspace: quota: exceeded, retry after " + e.RetryAfter.String()
}

// IsQuotaExceededError reports whether err's chain contains a *QuotaExceededError.
func IsQuotaExceededError(err error) bool {
	_, ok := errors.AsType[*QuotaExceededError](err)
	return ok
}

// UnavailableError reports that the Google API could not be reached or failed unexpectedly.
type UnavailableError struct{ Cause error }

func (e *UnavailableError) Error() string {
	if e.Cause == nil {
		return "gworkspace: google api: unavailable"
	}
	return "gworkspace: google api: " + e.Cause.Error()
}

func (e *UnavailableError) Unwrap() error { return e.Cause }

// IsUnavailableError reports whether err's chain contains an *UnavailableError.
func IsUnavailableError(err error) bool {
	_, ok := errors.AsType[*UnavailableError](err)
	return ok
}

// AuthExpiredError reports that the credential was rejected and needs reauthorization.
type AuthExpiredError struct{ Cause error }

func (e *AuthExpiredError) Error() string {
	if e.Cause == nil {
		return "gworkspace: credential: expired"
	}
	return "gworkspace: credential: " + e.Cause.Error()
}

func (e *AuthExpiredError) Unwrap() error { return e.Cause }

// IsAuthExpiredError reports whether err's chain contains an *AuthExpiredError.
func IsAuthExpiredError(err error) bool {
	_, ok := errors.AsType[*AuthExpiredError](err)
	return ok
}

// Translate maps a Google API failure into this package's typed errors; fileID names the document the call was for.
func Translate(err error, fileID string) error {
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
