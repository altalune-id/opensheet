package tokens

import (
	"errors"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
)

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
