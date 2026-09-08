// Package gwerr maps gworkspace provider errors into the app's error envelope.
package gwerr

import (
	"errors"
	"fmt"
	"strconv"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/gworkspace"
	"altalune.id/opensheet/gworkspace/gsheet"
	"altalune.id/opensheet/internal/apperror"
)

// AppError attaches the wire envelope to a gworkspace or gsheet provider failure, returning any other error unchanged.
func AppError(err error) error {
	app := envelope(err)
	if app == nil {
		return err
	}
	return &providerError{cause: err, app: app}
}

type providerError struct {
	cause error
	app   *apperror.AppError
}

func (e *providerError) Error() string { return e.cause.Error() }

func (e *providerError) Unwrap() error { return e.cause }

// ToAppError converts the provider failure into the wire envelope.
func (e *providerError) ToAppError() *apperror.AppError { return e.app }

func envelope(err error) *apperror.AppError {
	if e, ok := errors.AsType[*gworkspace.NotFoundError](err); ok {
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
	if e, ok := errors.AsType[*gworkspace.PermissionDeniedError](err); ok {
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
	if e, ok := errors.AsType[*gworkspace.QuotaExceededError](err); ok {
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
	if _, ok := errors.AsType[*gworkspace.UnavailableError](err); ok {
		return apperror.New(
			apperror.CodeGoogleUnavailable,
			"Google Sheets is unavailable; try again shortly",
			codes.Unavailable,
			&apperrorv1.ErrorDetail{Code: apperror.CodeGoogleUnavailable},
		)
	}
	if _, ok := errors.AsType[*gworkspace.AuthExpiredError](err); ok {
		return apperror.New(
			apperror.CodeCredentialReauthNeeded,
			"Google rejected this credential; reconnect it",
			codes.FailedPrecondition,
			&apperrorv1.ErrorDetail{Code: apperror.CodeCredentialReauthNeeded},
		)
	}
	if e, ok := errors.AsType[*gsheet.TabNotFoundError](err); ok {
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
	return nil
}
