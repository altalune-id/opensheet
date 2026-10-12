package apikey

import (
	"errors"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
)

// NotFoundError reports a key that does not exist or is outside the caller's reach.
type NotFoundError struct{}

func (*NotFoundError) Error() string { return "apikey: not found" }

// IsNotFoundError reports whether err is a *NotFoundError.
func IsNotFoundError(err error) bool {
	_, ok := errors.AsType[*NotFoundError](err)
	return ok
}

// UnknownScopeError names a scope outside the authn catalog.
type UnknownScopeError struct{ Scope string }

func (e *UnknownScopeError) Error() string { return "apikey: unknown scope " + e.Scope }

// ToAppError maps UnknownScopeError to its InvalidArgument envelope.
func (e *UnknownScopeError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeAPIKeyUnknownScope, "Unknown scope: "+e.Scope, codes.InvalidArgument,
		&apperrorv1.ErrorDetail{Code: apperror.CodeAPIKeyUnknownScope, Meta: map[string]string{"field": "scopes"}})
}

// IsUnknownScopeError reports whether err is a *UnknownScopeError.
func IsUnknownScopeError(err error) bool {
	_, ok := errors.AsType[*UnknownScopeError](err)
	return ok
}

// RetiredScopeError names a scope that still validates on existing keys but may not be granted to a new one.
type RetiredScopeError struct{ Scope string }

func (e *RetiredScopeError) Error() string { return "apikey: retired scope " + e.Scope }

// ToAppError maps RetiredScopeError to its InvalidArgument envelope.
func (e *RetiredScopeError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeAPIKeyRetiredScope, "This scope can no longer be granted: "+e.Scope, codes.InvalidArgument,
		&apperrorv1.ErrorDetail{Code: apperror.CodeAPIKeyRetiredScope, Meta: map[string]string{"field": "scopes"}})
}

// IsRetiredScopeError reports whether err is a *RetiredScopeError.
func IsRetiredScopeError(err error) bool {
	_, ok := errors.AsType[*RetiredScopeError](err)
	return ok
}

// ScopeLevelError names an org-level scope requested on a project key.
type ScopeLevelError struct{ Scope string }

func (e *ScopeLevelError) Error() string { return "apikey: scope " + e.Scope + " is org-level" }

// ToAppError maps ScopeLevelError to its InvalidArgument envelope.
func (e *ScopeLevelError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeAPIKeyScopeLevel, "This scope is organization-level and cannot go on a project key: "+e.Scope, codes.InvalidArgument,
		&apperrorv1.ErrorDetail{Code: apperror.CodeAPIKeyScopeLevel, Meta: map[string]string{"field": "scopes"}})
}

// IsScopeLevelError reports whether err is a *ScopeLevelError.
func IsScopeLevelError(err error) bool {
	_, ok := errors.AsType[*ScopeLevelError](err)
	return ok
}

// EmptyGrantError reports a grant that names no project and is not all projects.
type EmptyGrantError struct{}

func (*EmptyGrantError) Error() string { return "apikey: grant names no project" }

// ToAppError maps EmptyGrantError to its InvalidArgument envelope.
func (*EmptyGrantError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeAPIKeyEmptyGrant, "Choose at least one project, or grant all projects", codes.InvalidArgument,
		&apperrorv1.ErrorDetail{Code: apperror.CodeAPIKeyEmptyGrant, Meta: map[string]string{"field": "project_ids"}})
}

// IsEmptyGrantError reports whether err is a *EmptyGrantError.
func IsEmptyGrantError(err error) bool {
	_, ok := errors.AsType[*EmptyGrantError](err)
	return ok
}

// GrantConflictError reports a grant that is all projects and also names projects.
type GrantConflictError struct{}

func (*GrantConflictError) Error() string { return "apikey: grant is all projects and names projects" }

// ToAppError maps GrantConflictError to its InvalidArgument envelope.
func (*GrantConflictError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeAPIKeyGrantConflict, "Choose all projects or named projects, not both", codes.InvalidArgument,
		&apperrorv1.ErrorDetail{Code: apperror.CodeAPIKeyGrantConflict, Meta: map[string]string{"field": "project_ids"}})
}

// IsGrantConflictError reports whether err is a *GrantConflictError.
func IsGrantConflictError(err error) bool {
	_, ok := errors.AsType[*GrantConflictError](err)
	return ok
}

// BoundToProjectError reports a grant change on a project key, which is bound to its project for life.
type BoundToProjectError struct{}

func (*BoundToProjectError) Error() string { return "apikey: a project key is bound to its project" }

// ToAppError maps BoundToProjectError to its FailedPrecondition envelope.
func (*BoundToProjectError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeAPIKeyBoundToProject, "A project key is bound to its project", codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{Code: apperror.CodeAPIKeyBoundToProject})
}

// IsBoundToProjectError reports whether err is a *BoundToProjectError.
func IsBoundToProjectError(err error) bool {
	_, ok := errors.AsType[*BoundToProjectError](err)
	return ok
}

// AlreadyAllProjectsError reports a grant change on a key that already reaches every project.
type AlreadyAllProjectsError struct{}

func (*AlreadyAllProjectsError) Error() string { return "apikey: key already reaches all projects" }

// ToAppError maps AlreadyAllProjectsError to its FailedPrecondition envelope.
func (*AlreadyAllProjectsError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeAPIKeyAlreadyAllProjects, "This key already reaches all projects", codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{Code: apperror.CodeAPIKeyAlreadyAllProjects})
}

// IsAlreadyAllProjectsError reports whether err is a *AlreadyAllProjectsError.
func IsAlreadyAllProjectsError(err error) bool {
	_, ok := errors.AsType[*AlreadyAllProjectsError](err)
	return ok
}

// RevokedError reports a grant change on a revoked key.
type RevokedError struct{}

func (*RevokedError) Error() string { return "apikey: key is revoked" }

// ToAppError maps RevokedError to its FailedPrecondition envelope.
func (*RevokedError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeAPIKeyRevoked, "This key is revoked", codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{Code: apperror.CodeAPIKeyRevoked})
}

// IsRevokedError reports whether err is a *RevokedError.
func IsRevokedError(err error) bool {
	_, ok := errors.AsType[*RevokedError](err)
	return ok
}

// ProjectNotInOrgError names a granted project that is not a project of the key's org.
type ProjectNotInOrgError struct{ ProjectID string }

func (e *ProjectNotInOrgError) Error() string { return "apikey: project not in org: " + e.ProjectID }

// ToAppError maps ProjectNotInOrgError to its InvalidArgument envelope.
func (*ProjectNotInOrgError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeAPIKeyProjectNotInOrg, "A selected project is not in this organization", codes.InvalidArgument,
		&apperrorv1.ErrorDetail{Code: apperror.CodeAPIKeyProjectNotInOrg, Meta: map[string]string{"field": "project_ids"}})
}

// IsProjectNotInOrgError reports whether err is a *ProjectNotInOrgError.
func IsProjectNotInOrgError(err error) bool {
	_, ok := errors.AsType[*ProjectNotInOrgError](err)
	return ok
}

// ExpiryRequiredError reports a new key minted with no expiry.
type ExpiryRequiredError struct{}

func (*ExpiryRequiredError) Error() string { return "apikey: an expiry is required" }

// ToAppError maps ExpiryRequiredError to its InvalidArgument envelope.
func (*ExpiryRequiredError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeAPIKeyExpiryRequired, "An expiry is required", codes.InvalidArgument,
		&apperrorv1.ErrorDetail{Code: apperror.CodeAPIKeyExpiryRequired, Meta: map[string]string{"field": "expires_at"}})
}

// IsExpiryRequiredError reports whether err is a *ExpiryRequiredError.
func IsExpiryRequiredError(err error) bool {
	_, ok := errors.AsType[*ExpiryRequiredError](err)
	return ok
}

// ExpiryInPastError reports a new key whose expiry is not in the future.
type ExpiryInPastError struct{}

func (*ExpiryInPastError) Error() string { return "apikey: expiry must be in the future" }

// ToAppError maps ExpiryInPastError to its InvalidArgument envelope.
func (*ExpiryInPastError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeAPIKeyExpiryInPast, "The expiry must be in the future", codes.InvalidArgument,
		&apperrorv1.ErrorDetail{Code: apperror.CodeAPIKeyExpiryInPast, Meta: map[string]string{"field": "expires_at"}})
}

// IsExpiryInPastError reports whether err is a *ExpiryInPastError.
func IsExpiryInPastError(err error) bool {
	_, ok := errors.AsType[*ExpiryInPastError](err)
	return ok
}

// ExpiryTooLongError reports a new key whose expiry is further out than MaxLifetime.
type ExpiryTooLongError struct{}

func (*ExpiryTooLongError) Error() string { return "apikey: expiry is more than a year away" }

// ToAppError maps ExpiryTooLongError to its InvalidArgument envelope.
func (*ExpiryTooLongError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeAPIKeyExpiryTooLong, "The expiry must be within one year", codes.InvalidArgument,
		&apperrorv1.ErrorDetail{Code: apperror.CodeAPIKeyExpiryTooLong, Meta: map[string]string{"field": "expires_at"}})
}

// IsExpiryTooLongError reports whether err is a *ExpiryTooLongError.
func IsExpiryTooLongError(err error) bool {
	_, ok := errors.AsType[*ExpiryTooLongError](err)
	return ok
}
