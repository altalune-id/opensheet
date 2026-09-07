package tokens

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/template/gen/go/apperror/v1"
	"altalune.id/template/internal/apperror"
)

// MissingAuthError signals that the Authorization header is absent.
type MissingAuthError struct{}

func (*MissingAuthError) Error() string { return "tokens: missing Authorization header" }

// ToAppError maps MissingAuthError to the canonical Unauthenticated envelope.
func (*MissingAuthError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeUnauthenticated,
		"Missing Authorization header",
		codes.Unauthenticated,
		&apperrorv1.ErrorDetail{Code: apperror.CodeUnauthenticated},
	)
}

// IsMissingAuthError reports whether err's tree contains a *MissingAuthError.
func IsMissingAuthError(err error) bool {
	_, ok := errors.AsType[*MissingAuthError](err)
	return ok
}

// BadSchemeError signals that Authorization used a non-Bearer scheme.
type BadSchemeError struct {
	Scheme string
}

func (e *BadSchemeError) Error() string {
	if e.Scheme == "" {
		return "tokens: Authorization must use Bearer scheme"
	}
	return fmt.Sprintf("tokens: Authorization must use Bearer scheme (got %q)", e.Scheme)
}

// ToAppError maps BadSchemeError to the canonical Unauthenticated envelope.
func (*BadSchemeError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeUnauthenticated,
		"Authorization must use Bearer scheme",
		codes.Unauthenticated,
		&apperrorv1.ErrorDetail{Code: apperror.CodeUnauthenticated},
	)
}

// IsBadSchemeError reports whether err's tree contains a *BadSchemeError.
func IsBadSchemeError(err error) bool {
	_, ok := errors.AsType[*BadSchemeError](err)
	return ok
}

// InvalidTokenError signals that verification rejected the presented bearer token.
type InvalidTokenError struct {
	Reason string
	Cause  error
}

func (e *InvalidTokenError) Error() string {
	if e.Reason == "" {
		return "tokens: invalid token"
	}
	return "tokens: invalid token: " + e.Reason
}

func (e *InvalidTokenError) Unwrap() error { return e.Cause }

// ToAppError maps InvalidTokenError to the canonical Unauthenticated envelope.
func (*InvalidTokenError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeUnauthenticated,
		"Invalid access token",
		codes.Unauthenticated,
		&apperrorv1.ErrorDetail{Code: apperror.CodeUnauthenticated},
	)
}

// IsInvalidTokenError reports whether err's tree contains an *InvalidTokenError.
func IsInvalidTokenError(err error) bool {
	_, ok := errors.AsType[*InvalidTokenError](err)
	return ok
}

// ExpiredTokenError signals that the token failed verification because it was expired.
type ExpiredTokenError struct {
	Cause error
}

func (e *ExpiredTokenError) Error() string {
	if e.Cause == nil {
		return "tokens: token expired"
	}
	return "tokens: token expired: " + e.Cause.Error()
}

func (e *ExpiredTokenError) Unwrap() error { return e.Cause }

// ToAppError maps ExpiredTokenError to an Unauthenticated envelope with the token-expired code.
func (*ExpiredTokenError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeTokenExpired,
		"Access token expired",
		codes.Unauthenticated,
		&apperrorv1.ErrorDetail{Code: apperror.CodeTokenExpired},
	)
}

// IsExpiredTokenError reports whether err's tree contains an *ExpiredTokenError.
func IsExpiredTokenError(err error) bool {
	_, ok := errors.AsType[*ExpiredTokenError](err)
	return ok
}
