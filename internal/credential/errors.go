package credential

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
)

// NotFoundError reports that no credential matches ID in the caller's scope.
type NotFoundError struct{ ID string }

func (e *NotFoundError) Error() string { return fmt.Sprintf("credential: %q: not found", e.ID) }

// ToAppError converts the typed error into the wire envelope.
func (e *NotFoundError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeCredentialNotFound,
		fmt.Sprintf("Credential %q not found", e.ID),
		codes.NotFound,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeCredentialNotFound,
			Meta: map[string]string{"credential_id": e.ID},
		},
	)
}

// IsNotFoundError reports whether err's chain contains a *NotFoundError.
func IsNotFoundError(err error) bool {
	_, ok := errors.AsType[*NotFoundError](err)
	return ok
}

// AlreadyExistsError reports that the project already has a credential with that name.
type AlreadyExistsError struct{ Name string }

func (e *AlreadyExistsError) Error() string {
	return fmt.Sprintf("credential: name %q: already exists in this project", e.Name)
}

// ToAppError converts the typed error into the wire envelope.
func (e *AlreadyExistsError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeCredentialAlreadyExists,
		"A credential with that name already exists in this project",
		codes.AlreadyExists,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeCredentialAlreadyExists,
			Meta: map[string]string{"name": e.Name},
		},
	)
}

// IsAlreadyExistsError reports whether err's chain contains an *AlreadyExistsError.
func IsAlreadyExistsError(err error) bool {
	_, ok := errors.AsType[*AlreadyExistsError](err)
	return ok
}

// InvalidNameError reports that a name violates a creation invariant.
type InvalidNameError struct{ Reason string }

func (e *InvalidNameError) Error() string { return "credential: name: " + e.Reason }

// ToAppError converts the typed error into the wire envelope.
func (e *InvalidNameError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeCredentialInvalidName,
		"Invalid credential name: "+e.Reason,
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeCredentialInvalidName,
			Meta: map[string]string{"reason": e.Reason},
		},
	)
}

// IsInvalidNameError reports whether err's chain contains an *InvalidNameError.
func IsInvalidNameError(err error) bool {
	_, ok := errors.AsType[*InvalidNameError](err)
	return ok
}

// InvalidKindError reports a credential kind opensheet does not accept.
type InvalidKindError struct{ Kind string }

func (e *InvalidKindError) Error() string {
	return fmt.Sprintf("credential: kind: %q is not a supported credential kind", e.Kind)
}

// ToAppError converts the typed error into the wire envelope.
func (e *InvalidKindError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeCredentialInvalidKind,
		fmt.Sprintf("Unsupported credential kind %q", e.Kind),
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeCredentialInvalidKind,
			Meta: map[string]string{"kind": e.Kind},
		},
	)
}

// IsInvalidKindError reports whether err's chain contains an *InvalidKindError.
func IsInvalidKindError(err error) bool {
	_, ok := errors.AsType[*InvalidKindError](err)
	return ok
}

// InvalidServiceAccountError reports that an uploaded key is not a usable Google service account.
type InvalidServiceAccountError struct{ Reason string }

// SECURITY: Reason is a fixed phrase; it must never quote the uploaded payload.
func (e *InvalidServiceAccountError) Error() string {
	return "credential: service account: " + e.Reason
}

// ToAppError converts the typed error into the wire envelope.
func (e *InvalidServiceAccountError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeCredentialInvalidKind,
		"That file is not a usable Google service account key: "+e.Reason,
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeCredentialInvalidKind,
			Meta: map[string]string{"reason": e.Reason},
		},
	)
}

// IsInvalidServiceAccountError reports whether err's chain contains an *InvalidServiceAccountError.
func IsInvalidServiceAccountError(err error) bool {
	_, ok := errors.AsType[*InvalidServiceAccountError](err)
	return ok
}

// InUseError reports that a spreadsheet still references the credential.
type InUseError struct{ ID string }

func (e *InUseError) Error() string {
	return fmt.Sprintf("credential: %q: still referenced by a spreadsheet", e.ID)
}

// ToAppError converts the typed error into the wire envelope.
func (e *InUseError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeCredentialInUse,
		"This credential is still used by a spreadsheet; remove the spreadsheet first",
		codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeCredentialInUse,
			Meta: map[string]string{"credential_id": e.ID},
		},
	)
}

// IsInUseError reports whether err's chain contains an *InUseError.
func IsInUseError(err error) bool {
	_, ok := errors.AsType[*InUseError](err)
	return ok
}

// ReauthNeededError reports that Google rejected the credential and a human must reconnect it.
type ReauthNeededError struct{ ID string }

func (e *ReauthNeededError) Error() string {
	return fmt.Sprintf("credential: %q: needs reauthorization", e.ID)
}

// ToAppError converts the typed error into the wire envelope.
func (e *ReauthNeededError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeCredentialReauthNeeded,
		"This credential needs to be reconnected to Google",
		codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeCredentialReauthNeeded,
			Meta: map[string]string{"credential_id": e.ID},
		},
	)
}

// IsReauthNeededError reports whether err's chain contains a *ReauthNeededError.
func IsReauthNeededError(err error) bool {
	_, ok := errors.AsType[*ReauthNeededError](err)
	return ok
}

// NotSealedError reports a credential carrying no ciphertext.
type NotSealedError struct{ Situation string }

func (e *NotSealedError) Error() string {
	return "credential: sealed: empty ciphertext on " + e.Situation
}

// ToAppError converts the typed error into the wire envelope.
func (e *NotSealedError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeCredentialNotSealed,
		"The credential carries no sealed secret",
		codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeCredentialNotSealed,
			Meta: map[string]string{"situation": e.Situation},
		},
	)
}

// IsNotSealedError reports whether err's chain contains a *NotSealedError.
func IsNotSealedError(err error) bool {
	_, ok := errors.AsType[*NotSealedError](err)
	return ok
}

// StateInvalidError reports that a Google connect state failed verification.
type StateInvalidError struct{ Reason string }

// SECURITY: Reason is a fixed phrase; it must never quote the received state.
func (e *StateInvalidError) Error() string { return "credential: connect state: " + e.Reason }

// ToAppError converts the typed error into the wire envelope.
func (e *StateInvalidError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeCredentialStateInvalid,
		"This Google connection attempt is no longer valid; start again",
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeCredentialStateInvalid,
			Meta: map[string]string{"reason": e.Reason},
		},
	)
}

// IsStateInvalidError reports whether err's chain contains a *StateInvalidError.
func IsStateInvalidError(err error) bool {
	_, ok := errors.AsType[*StateInvalidError](err)
	return ok
}

// NoRefreshTokenError reports that Google granted access without a refresh token, so the grant cannot outlive the browser.
type NoRefreshTokenError struct{}

func (*NoRefreshTokenError) Error() string {
	return "credential: connect: google returned no refresh token"
}

// ToAppError converts the typed error into the wire envelope.
func (*NoRefreshTokenError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeCredentialReauthNeeded,
		"Google did not return a refresh token; revoke opensheet's access in your Google account and connect again",
		codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{Code: apperror.CodeCredentialReauthNeeded},
	)
}

// IsNoRefreshTokenError reports whether err's chain contains a *NoRefreshTokenError.
func IsNoRefreshTokenError(err error) bool {
	_, ok := errors.AsType[*NoRefreshTokenError](err)
	return ok
}

// NotConfiguredError reports that this deployment has no Google OAuth client, so no connect flow and no google_oauth token source.
type NotConfiguredError struct{}

func (*NotConfiguredError) Error() string {
	return "credential: google oauth: not configured on this deployment"
}

// ToAppError converts the typed error into the wire envelope.
// CodeCredentialGoogleNotConfigured once internal/apperror and docs/ERROR_CODES.md carry it.
func (*NotConfiguredError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeCredentialGoogleNotConfigured,
		"Connecting a Google account is not available on this deployment",
		codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{Code: apperror.CodeCredentialGoogleNotConfigured},
	)
}

// IsNotConfiguredError reports whether err's chain contains a *NotConfiguredError.
func IsNotConfiguredError(err error) bool {
	_, ok := errors.AsType[*NotConfiguredError](err)
	return ok
}
