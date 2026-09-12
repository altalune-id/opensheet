package authn

import (
	"errors"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
)

// UnauthorizedError reports that no authenticator accepted the credential.
type UnauthorizedError struct{}

// SECURITY: the message must not distinguish missing, malformed, unknown, revoked or expired credentials.
func (*UnauthorizedError) Error() string { return "authn: unauthorized" }

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

// InsufficientScopeError reports that the principal lacks a required scope.
type InsufficientScopeError struct{ Scope string }

func (e *InsufficientScopeError) Error() string {
	return "authn: scope: missing " + e.Scope
}

// ToAppError converts the typed error into the wire envelope.
func (e *InsufficientScopeError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeAPIKeyInsufficientScope,
		"Missing required scope: "+e.Scope,
		codes.PermissionDenied,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeAPIKeyInsufficientScope,
			Meta: map[string]string{"scope": e.Scope},
		},
	)
}

// IsInsufficientScopeError reports whether err's chain contains an *InsufficientScopeError.
func IsInsufficientScopeError(err error) bool {
	_, ok := errors.AsType[*InsufficientScopeError](err)
	return ok
}
