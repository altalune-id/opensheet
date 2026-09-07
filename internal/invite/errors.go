package invite

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/template/gen/go/apperror/v1"
	"altalune.id/template/internal/apperror"
)

// NotFoundError reports that no invite matched the lookup.
type NotFoundError struct {
	ID string
}

func (e *NotFoundError) Error() string {
	if e.ID == "" {
		return "invite: not found"
	}
	return fmt.Sprintf("invite: not found: id=%s", e.ID)
}

// ToAppError converts the typed error into the wire envelope.
func (e *NotFoundError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeInviteNotFound,
		"Invite not found",
		codes.NotFound,
		&apperrorv1.ErrorDetail{Code: apperror.CodeInviteNotFound},
	)
}

// IsNotFoundError reports whether err's tree contains a *NotFoundError.
func IsNotFoundError(err error) bool {
	_, ok := errors.AsType[*NotFoundError](err)
	return ok
}

// ExpiredError reports the invite is past its TTL.
type ExpiredError struct {
	ID string
}

func (e *ExpiredError) Error() string {
	if e.ID == "" {
		return "invite: expired"
	}
	return fmt.Sprintf("invite: expired: id=%s", e.ID)
}

// ToAppError converts the typed error into the wire envelope.
func (e *ExpiredError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeInviteExpired,
		"Invite has expired",
		codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{Code: apperror.CodeInviteExpired},
	)
}

// IsExpiredError reports whether err's tree contains an *ExpiredError.
func IsExpiredError(err error) bool {
	_, ok := errors.AsType[*ExpiredError](err)
	return ok
}

// AlreadyUsedError reports the invite has already been consumed.
type AlreadyUsedError struct {
	ID string
}

func (e *AlreadyUsedError) Error() string {
	if e.ID == "" {
		return "invite: already used"
	}
	return fmt.Sprintf("invite: already used: id=%s", e.ID)
}

// ToAppError converts the typed error into the wire envelope.
func (e *AlreadyUsedError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeInviteAlreadyUsed,
		"Invite has already been used",
		codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{Code: apperror.CodeInviteAlreadyUsed},
	)
}

// IsAlreadyUsedError reports whether err's tree contains an *AlreadyUsedError.
func IsAlreadyUsedError(err error) bool {
	_, ok := errors.AsType[*AlreadyUsedError](err)
	return ok
}

// InvalidRoleError reports a role outside the enumerated Role set.
type InvalidRoleError struct {
	Role string
}

func (e *InvalidRoleError) Error() string {
	return fmt.Sprintf("invite: invalid role: %q", e.Role)
}

// ToAppError converts the typed error into the wire envelope.
func (e *InvalidRoleError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeInviteInvalidRole,
		"Invalid invite role",
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{Code: apperror.CodeInviteInvalidRole},
	)
}

// IsInvalidRoleError reports whether err's tree contains an *InvalidRoleError.
func IsInvalidRoleError(err error) bool {
	_, ok := errors.AsType[*InvalidRoleError](err)
	return ok
}

// InvalidEmailError reports a malformed or empty email.
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
		return "invite: email: " + reason
	}
	return fmt.Sprintf("invite: email: %s: %q", reason, e.Value)
}

// ToAppError converts the typed error into the wire envelope.
func (e *InvalidEmailError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeValidation,
		"Invalid invite email",
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{Code: apperror.CodeValidation},
	)
}

// IsInvalidEmailError reports whether err's tree contains an *InvalidEmailError.
func IsInvalidEmailError(err error) bool {
	_, ok := errors.AsType[*InvalidEmailError](err)
	return ok
}

// InvitesDisabledError reports that this deployment does not allow issuing invites.
type InvitesDisabledError struct {
	Reason string
}

func (e *InvitesDisabledError) Error() string {
	if e.Reason == "" {
		return "invite: disabled"
	}
	return "invite: disabled: " + e.Reason
}

// ToAppError converts the typed error into the wire envelope.
func (e *InvitesDisabledError) ToAppError() *apperror.AppError {
	meta := map[string]string{}
	if e.Reason != "" {
		meta["reason"] = e.Reason
	}
	return apperror.New(
		apperror.CodeInviteDisabled,
		"Invites are disabled in this deployment",
		codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{Code: apperror.CodeInviteDisabled, Meta: meta},
	)
}

// IsInvitesDisabledError reports whether err's tree contains an *InvitesDisabledError.
func IsInvitesDisabledError(err error) bool {
	_, ok := errors.AsType[*InvitesDisabledError](err)
	return ok
}

// TokenMismatchError reports that the presented token did not match any stored invite. SECURITY: mapped to NotFound on the wire so callers cannot learn which id was tried.
type TokenMismatchError struct{}

func (*TokenMismatchError) Error() string { return "invite: token mismatch" }

// ToAppError converts the typed error into the wire envelope.
func (*TokenMismatchError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeInviteNotFound,
		"Invite not found",
		codes.NotFound,
		&apperrorv1.ErrorDetail{Code: apperror.CodeInviteNotFound},
	)
}

// IsTokenMismatchError reports whether err's tree contains a *TokenMismatchError.
func IsTokenMismatchError(err error) bool {
	_, ok := errors.AsType[*TokenMismatchError](err)
	return ok
}
