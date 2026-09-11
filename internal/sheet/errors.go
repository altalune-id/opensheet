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

// PayloadTooLargeError signals a serialized grid larger than the configured read limit.
type PayloadTooLargeError struct {
	Bytes    int64
	MaxBytes int64
}

func (e *PayloadTooLargeError) Error() string {
	return fmt.Sprintf("sheet: payload: %d bytes over the %d byte limit", e.Bytes, e.MaxBytes)
}

// ToAppError maps PayloadTooLargeError to the canonical payload-too-large envelope.
func (e *PayloadTooLargeError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetPayloadTooLarge,
		"Sheet payload is too large to serve",
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

// IsPayloadTooLargeError reports whether err's tree contains a *PayloadTooLargeError.
func IsPayloadTooLargeError(err error) bool {
	_, ok := errors.AsType[*PayloadTooLargeError](err)
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

// NotWritableError signals a write against a sheet that was never opted in to writes.
type NotWritableError struct {
	SheetID string
	Slug    string
}

func (e *NotWritableError) Error() string {
	if e.Slug == "" {
		return "sheet: not writable: id=" + e.SheetID
	}
	return fmt.Sprintf("sheet: not writable: %q", e.Slug)
}

// ToAppError maps NotWritableError to a PermissionDenied envelope.
func (e *NotWritableError) ToAppError() *apperror.AppError {
	meta := map[string]string{}
	if e.SheetID != "" {
		meta["sheet_id"] = e.SheetID
	}
	if e.Slug != "" {
		meta["slug"] = e.Slug
	}
	return apperror.New(
		apperror.CodeSheetNotWritable,
		"This sheet is not writable",
		codes.PermissionDenied,
		&apperrorv1.ErrorDetail{Code: apperror.CodeSheetNotWritable, Meta: meta},
	)
}

// IsNotWritableError reports whether err's tree contains a *NotWritableError.
func IsNotWritableError(err error) bool {
	_, ok := errors.AsType[*NotWritableError](err)
	return ok
}

// NoIDColumnError signals a tab with no id header, so its rows cannot be addressed.
type NoIDColumnError struct {
	Tab string
}

func (e *NoIDColumnError) Error() string {
	return fmt.Sprintf("sheet: tab %q: no id column", e.Tab)
}

// ToAppError maps NoIDColumnError to an Aborted envelope.
func (e *NoIDColumnError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetNoIDColumn,
		"This tab has no id column, so its rows cannot be addressed",
		codes.Aborted,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetNoIDColumn,
			Meta: map[string]string{"tab": e.Tab},
		},
	)
}

// IsNoIDColumnError reports whether err's tree contains a *NoIDColumnError.
func IsNoIDColumnError(err error) bool {
	_, ok := errors.AsType[*NoIDColumnError](err)
	return ok
}

// AmbiguousIDColumnError signals more than one header naming the id column.
type AmbiguousIDColumnError struct {
	Tab     string
	Columns []int
}

func (e *AmbiguousIDColumnError) Error() string {
	return fmt.Sprintf("sheet: tab %q: %d headers name the id column", e.Tab, len(e.Columns))
}

// ToAppError maps AmbiguousIDColumnError to an Aborted envelope.
func (e *AmbiguousIDColumnError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetAmbiguousIDColumn,
		"This tab has more than one id column; rename all but one",
		codes.Aborted,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetAmbiguousIDColumn,
			Meta: map[string]string{"tab": e.Tab, "columns": strconv.Itoa(len(e.Columns))},
		},
	)
}

// IsAmbiguousIDColumnError reports whether err's tree contains an *AmbiguousIDColumnError.
func IsAmbiguousIDColumnError(err error) bool {
	_, ok := errors.AsType[*AmbiguousIDColumnError](err)
	return ok
}

// DuplicateIDError signals more than one row carrying the addressed id.
type DuplicateIDError struct {
	ID    string
	Count int
}

func (e *DuplicateIDError) Error() string {
	return fmt.Sprintf("sheet: id %q: matches %d rows", e.ID, e.Count)
}

// ToAppError maps DuplicateIDError to an Aborted envelope.
func (e *DuplicateIDError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetDuplicateID,
		"More than one row carries this id",
		codes.Aborted,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetDuplicateID,
			Meta: map[string]string{"id": e.ID, "rows": strconv.Itoa(e.Count)},
		},
	)
}

// IsDuplicateIDError reports whether err's tree contains a *DuplicateIDError.
func IsDuplicateIDError(err error) bool {
	_, ok := errors.AsType[*DuplicateIDError](err)
	return ok
}

// RowNotFoundError signals that no row carries the addressed id.
type RowNotFoundError struct {
	ID string
}

func (e *RowNotFoundError) Error() string {
	return fmt.Sprintf("sheet: row: not found: id=%q", e.ID)
}

// ToAppError maps RowNotFoundError to the canonical NotFound envelope.
func (e *RowNotFoundError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetRowNotFound,
		"Row not found",
		codes.NotFound,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetRowNotFound,
			Meta: map[string]string{"id": e.ID},
		},
	)
}

// IsRowNotFoundError reports whether err's tree contains a *RowNotFoundError.
func IsRowNotFoundError(err error) bool {
	_, ok := errors.AsType[*RowNotFoundError](err)
	return ok
}

// UnknownColumnError signals a named column absent from the tab's header row.
type UnknownColumnError struct {
	Column string
	Tab    string
}

func (e *UnknownColumnError) Error() string {
	return fmt.Sprintf("sheet: tab %q: unknown column %q", e.Tab, e.Column)
}

// ToAppError maps UnknownColumnError to the canonical InvalidArgument envelope.
func (e *UnknownColumnError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetUnknownColumn,
		"This tab has no such column",
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetUnknownColumn,
			Meta: map[string]string{"column": e.Column, "tab": e.Tab},
		},
	)
}

// IsUnknownColumnError reports whether err's tree contains an *UnknownColumnError.
func IsUnknownColumnError(err error) bool {
	_, ok := errors.AsType[*UnknownColumnError](err)
	return ok
}

// ReadOnlyColumnError signals a write to a column the data plane keeps immutable.
type ReadOnlyColumnError struct {
	Column string
}

func (e *ReadOnlyColumnError) Error() string {
	return fmt.Sprintf("sheet: column %q: read-only", e.Column)
}

// ToAppError maps ReadOnlyColumnError to the canonical InvalidArgument envelope.
func (e *ReadOnlyColumnError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetReadOnlyColumn,
		"This column is read-only",
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetReadOnlyColumn,
			Meta: map[string]string{"column": e.Column},
		},
	)
}

// IsReadOnlyColumnError reports whether err's tree contains a *ReadOnlyColumnError.
func IsReadOnlyColumnError(err error) bool {
	_, ok := errors.AsType[*ReadOnlyColumnError](err)
	return ok
}

// InvalidRowError signals a row payload that cannot be written as given.
type InvalidRowError struct {
	Reason string
}

func (e *InvalidRowError) Error() string {
	reason := e.Reason
	if reason == "" {
		reason = "invalid"
	}
	return "sheet: row: " + reason
}

// ToAppError maps InvalidRowError to the canonical InvalidArgument envelope.
func (e *InvalidRowError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetInvalidRow,
		"Invalid row payload",
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetInvalidRow,
			Meta: map[string]string{"reason": e.Reason},
		},
	)
}

// IsInvalidRowError reports whether err's tree contains an *InvalidRowError.
func IsInvalidRowError(err error) bool {
	_, ok := errors.AsType[*InvalidRowError](err)
	return ok
}

// WriteInFlightError signals an earlier attempt under the same idempotency key that has not reported an outcome.
type WriteInFlightError struct {
	Key string
}

func (e *WriteInFlightError) Error() string {
	return fmt.Sprintf("sheet: idempotency key %q: an earlier attempt is still in flight", e.Key)
}

// ToAppError maps WriteInFlightError to an Aborted envelope.
func (e *WriteInFlightError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetWriteInFlight,
		"An earlier write under this idempotency key has not reported an outcome yet; retry shortly",
		codes.Aborted,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetWriteInFlight,
			Meta: map[string]string{"idempotency_key": e.Key},
		},
	)
}

// IsWriteInFlightError reports whether err's tree contains a *WriteInFlightError.
func IsWriteInFlightError(err error) bool {
	_, ok := errors.AsType[*WriteInFlightError](err)
	return ok
}

// IdempotencyMismatchError signals an idempotency key reused under a different request body.
type IdempotencyMismatchError struct {
	Key string
}

func (e *IdempotencyMismatchError) Error() string {
	return fmt.Sprintf("sheet: idempotency key %q: reused under a different body", e.Key)
}

// ToAppError maps IdempotencyMismatchError to the envelope the status table renders as 422.
func (e *IdempotencyMismatchError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetIdempotencyMismatch,
		"This idempotency key was already used for a different request body",
		codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetIdempotencyMismatch,
			Meta: map[string]string{"idempotency_key": e.Key},
		},
	)
}

// IsIdempotencyMismatchError reports whether err's tree contains an *IdempotencyMismatchError.
func IsIdempotencyMismatchError(err error) bool {
	_, ok := errors.AsType[*IdempotencyMismatchError](err)
	return ok
}

// DuplicateColumnError signals more than one header folding to the same reserved column name.
type DuplicateColumnError struct {
	Tab     string
	Column  string
	Columns []int
}

func (e *DuplicateColumnError) Error() string {
	return fmt.Sprintf("sheet: tab %q: %d headers name the %q column", e.Tab, len(e.Columns), e.Column)
}

// ToAppError maps DuplicateColumnError to an Aborted envelope.
func (e *DuplicateColumnError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetDuplicateColumn,
		"This tab has more than one column with this name; rename all but one",
		codes.Aborted,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetDuplicateColumn,
			Meta: map[string]string{"tab": e.Tab, "column": e.Column, "columns": strconv.Itoa(len(e.Columns))},
		},
	)
}

// IsDuplicateColumnError reports whether err's tree contains a *DuplicateColumnError.
func IsDuplicateColumnError(err error) bool {
	_, ok := errors.AsType[*DuplicateColumnError](err)
	return ok
}

// EmptyIDError signals a row carrying content under an empty id.
type EmptyIDError struct {
	Tab      string
	RowIndex int
}

func (e *EmptyIDError) Error() string {
	return fmt.Sprintf("sheet: tab %q: row %d: empty id", e.Tab, e.RowIndex)
}

// ToAppError maps EmptyIDError to an Aborted envelope.
func (e *EmptyIDError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetEmptyID,
		"A row with content carries an empty id; every row needs one",
		codes.Aborted,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetEmptyID,
			Meta: map[string]string{"tab": e.Tab, "row_index": strconv.Itoa(e.RowIndex)},
		},
	)
}

// IsEmptyIDError reports whether err's tree contains an *EmptyIDError.
func IsEmptyIDError(err error) bool {
	_, ok := errors.AsType[*EmptyIDError](err)
	return ok
}

// ContractViolationError signals an id-addressed operation on a sheet that does not satisfy the table contract.
// NOTE: unused until the keyed-write path; landed with its siblings so the block and its docs stay in one piece.
type ContractViolationError struct {
	Slug   string
	Reason string
}

func (e *ContractViolationError) Error() string {
	reason := e.Reason
	if reason == "" {
		reason = "table contract not satisfied"
	}
	return fmt.Sprintf("sheet: %q: %s", e.Slug, reason)
}

// ToAppError maps ContractViolationError to an Aborted envelope.
func (e *ContractViolationError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetContractViolation,
		"This sheet does not satisfy the table contract, so its rows cannot be addressed by id",
		codes.Aborted,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetContractViolation,
			Meta: map[string]string{"slug": e.Slug, "reason": e.Reason},
		},
	)
}

// IsContractViolationError reports whether err's tree contains a *ContractViolationError.
func IsContractViolationError(err error) bool {
	_, ok := errors.AsType[*ContractViolationError](err)
	return ok
}

// NothingToFixError signals a tab whose id column already gives every row with content a value.
type NothingToFixError struct {
	Tab string
}

func (e *NothingToFixError) Error() string {
	return fmt.Sprintf("sheet: tab %q: nothing to fix", e.Tab)
}

// ToAppError maps NothingToFixError to an Aborted envelope.
func (e *NothingToFixError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetNothingToFix,
		"This tab already has an id column with a value in every row with content",
		codes.Aborted,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetNothingToFix,
			Meta: map[string]string{"tab": e.Tab},
		},
	)
}

// IsNothingToFixError reports whether err's tree contains a *NothingToFixError.
func IsNothingToFixError(err error) bool {
	_, ok := errors.AsType[*NothingToFixError](err)
	return ok
}

// ColumnNotEmptyError signals that the column an id header would claim already holds data below row 1.
type ColumnNotEmptyError struct {
	Tab    string
	Column string
	Row    int
}

func (e *ColumnNotEmptyError) Error() string {
	return fmt.Sprintf("sheet: tab %q: column %s holds data in row %d", e.Tab, e.Column, e.Row)
}

// ToAppError maps ColumnNotEmptyError to an Aborted envelope.
func (e *ColumnNotEmptyError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetColumnNotEmpty,
		"The column an id header would claim already holds data, so it will not be overwritten",
		codes.Aborted,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetColumnNotEmpty,
			Meta: map[string]string{"tab": e.Tab, "column": e.Column, "row": strconv.Itoa(e.Row)},
		},
	)
}

// IsColumnNotEmptyError reports whether err's tree contains a *ColumnNotEmptyError.
func IsColumnNotEmptyError(err error) bool {
	_, ok := errors.AsType[*ColumnNotEmptyError](err)
	return ok
}

// IDMismatchError signals a whole-row replace whose body names a different id than its path does.
type IDMismatchError struct {
	PathID string
	BodyID string
}

func (e *IDMismatchError) Error() string {
	return fmt.Sprintf("sheet: row: body id %q does not match path id %q", e.BodyID, e.PathID)
}

// ToAppError maps IDMismatchError to an InvalidArgument envelope its own status override lifts to 422.
func (e *IDMismatchError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetIDMismatch,
		"A row cannot be renamed, so the body's id must match the path's id or be omitted",
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetIDMismatch,
			Meta: map[string]string{"path_id": e.PathID, "body_id": e.BodyID},
		},
	)
}

// IsIDMismatchError reports whether err's tree contains an *IDMismatchError.
func IsIDMismatchError(err error) bool {
	_, ok := errors.AsType[*IDMismatchError](err)
	return ok
}

// BatchTooLargeError signals a batch carrying more rows than one request may write.
type BatchTooLargeError struct {
	Rows  int
	Limit int
}

func (e *BatchTooLargeError) Error() string {
	return fmt.Sprintf("sheet: batch: %d rows over the %d row limit", e.Rows, e.Limit)
}

// ToAppError maps BatchTooLargeError to an InvalidArgument envelope its own status override lifts to 422.
func (e *BatchTooLargeError) ToAppError() *apperror.AppError {
	limit := strconv.Itoa(e.Limit)
	return apperror.New(
		apperror.CodeSheetBatchTooLarge,
		"A batch may carry at most "+limit+" rows",
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetBatchTooLarge,
			Meta: map[string]string{"rows": strconv.Itoa(e.Rows), "limit": limit},
		},
	)
}

// IsBatchTooLargeError reports whether err's tree contains a *BatchTooLargeError.
func IsBatchTooLargeError(err error) bool {
	_, ok := errors.AsType[*BatchTooLargeError](err)
	return ok
}

// SoftDeleteUnsupportedError signals a delete on a tab whose header row carries no deleted_at column.
type SoftDeleteUnsupportedError struct {
	Slug string
	Tab  string
}

func (e *SoftDeleteUnsupportedError) Error() string {
	return fmt.Sprintf("sheet: %q: tab %q has no %s column", e.Slug, e.Tab, deletedAtColumn)
}

// ToAppError maps SoftDeleteUnsupportedError to an InvalidArgument envelope its own status override lifts to 422.
func (e *SoftDeleteUnsupportedError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetSoftDeleteUnsupported,
		"This tab cannot record a deletion: add a column headed "+deletedAtColumn+" to it, then delete the row again",
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetSoftDeleteUnsupported,
			Meta: map[string]string{"slug": e.Slug, "tab": e.Tab, "column": deletedAtColumn},
		},
	)
}

// IsSoftDeleteUnsupportedError reports whether err's tree contains a *SoftDeleteUnsupportedError.
func IsSoftDeleteUnsupportedError(err error) bool {
	_, ok := errors.AsType[*SoftDeleteUnsupportedError](err)
	return ok
}

// PreconditionFailedError signals a conditional write whose If-Match tag does not name the row as it stands.
type PreconditionFailedError struct {
	ID   string
	ETag string
}

func (e *PreconditionFailedError) Error() string {
	return fmt.Sprintf("sheet: row %q: the if-match tag does not match %q", e.ID, e.ETag)
}

// ToAppError maps PreconditionFailedError to a FailedPrecondition envelope its own status override lifts to 412.
func (e *PreconditionFailedError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetPreconditionFailed,
		"The row changed since you read it, so the write was refused: read it again and retry with the new tag",
		codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetPreconditionFailed,
			Meta: map[string]string{"row_id": e.ID, "etag": e.ETag},
		},
	)
}

// IsPreconditionFailedError reports whether err's tree contains a *PreconditionFailedError.
func IsPreconditionFailedError(err error) bool {
	_, ok := errors.AsType[*PreconditionFailedError](err)
	return ok
}

// InvalidClauseError signals a ?where= clause the filter grammar cannot read.
type InvalidClauseError struct {
	Clause string
	Reason string
}

func (e *InvalidClauseError) Error() string {
	reason := e.Reason
	if reason == "" {
		reason = "invalid"
	}
	return fmt.Sprintf("sheet: filter %q: %s", e.Clause, reason)
}

// ToAppError maps InvalidClauseError to the canonical InvalidArgument envelope.
func (e *InvalidClauseError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetInvalidClause,
		"This filter cannot be read: a clause is written column:operator:value",
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetInvalidClause,
			Meta: map[string]string{"clause": e.Clause, "reason": e.Reason},
		},
	)
}

// IsInvalidClauseError reports whether err's tree contains an *InvalidClauseError.
func IsInvalidClauseError(err error) bool {
	_, ok := errors.AsType[*InvalidClauseError](err)
	return ok
}

// UnqueryableColumnError signals a filter on a column the projection does not store.
type UnqueryableColumnError struct {
	Column string
	Tab    string
}

func (e *UnqueryableColumnError) Error() string {
	return fmt.Sprintf("sheet: tab %q: column %q cannot be filtered on", e.Tab, e.Column)
}

// ToAppError maps UnqueryableColumnError to the canonical InvalidArgument envelope.
func (e *UnqueryableColumnError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetUnqueryableColumn,
		"This column cannot be filtered on",
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetUnqueryableColumn,
			Meta: map[string]string{"column": e.Column, "tab": e.Tab},
		},
	)
}

// IsUnqueryableColumnError reports whether err's tree contains an *UnqueryableColumnError.
func IsUnqueryableColumnError(err error) bool {
	_, ok := errors.AsType[*UnqueryableColumnError](err)
	return ok
}

// InvalidLimitError signals a ?limit= that is not a whole number of rows within the configured cap.
type InvalidLimitError struct {
	Limit   string
	MaxRows int
}

func (e *InvalidLimitError) Error() string {
	return fmt.Sprintf("sheet: limit %q: not a whole number of rows from 1 to %d", e.Limit, e.MaxRows)
}

// ToAppError maps InvalidLimitError to the canonical InvalidArgument envelope.
func (e *InvalidLimitError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetInvalidLimit,
		fmt.Sprintf("Ask for a whole number of rows from 1 to %d", e.MaxRows),
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetInvalidLimit,
			Meta: map[string]string{
				"limit":    e.Limit,
				"max_rows": strconv.Itoa(e.MaxRows),
			},
		},
	)
}

// IsInvalidLimitError reports whether err's tree contains an *InvalidLimitError.
func IsInvalidLimitError(err error) bool {
	_, ok := errors.AsType[*InvalidLimitError](err)
	return ok
}

// InvalidCursorError signals a ?cursor= this version did not issue.
type InvalidCursorError struct {
	Reason string
}

func (e *InvalidCursorError) Error() string {
	reason := e.Reason
	if reason == "" {
		reason = "invalid"
	}
	return "sheet: cursor: " + reason
}

// ToAppError maps InvalidCursorError to the canonical InvalidArgument envelope.
func (e *InvalidCursorError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeSheetInvalidCursor,
		"This page cursor cannot be read: start the walk again without one",
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeSheetInvalidCursor,
			Meta: map[string]string{"reason": e.Reason},
		},
	)
}

// IsInvalidCursorError reports whether err's tree contains an *InvalidCursorError.
func IsInvalidCursorError(err error) bool {
	_, ok := errors.AsType[*InvalidCursorError](err)
	return ok
}
