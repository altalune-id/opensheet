package data

import (
	"errors"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
)

// NotFoundError is the one outcome a caller sees when no sheet is servable at the requested path.
type NotFoundError struct{ cause error }

func (e *NotFoundError) Error() string {
	if e.cause == nil {
		return "data: not found: no sheet is served at this path"
	}
	return "data: not found: " + e.cause.Error()
}

// Unwrap exposes the masked cause, which ToAppError keeps off the wire.
func (e *NotFoundError) Unwrap() error { return e.cause }

// ToAppError maps NotFoundError to the canonical NotFound envelope.
// SECURITY: it names no slug, org, project or reason, so every masked case is byte-identical on the wire.
func (e *NotFoundError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetNotFound,
		"Sheet not found",
		codes.NotFound,
		&apperrorv1.ErrorDetail{Code: apperror.CodeSheetNotFound},
	)
}

// IsNotFoundError reports whether err's tree contains a *NotFoundError.
func IsNotFoundError(err error) bool {
	_, ok := errors.AsType[*NotFoundError](err)
	return ok
}

// SECURITY: the caller must not learn which stage refused it, so a lookup or permission failure collapses
// to NotFoundError while an unexpected failure passes through.
func maskNotFound(err error) error {
	ae, ok := apperror.AsAppError(err)
	if !ok {
		return err
	}
	switch ae.GRPCCode() {
	case codes.NotFound, codes.PermissionDenied, codes.Unauthenticated:
		return &NotFoundError{cause: err}
	default:
		return err
	}
}

// SECURITY: the read path re-checks the capability, and its SHT006 names the slug and confirms the sheet
// exists, so on this surface it collapses to the same 404.
func maskPublicDisabled(err error) error {
	if ae, ok := apperror.AsAppError(err); ok && ae.Code() == apperror.CodeSheetPublicDisabled {
		return &NotFoundError{cause: err}
	}
	return err
}

func publicDisabled() error {
	return apperror.New(
		apperror.CodeSheetPublicDisabled,
		"Public sheets are disabled on this deployment",
		codes.FailedPrecondition,
	)
}
