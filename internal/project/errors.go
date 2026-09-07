package project

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/template/gen/go/apperror/v1"
	"altalune.id/template/internal/apperror"
)

// NotFoundError signals no project matched the lookup.
type NotFoundError struct {
	ID    string
	OrgID string
	Slug  string
}

func (e *NotFoundError) Error() string {
	switch {
	case e.ID != "":
		return "project: not found: id=" + e.ID
	case e.Slug != "":
		return fmt.Sprintf("project: not found: org=%s slug=%q", e.OrgID, e.Slug)
	default:
		return "project: not found"
	}
}

// ToAppError maps NotFoundError to the canonical NotFound envelope.
func (e *NotFoundError) ToAppError() *apperror.AppError {
	meta := map[string]string{}
	if e.ID != "" {
		meta["project_id"] = e.ID
	}
	if e.Slug != "" {
		meta["slug"] = e.Slug
	}
	if e.OrgID != "" {
		meta["org_id"] = e.OrgID
	}
	return apperror.New(
		apperror.CodeProjectNotFound,
		"Project not found",
		codes.NotFound,
		&apperrorv1.ErrorDetail{Code: apperror.CodeProjectNotFound, Meta: meta},
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
		return "project: already exists"
	}
	return fmt.Sprintf("project: already exists: %s=%q", e.Field, e.Value)
}

// ToAppError maps AlreadyExistsError to the canonical AlreadyExists envelope.
func (e *AlreadyExistsError) ToAppError() *apperror.AppError {
	meta := map[string]string{}
	if e.Field != "" {
		meta[e.Field] = e.Value
	}
	return apperror.New(
		apperror.CodeProjectAlreadyExists,
		"Project already exists",
		codes.AlreadyExists,
		&apperrorv1.ErrorDetail{Code: apperror.CodeProjectAlreadyExists, Meta: meta},
	)
}

// IsAlreadyExistsError reports whether err's tree contains an *AlreadyExistsError.
func IsAlreadyExistsError(err error) bool {
	_, ok := errors.AsType[*AlreadyExistsError](err)
	return ok
}

// InvalidSlugError signals a slug violates the aggregate's format rules.
type InvalidSlugError struct {
	Slug   string
	Reason string
}

func (e *InvalidSlugError) Error() string {
	reason := e.Reason
	if reason == "" {
		reason = "invalid"
	}
	if e.Slug == "" {
		return "project: slug: " + reason
	}
	return fmt.Sprintf("project: slug: %s: %q", reason, e.Slug)
}

// ToAppError maps InvalidSlugError to the canonical InvalidArgument envelope.
func (e *InvalidSlugError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeProjectInvalidSlug,
		"Invalid project slug",
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeProjectInvalidSlug,
			Meta: map[string]string{"slug": e.Slug, "reason": e.Reason},
		},
	)
}

// IsInvalidSlugError reports whether err's tree contains an *InvalidSlugError.
func IsInvalidSlugError(err error) bool {
	_, ok := errors.AsType[*InvalidSlugError](err)
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
	return "project: name: " + reason
}

// ToAppError maps InvalidNameError to the canonical Validation envelope.
func (e *InvalidNameError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeValidation,
		"Invalid project name",
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeValidation,
			Meta: map[string]string{"field": "name", "reason": e.Reason},
		},
	)
}

// IsInvalidNameError reports whether err's tree contains an *InvalidNameError.
func IsInvalidNameError(err error) bool {
	_, ok := errors.AsType[*InvalidNameError](err)
	return ok
}

// SystemProtectedError signals a destructive op on a bootstrap-owned project.
type SystemProtectedError struct {
	Op        string
	ProjectID string
}

func (e *SystemProtectedError) Error() string {
	if e == nil {
		return "project: system-protected"
	}
	if e.Op == "" {
		return "project: system-protected"
	}
	return "project: system-protected: " + e.Op + " not allowed"
}

// ToAppError maps SystemProtectedError to a FailedPrecondition envelope.
func (e *SystemProtectedError) ToAppError() *apperror.AppError {
	meta := map[string]string{}
	if e.ProjectID != "" {
		meta["project_id"] = e.ProjectID
	}
	return apperror.New(
		apperror.CodeProjectSystemProtected,
		"System-protected project cannot be modified",
		codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{Code: apperror.CodeProjectSystemProtected, Meta: meta},
	)
}

// IsSystemProtectedError reports whether err's tree contains a *SystemProtectedError.
func IsSystemProtectedError(err error) bool {
	_, ok := errors.AsType[*SystemProtectedError](err)
	return ok
}
