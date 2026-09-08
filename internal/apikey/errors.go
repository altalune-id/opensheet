package apikey

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
)

// NotFoundError reports that no API key matches ID.
type NotFoundError struct{ ID string }

func (e *NotFoundError) Error() string { return fmt.Sprintf("apikey: %q: not found", e.ID) }

// ToAppError converts the typed error into the wire envelope.
func (e *NotFoundError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeAPIKeyNotFound,
		fmt.Sprintf("API key %q not found", e.ID),
		codes.NotFound,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeAPIKeyNotFound,
			Meta: map[string]string{"api_key_id": e.ID},
		},
	)
}

// IsNotFoundError reports whether err's chain contains a *NotFoundError.
func IsNotFoundError(err error) bool {
	_, ok := errors.AsType[*NotFoundError](err)
	return ok
}

// AlreadyExistsError reports a unique-constraint collision on Field.
type AlreadyExistsError struct{ Field, Value string }

func (e *AlreadyExistsError) Error() string { return "apikey: " + e.Field + ": already exists" }

// ToAppError converts the typed error into the wire envelope.
func (e *AlreadyExistsError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeAlreadyExists,
		"API key "+e.Field+" already exists",
		codes.AlreadyExists,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeAlreadyExists,
			Meta: map[string]string{"field": e.Field},
		},
	)
}

// IsAlreadyExistsError reports whether err's chain contains an *AlreadyExistsError.
func IsAlreadyExistsError(err error) bool {
	_, ok := errors.AsType[*AlreadyExistsError](err)
	return ok
}

// InvalidNameError reports that name violates a creation invariant.
type InvalidNameError struct{ Reason string }

func (e *InvalidNameError) Error() string { return "apikey: name: " + e.Reason }

// ToAppError converts the typed error into the wire envelope.
func (e *InvalidNameError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeAPIKeyInvalidName,
		"Invalid API key name: "+e.Reason,
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeAPIKeyInvalidName,
			Meta: map[string]string{"reason": e.Reason},
		},
	)
}

// IsInvalidNameError reports whether err's chain contains an *InvalidNameError.
func IsInvalidNameError(err error) bool {
	_, ok := errors.AsType[*InvalidNameError](err)
	return ok
}

// InvalidScopeError reports that the requested scope set is unusable.
type InvalidScopeError struct{ Scope, Reason string }

func (e *InvalidScopeError) Error() string { return "apikey: scopes: " + e.Reason }

// ToAppError converts the typed error into the wire envelope.
func (e *InvalidScopeError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeAPIKeyInvalidScope,
		"Invalid API key scopes: "+e.Reason,
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeAPIKeyInvalidScope,
			Meta: map[string]string{"reason": e.Reason, "scope": e.Scope},
		},
	)
}

// IsInvalidScopeError reports whether err's chain contains an *InvalidScopeError.
func IsInvalidScopeError(err error) bool {
	_, ok := errors.AsType[*InvalidScopeError](err)
	return ok
}

// InvalidExpiryError reports that expiresAt is not strictly in the future.
type InvalidExpiryError struct{ Reason string }

func (e *InvalidExpiryError) Error() string { return "apikey: expires at: " + e.Reason }

// ToAppError converts the typed error into the wire envelope.
func (e *InvalidExpiryError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeAPIKeyInvalidScope,
		"Invalid API key expiry: "+e.Reason,
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeAPIKeyInvalidScope,
			Meta: map[string]string{"reason": e.Reason},
		},
	)
}

// IsInvalidExpiryError reports whether err's chain contains an *InvalidExpiryError.
func IsInvalidExpiryError(err error) bool {
	_, ok := errors.AsType[*InvalidExpiryError](err)
	return ok
}

// UnknownSheetError reports that a granted sheet does not belong to the key's project.
type UnknownSheetError struct{ SheetID string }

func (e *UnknownSheetError) Error() string {
	return fmt.Sprintf("apikey: sheet ids: %q is not in this project", e.SheetID)
}

// ToAppError converts the typed error into the wire envelope.
func (e *UnknownSheetError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeAPIKeyInvalidScope,
		"Sheet "+e.SheetID+" is not in this project",
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeAPIKeyInvalidScope,
			Meta: map[string]string{"sheet_id": e.SheetID},
		},
	)
}

// IsUnknownSheetError reports whether err's chain contains an *UnknownSheetError.
func IsUnknownSheetError(err error) bool {
	_, ok := errors.AsType[*UnknownSheetError](err)
	return ok
}

// UnauthorizedError reports that a presented key was not accepted.
type UnauthorizedError struct{}

// SECURITY: the message must not distinguish missing, malformed, unknown, revoked or expired keys.
func (*UnauthorizedError) Error() string { return "apikey: unauthorized" }

// ToAppError converts the typed error into the wire envelope.
func (*UnauthorizedError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeAPIKeyUnauthorized,
		"Unauthorized",
		codes.Unauthenticated,
		&apperrorv1.ErrorDetail{Code: apperror.CodeAPIKeyUnauthorized},
	)
}

// IsUnauthorizedError reports whether err's chain contains an *UnauthorizedError.
func IsUnauthorizedError(err error) bool {
	_, ok := errors.AsType[*UnauthorizedError](err)
	return ok
}
