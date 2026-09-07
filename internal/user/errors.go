package user

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/template/gen/go/apperror/v1"
	"altalune.id/template/internal/apperror"
)

// SingletonOrgMissingError signals the selfhosted singleton org has not been provisioned yet — the caller (typically OnboardWorkflow) should treat it as "onboarding still needs to run".
type SingletonOrgMissingError struct {
	Slug string
}

func (e *SingletonOrgMissingError) Error() string {
	if e == nil || e.Slug == "" {
		return "user: singleton org not provisioned"
	}
	return fmt.Sprintf("user: singleton org not provisioned: slug=%q", e.Slug)
}

// IsSingletonOrgMissingError reports whether err's tree contains a *SingletonOrgMissingError.
func IsSingletonOrgMissingError(err error) bool {
	_, ok := errors.AsType[*SingletonOrgMissingError](err)
	return ok
}

// NotFoundError signals no user matched the lookup.
type NotFoundError struct {
	ID    string
	Email string
}

func (e *NotFoundError) Error() string {
	switch {
	case e.ID != "":
		return "user: not found: id=" + e.ID
	case e.Email != "":
		return "user: not found: email=" + e.Email
	default:
		return "user: not found"
	}
}

// ToAppError maps NotFoundError to the canonical NotFound envelope.
func (e *NotFoundError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeUserNotFound,
		"User not found",
		codes.NotFound,
		&apperrorv1.ErrorDetail{Code: apperror.CodeUserNotFound},
	)
}

// IsNotFoundError reports whether err's tree contains a *NotFoundError.
func IsNotFoundError(err error) bool {
	_, ok := errors.AsType[*NotFoundError](err)
	return ok
}

// AlreadyExistsError signals a uniqueness violation on save.
type AlreadyExistsError struct {
	Field string
	Value string
}

func (e *AlreadyExistsError) Error() string {
	if e.Field == "" {
		return "user: already exists"
	}
	return fmt.Sprintf("user: already exists: %s=%q", e.Field, e.Value)
}

// ToAppError maps AlreadyExistsError to the canonical AlreadyExists envelope.
func (e *AlreadyExistsError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeUserAlreadyExists,
		"User already exists",
		codes.AlreadyExists,
		&apperrorv1.ErrorDetail{Code: apperror.CodeUserAlreadyExists},
	)
}

// IsAlreadyExistsError reports whether err's tree contains an *AlreadyExistsError.
func IsAlreadyExistsError(err error) bool {
	_, ok := errors.AsType[*AlreadyExistsError](err)
	return ok
}

// NotInvitedError signals a selfhosted OIDC login with no membership and no invite.
type NotInvitedError struct {
	Email string
}

func (e *NotInvitedError) Error() string {
	if e.Email == "" {
		return "user: not invited"
	}
	return "user: not invited: email=" + e.Email
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

// InvalidEmailError signals a malformed or empty email.
type InvalidEmailError struct {
	Reason string
	Value  string
}

func (e *InvalidEmailError) Error() string {
	reason := e.Reason
	if reason == "" {
		reason = "invalid"
	}
	if e.Value == "" {
		return "user: email: " + reason
	}
	return fmt.Sprintf("user: email: %s: %q", reason, e.Value)
}

// ToAppError maps InvalidEmailError to the canonical InvalidArgument envelope.
func (e *InvalidEmailError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeUserInvalidEmail,
		"Invalid user email",
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{Code: apperror.CodeUserInvalidEmail},
	)
}

// IsInvalidEmailError reports whether err's tree contains an *InvalidEmailError.
func IsInvalidEmailError(err error) bool {
	_, ok := errors.AsType[*InvalidEmailError](err)
	return ok
}

// SignupRequiredError signals a cloud OIDC login where the user has no membership and no pending invite; the caller should route to /signup/complete.
type SignupRequiredError struct {
	UserID string
	Email  string
}

func (e *SignupRequiredError) Error() string {
	if e.Email == "" {
		return "user: signup required"
	}
	return "user: signup required: email=" + e.Email
}

// ToAppError maps SignupRequiredError to the canonical FailedPrecondition envelope.
func (e *SignupRequiredError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSignupRequired,
		"Signup completion required",
		codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{Code: apperror.CodeSignupRequired},
	)
}

// IsSignupRequiredError reports whether err's tree contains a *SignupRequiredError.
func IsSignupRequiredError(err error) bool {
	_, ok := errors.AsType[*SignupRequiredError](err)
	return ok
}

// InvalidNameError signals an empty or invalid display name.
type InvalidNameError struct {
	Reason string
}

func (e *InvalidNameError) Error() string {
	reason := e.Reason
	if reason == "" {
		reason = "invalid"
	}
	return "user: name: " + reason
}

// ToAppError maps InvalidNameError to the canonical InvalidArgument envelope.
func (e *InvalidNameError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeUserInvalidName,
		"Invalid user name",
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{Code: apperror.CodeUserInvalidName},
	)
}

// IsInvalidNameError reports whether err's tree contains an *InvalidNameError.
func IsInvalidNameError(err error) bool {
	_, ok := errors.AsType[*InvalidNameError](err)
	return ok
}
