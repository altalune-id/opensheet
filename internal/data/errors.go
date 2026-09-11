package data

import (
	"errors"
	"strconv"

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

// PayloadTooLargeError reports a request body over the data plane's cap.
type PayloadTooLargeError struct{ Limit int64 }

func (e *PayloadTooLargeError) Error() string {
	return "data: payload too large: over " + strconv.FormatInt(e.Limit, 10) + " bytes"
}

// ToAppError maps PayloadTooLargeError to the shared oversize-payload envelope, which overrides to 413.
func (e *PayloadTooLargeError) ToAppError() *apperror.AppError {
	limit := strconv.FormatInt(e.Limit, 10)
	return apperror.New(
		apperror.CodeSheetPayloadTooLarge,
		"The request body is over the "+limit+" byte limit",
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetPayloadTooLarge,
			Meta: map[string]string{"limit_bytes": limit},
		},
	)
}

// IsPayloadTooLargeError reports whether err's tree contains a *PayloadTooLargeError.
func IsPayloadTooLargeError(err error) bool {
	_, ok := errors.AsType[*PayloadTooLargeError](err)
	return ok
}

// InvalidBodyError reports a request body this surface cannot accept.
type InvalidBodyError struct{ Reason string }

func (e *InvalidBodyError) Error() string { return "data: invalid body: " + e.Reason }

// ToAppError maps InvalidBodyError to the canonical validation envelope.
func (e *InvalidBodyError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeValidation,
		"Invalid request body: "+e.Reason,
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{Code: apperror.CodeValidation},
	)
}

// IsInvalidBodyError reports whether err's tree contains a *InvalidBodyError.
func IsInvalidBodyError(err error) bool {
	_, ok := errors.AsType[*InvalidBodyError](err)
	return ok
}

// InvalidCellError reports a cell the caller asked to be numeric that is not a finite number.
type InvalidCellError struct {
	Column string
	Reason string
}

func (e *InvalidCellError) Error() string {
	return "data: invalid cell: column " + strconv.Quote(e.Column) + ": " + e.Reason
}

// ToAppError maps InvalidCellError to the sheet module's invalid-row envelope.
func (e *InvalidCellError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetInvalidRow,
		"Column "+strconv.Quote(e.Column)+" must be a finite number",
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetInvalidRow,
			Meta: map[string]string{"column": e.Column},
		},
	)
}

// IsInvalidCellError reports whether err's tree contains a *InvalidCellError.
func IsInvalidCellError(err error) bool {
	_, ok := errors.AsType[*InvalidCellError](err)
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

// UnknownQueryParamError reports a query parameter this surface does not understand.
type UnknownQueryParamError struct{ Param string }

func (e *UnknownQueryParamError) Error() string {
	return "data: unknown query parameter " + strconv.Quote(e.Param)
}

// ToAppError maps UnknownQueryParamError to the canonical InvalidArgument envelope.
func (e *UnknownQueryParamError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetUnknownQueryParam,
		"This endpoint does not understand the query parameter "+strconv.Quote(e.Param),
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetUnknownQueryParam,
			Meta: map[string]string{"param": e.Param},
		},
	)
}

// IsUnknownQueryParamError reports whether err's tree contains an *UnknownQueryParamError.
func IsUnknownQueryParamError(err error) bool {
	_, ok := errors.AsType[*UnknownQueryParamError](err)
	return ok
}
