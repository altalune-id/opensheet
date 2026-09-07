package auth

import (
	"errors"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/template/gen/go/apperror/v1"
	"altalune.id/template/internal/apperror"
)

// InvalidCredentialsError signals a failed local login. SECURITY: masks unknown-user vs wrong-password to avoid enumeration.
type InvalidCredentialsError struct{}

func (e *InvalidCredentialsError) Error() string { return "auth: invalid credentials" }

// ToAppError maps InvalidCredentialsError to the canonical Unauthenticated envelope.
func (e *InvalidCredentialsError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeAuthInvalidCredentials,
		"Invalid credentials",
		codes.Unauthenticated,
		&apperrorv1.ErrorDetail{Code: apperror.CodeAuthInvalidCredentials},
	)
}

// IsInvalidCredentialsError reports whether err's tree contains a *InvalidCredentialsError.
func IsInvalidCredentialsError(err error) bool {
	_, ok := errors.AsType[*InvalidCredentialsError](err)
	return ok
}

// OIDCUnavailableError signals OIDC login was attempted but no OIDC client is wired.
type OIDCUnavailableError struct{}

func (e *OIDCUnavailableError) Error() string { return "auth: oidc: unavailable" }

// ToAppError maps OIDCUnavailableError to the canonical FailedPrecondition envelope.
func (e *OIDCUnavailableError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeAuthOIDCUnavailable,
		"OIDC login is not configured",
		codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{Code: apperror.CodeAuthOIDCUnavailable},
	)
}

// IsOIDCUnavailableError reports whether err's tree contains a *OIDCUnavailableError.
func IsOIDCUnavailableError(err error) bool {
	_, ok := errors.AsType[*OIDCUnavailableError](err)
	return ok
}

// OIDCClaimMissingError signals a required OIDC claim was empty.
type OIDCClaimMissingError struct {
	Claim string
}

func (e *OIDCClaimMissingError) Error() string {
	if e.Claim == "" {
		return "auth: oidc: claim missing"
	}
	return "auth: oidc: claim missing: " + e.Claim
}

// ToAppError maps OIDCClaimMissingError to the canonical InvalidArgument envelope.
func (e *OIDCClaimMissingError) ToAppError() *apperror.AppError {
	meta := map[string]string{}
	if e.Claim != "" {
		meta["claim"] = e.Claim
	}
	return apperror.New(
		apperror.CodeAuthOIDCClaimMissing,
		"OIDC claim missing",
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{Code: apperror.CodeAuthOIDCClaimMissing, Meta: meta},
	)
}

// IsOIDCClaimMissingError reports whether err's tree contains a *OIDCClaimMissingError.
func IsOIDCClaimMissingError(err error) bool {
	_, ok := errors.AsType[*OIDCClaimMissingError](err)
	return ok
}

// NotInvitedError signals a selfhosted OIDC login with no membership and no invite.
type NotInvitedError struct {
	Email string
}

func (e *NotInvitedError) Error() string {
	if e.Email == "" {
		return "auth: not invited"
	}
	return "auth: not invited: email=" + e.Email
}

// ToAppError maps NotInvitedError to the canonical PermissionDenied envelope.
func (e *NotInvitedError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeUserNotInvited,
		"User has no membership and no pending invite",
		codes.PermissionDenied,
		&apperrorv1.ErrorDetail{Code: apperror.CodeUserNotInvited},
	)
}

// IsNotInvitedError reports whether err's tree contains a *NotInvitedError.
func IsNotInvitedError(err error) bool {
	_, ok := errors.AsType[*NotInvitedError](err)
	return ok
}
