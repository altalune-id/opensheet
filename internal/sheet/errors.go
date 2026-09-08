package sheet

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
)

// NotFoundError signals no sheet matched the lookup.
type NotFoundError struct {
	ID        string
	OrgID     string
	ProjectID string
	Slug      string
}

func (e *NotFoundError) Error() string {
	switch {
	case e.ID != "":
		return "sheet: not found: id=" + e.ID
	case e.Slug != "":
		return fmt.Sprintf("sheet: not found: project=%s slug=%q", e.ProjectID, e.Slug)
	default:
		return "sheet: not found: no identifier"
	}
}

// ToAppError maps NotFoundError to the canonical NotFound envelope.
func (e *NotFoundError) ToAppError() *apperror.AppError {
	meta := map[string]string{}
	if e.ID != "" {
		meta["sheet_id"] = e.ID
	}
	if e.OrgID != "" {
		meta["org_id"] = e.OrgID
	}
	if e.ProjectID != "" {
		meta["project_id"] = e.ProjectID
	}
	if e.Slug != "" {
		meta["slug"] = e.Slug
	}
	return apperror.New(
		apperror.CodeSheetNotFound,
		"Sheet not found",
		codes.NotFound,
		&apperrorv1.ErrorDetail{Code: apperror.CodeSheetNotFound, Meta: meta},
	)
}

// IsNotFoundError reports whether err's tree contains a *NotFoundError.
func IsNotFoundError(err error) bool {
	_, ok := errors.AsType[*NotFoundError](err)
	return ok
}

// AlreadyExistsError signals a uniqueness violation on (project_id, slug).
type AlreadyExistsError struct {
	Field string
	Value string
}

func (e *AlreadyExistsError) Error() string {
	if e.Field == "" {
		return "sheet: already exists: slug taken in this project"
	}
	return fmt.Sprintf("sheet: already exists: %s=%q taken in this project", e.Field, e.Value)
}

// ToAppError maps AlreadyExistsError to the canonical AlreadyExists envelope.
func (e *AlreadyExistsError) ToAppError() *apperror.AppError {
	meta := map[string]string{}
	if e.Field != "" {
		meta[e.Field] = e.Value
	}
	return apperror.New(
		apperror.CodeSheetAlreadyExists,
		"Sheet slug already taken in this project",
		codes.AlreadyExists,
		&apperrorv1.ErrorDetail{Code: apperror.CodeSheetAlreadyExists, Meta: meta},
	)
}

// IsAlreadyExistsError reports whether err's tree contains an *AlreadyExistsError.
func IsAlreadyExistsError(err error) bool {
	_, ok := errors.AsType[*AlreadyExistsError](err)
	return ok
}

// InvalidSlugError signals a slug that is malformed or reserved.
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
		return "sheet: slug: " + reason
	}
	return fmt.Sprintf("sheet: slug: %s: %q", reason, e.Slug)
}

// ToAppError maps InvalidSlugError to the canonical InvalidArgument envelope.
func (e *InvalidSlugError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetInvalidSlug,
		"Invalid sheet slug",
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetInvalidSlug,
			Meta: map[string]string{"slug": e.Slug, "reason": e.Reason},
		},
	)
}

// IsInvalidSlugError reports whether err's tree contains an *InvalidSlugError.
func IsInvalidSlugError(err error) bool {
	_, ok := errors.AsType[*InvalidSlugError](err)
	return ok
}

// InvalidVisibilityError signals a visibility outside the defined set.
type InvalidVisibilityError struct {
	Value string
}

func (e *InvalidVisibilityError) Error() string {
	return fmt.Sprintf("sheet: visibility: not one of key or public: %q", e.Value)
}

// ToAppError maps InvalidVisibilityError to the canonical InvalidArgument envelope.
func (e *InvalidVisibilityError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetInvalidVisibility,
		"Invalid sheet visibility",
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetInvalidVisibility,
			Meta: map[string]string{"visibility": e.Value},
		},
	)
}

// IsInvalidVisibilityError reports whether err's tree contains an *InvalidVisibilityError.
func IsInvalidVisibilityError(err error) bool {
	_, ok := errors.AsType[*InvalidVisibilityError](err)
	return ok
}

// InvalidTTLError signals a cache TTL outside zero or [1s, 24h].
type InvalidTTLError struct {
	TTL    time.Duration
	Reason string
}

func (e *InvalidTTLError) Error() string {
	reason := e.Reason
	if reason == "" {
		reason = "outside zero or [1s, 24h]"
	}
	return fmt.Sprintf("sheet: cache ttl: %s: %v", reason, e.TTL)
}

// ToAppError maps InvalidTTLError to the canonical InvalidArgument envelope.
func (e *InvalidTTLError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetInvalidTTL,
		"Invalid sheet cache TTL",
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetInvalidTTL,
			Meta: map[string]string{"cache_ttl": e.TTL.String(), "reason": e.Reason},
		},
	)
}

// IsInvalidTTLError reports whether err's tree contains an *InvalidTTLError.
func IsInvalidTTLError(err error) bool {
	_, ok := errors.AsType[*InvalidTTLError](err)
	return ok
}

// PublicDisabledError signals that public visibility was requested while the capability is off.
type PublicDisabledError struct {
	Slug string
}

func (e *PublicDisabledError) Error() string {
	if e.Slug == "" {
		return "sheet: public visibility: disabled by configuration"
	}
	return fmt.Sprintf("sheet: public visibility: disabled by configuration: %q", e.Slug)
}

// ToAppError maps PublicDisabledError to a FailedPrecondition envelope.
func (e *PublicDisabledError) ToAppError() *apperror.AppError {
	meta := map[string]string{}
	if e.Slug != "" {
		meta["slug"] = e.Slug
	}
	return apperror.New(
		apperror.CodeSheetPublicDisabled,
		"Public sheets are disabled on this deployment",
		codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{Code: apperror.CodeSheetPublicDisabled, Meta: meta},
	)
}

// IsPublicDisabledError reports whether err's tree contains a *PublicDisabledError.
func IsPublicDisabledError(err error) bool {
	_, ok := errors.AsType[*PublicDisabledError](err)
	return ok
}

// SnapshotTooLargeError signals a payload larger than the whole snapshot cache budget.
type SnapshotTooLargeError struct {
	Bytes    int64
	MaxBytes int64
}

func (e *SnapshotTooLargeError) Error() string {
	return fmt.Sprintf("sheet: snapshot payload: %d bytes over the %d byte cache budget", e.Bytes, e.MaxBytes)
}

// ToAppError maps SnapshotTooLargeError to the canonical payload-too-large envelope.
func (e *SnapshotTooLargeError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetPayloadTooLarge,
		"Sheet payload is too large to cache",
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetPayloadTooLarge,
			Meta: map[string]string{
				"bytes":     strconv.FormatInt(e.Bytes, 10),
				"max_bytes": strconv.FormatInt(e.MaxBytes, 10),
			},
		},
	)
}

// IsSnapshotTooLargeError reports whether err's tree contains a *SnapshotTooLargeError.
func IsSnapshotTooLargeError(err error) bool {
	_, ok := errors.AsType[*SnapshotTooLargeError](err)
	return ok
}

// CacheUnavailableError signals that the configured snapshot cache driver cannot be built.
type CacheUnavailableError struct {
	Driver string
	Reason string
}

func (e *CacheUnavailableError) Error() string {
	return fmt.Sprintf("sheet: snapshot cache driver %s: unavailable: %s", e.Driver, e.Reason)
}

// ToAppError maps CacheUnavailableError to the canonical internal-failure envelope.
func (e *CacheUnavailableError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeUnexpectedError,
		"Sheet cache is not available on this deployment",
		codes.Internal,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeUnexpectedError,
			Meta: map[string]string{"driver": e.Driver, "reason": e.Reason},
		},
	)
}

// IsCacheUnavailableError reports whether err's tree contains a *CacheUnavailableError.
func IsCacheUnavailableError(err error) bool {
	_, ok := errors.AsType[*CacheUnavailableError](err)
	return ok
}
