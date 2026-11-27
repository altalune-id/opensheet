package role

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
)

// NotFoundError signals no custom role with that id in the org.
type NotFoundError struct{ ID string }

func (e *NotFoundError) Error() string { return "role: not found: id=" + e.ID }

// ToAppError maps NotFoundError to ROL002 NotFound.
func (e *NotFoundError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeRoleNotFound, "Role not found", codes.NotFound,
		&apperrorv1.ErrorDetail{Code: apperror.CodeRoleNotFound, Meta: map[string]string{"role_id": e.ID}})
}

// IsNotFoundError reports whether err's tree contains a *NotFoundError.
func IsNotFoundError(err error) bool {
	_, ok := errors.AsType[*NotFoundError](err)
	return ok
}

// InUseError refuses deleting a role a member or invite still holds.
type InUseError struct{ ID string }

func (e *InUseError) Error() string { return "role: in use: id=" + e.ID }

// ToAppError maps InUseError to ROL003 FailedPrecondition.
func (e *InUseError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeRoleInUse, "This role is still held by a member or invite. Change their role first.", codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{Code: apperror.CodeRoleInUse, Meta: map[string]string{"role_id": e.ID}})
}

// IsInUseError reports whether err's tree contains a *InUseError.
func IsInUseError(err error) bool {
	_, ok := errors.AsType[*InUseError](err)
	return ok
}

// InvalidPermissionError refuses a permission that is not an exact catalogue entry.
type InvalidPermissionError struct{ Permission string }

func (e *InvalidPermissionError) Error() string {
	return fmt.Sprintf("role: invalid permission %q", e.Permission)
}

// ToAppError maps InvalidPermissionError to ROL005 InvalidArgument.
func (e *InvalidPermissionError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeRoleInvalidPermission, fmt.Sprintf("%q is not a permission.", e.Permission), codes.InvalidArgument,
		&apperrorv1.ErrorDetail{Code: apperror.CodeRoleInvalidPermission, Meta: map[string]string{"permission": e.Permission}})
}

// IsInvalidPermissionError reports whether err's tree contains a *InvalidPermissionError.
func IsInvalidPermissionError(err error) bool {
	_, ok := errors.AsType[*InvalidPermissionError](err)
	return ok
}

// SystemOrgError refuses custom roles in the cloud system org, whose built-in roles decide who is platform staff.
type SystemOrgError struct{}

func (*SystemOrgError) Error() string { return "role: custom roles are refused in the system org" }

// ToAppError maps SystemOrgError to ROL006 FailedPrecondition.
func (*SystemOrgError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeRoleSystemOrg, "The system organization can't have custom roles.", codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{Code: apperror.CodeRoleSystemOrg})
}

// IsSystemOrgError reports whether err's tree contains a *SystemOrgError.
func IsSystemOrgError(err error) bool {
	_, ok := errors.AsType[*SystemOrgError](err)
	return ok
}

// InvalidNameError refuses a role name that is blank or longer than MaxNameLen characters.
type InvalidNameError struct{ Name string }

func (e *InvalidNameError) Error() string { return fmt.Sprintf("role: invalid name %q", e.Name) }

// ToAppError maps InvalidNameError to ROL007 InvalidArgument.
func (e *InvalidNameError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeRoleInvalidName, fmt.Sprintf("A role name needs 1 to %d characters.", MaxNameLen), codes.InvalidArgument,
		&apperrorv1.ErrorDetail{Code: apperror.CodeRoleInvalidName})
}

// IsInvalidNameError reports whether err's tree contains a *InvalidNameError.
func IsInvalidNameError(err error) bool {
	_, ok := errors.AsType[*InvalidNameError](err)
	return ok
}

// AlreadyExistsError refuses a role name another role in the org already has, ignoring case.
type AlreadyExistsError struct{ Name string }

func (e *AlreadyExistsError) Error() string {
	return fmt.Sprintf("role: already exists: name=%q", e.Name)
}

// ToAppError maps AlreadyExistsError to ROL008 AlreadyExists.
func (e *AlreadyExistsError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeRoleAlreadyExists, fmt.Sprintf("A role named %q already exists.", e.Name), codes.AlreadyExists,
		&apperrorv1.ErrorDetail{Code: apperror.CodeRoleAlreadyExists, Meta: map[string]string{"name": e.Name}})
}

// IsAlreadyExistsError reports whether err's tree contains a *AlreadyExistsError.
func IsAlreadyExistsError(err error) bool {
	_, ok := errors.AsType[*AlreadyExistsError](err)
	return ok
}

// StaleVersionError refuses a conditional write whose expected version no longer matches.
type StaleVersionError struct{ Want, Got int64 }

func (e *StaleVersionError) Error() string {
	return fmt.Sprintf("role: stale version, have %d want %d", e.Got, e.Want)
}

// ToAppError maps StaleVersionError to ROL009 FailedPrecondition.
func (e *StaleVersionError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeRoleStaleVersion, "The role changed since you opened it. Reload and try again.", codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{Code: apperror.CodeRoleStaleVersion})
}

// IsStaleVersionError reports whether err's tree contains a *StaleVersionError.
func IsStaleVersionError(err error) bool {
	_, ok := errors.AsType[*StaleVersionError](err)
	return ok
}
