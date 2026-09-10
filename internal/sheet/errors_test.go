package sheet

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"altalune.id/opensheet/internal/apperror"
)

func TestDuplicateColumnError(t *testing.T) {
	e := &DuplicateColumnError{Tab: "Sheet1", Column: "deleted_at", Columns: []int{1, 2}}
	msg := e.Error()
	if !strings.Contains(msg, "Sheet1") || !strings.Contains(msg, "deleted_at") {
		t.Errorf("Error() = %q, want it to carry the tab and the column", msg)
	}
	assertAppError(t, e.ToAppError(), apperror.CodeSheetDuplicateColumn)

	if !IsDuplicateColumnError(e) {
		t.Error("IsDuplicateColumnError = false, want true")
	}
	if !IsDuplicateColumnError(fmt.Errorf("wrap: %w", e)) {
		t.Error("IsDuplicateColumnError did not unwrap")
	}
	if IsDuplicateColumnError(&NotFoundError{}) {
		t.Error("IsDuplicateColumnError matched the wrong type")
	}
	if IsDuplicateColumnError(nil) {
		t.Error("IsDuplicateColumnError matched nil")
	}
}

func TestDuplicateColumnError_MapsTo409(t *testing.T) {
	app := (&DuplicateColumnError{Tab: "Sheet1", Column: "id", Columns: []int{0, 3}}).ToAppError()
	if got := app.Code(); got != "SHT021" {
		t.Errorf("Code() = %q, want %q", got, "SHT021")
	}
	if got := app.HTTPStatus(); got != http.StatusConflict {
		t.Errorf("HTTPStatus() = %d, want %d", got, http.StatusConflict)
	}
}

func TestEmptyIDError(t *testing.T) {
	e := &EmptyIDError{Tab: "Sheet1", RowIndex: 3}
	msg := e.Error()
	if !strings.Contains(msg, "Sheet1") || !strings.Contains(msg, "3") {
		t.Errorf("Error() = %q, want it to carry the tab and the row index", msg)
	}
	assertAppError(t, e.ToAppError(), apperror.CodeSheetEmptyID)

	if !IsEmptyIDError(e) {
		t.Error("IsEmptyIDError = false, want true")
	}
	if !IsEmptyIDError(fmt.Errorf("wrap: %w", e)) {
		t.Error("IsEmptyIDError did not unwrap")
	}
	if IsEmptyIDError(&NotFoundError{}) {
		t.Error("IsEmptyIDError matched the wrong type")
	}
	if IsEmptyIDError(nil) {
		t.Error("IsEmptyIDError matched nil")
	}
}

func TestEmptyIDError_MapsTo409(t *testing.T) {
	app := (&EmptyIDError{Tab: "Sheet1", RowIndex: 3}).ToAppError()
	if got := app.Code(); got != "SHT022" {
		t.Errorf("Code() = %q, want %q", got, "SHT022")
	}
	if got := app.HTTPStatus(); got != http.StatusConflict {
		t.Errorf("HTTPStatus() = %d, want %d", got, http.StatusConflict)
	}
}

func TestContractViolationError(t *testing.T) {
	e := &ContractViolationError{Slug: "prices", Reason: "no id column"}
	msg := e.Error()
	if !strings.Contains(msg, "prices") || !strings.Contains(msg, "no id column") {
		t.Errorf("Error() = %q, want it to carry the slug and the reason", msg)
	}
	assertAppError(t, e.ToAppError(), apperror.CodeSheetContractViolation)

	bare := &ContractViolationError{}
	if !strings.HasPrefix(bare.Error(), "sheet: ") {
		t.Errorf("Error() = %q, want the %q prefix", bare.Error(), "sheet: ")
	}
	assertAppError(t, bare.ToAppError(), apperror.CodeSheetContractViolation)

	if !IsContractViolationError(e) {
		t.Error("IsContractViolationError = false, want true")
	}
	if !IsContractViolationError(fmt.Errorf("wrap: %w", e)) {
		t.Error("IsContractViolationError did not unwrap")
	}
	if IsContractViolationError(errors.New("plain")) {
		t.Error("IsContractViolationError matched a plain error")
	}
	if IsContractViolationError(nil) {
		t.Error("IsContractViolationError matched nil")
	}
}

func TestContractViolationError_MapsTo409(t *testing.T) {
	app := (&ContractViolationError{Slug: "prices", Reason: "no id column"}).ToAppError()
	if got := app.Code(); got != "SHT023" {
		t.Errorf("Code() = %q, want %q", got, "SHT023")
	}
	if got := app.HTTPStatus(); got != http.StatusConflict {
		t.Errorf("HTTPStatus() = %d, want %d", got, http.StatusConflict)
	}
}

func TestIDMismatchError(t *testing.T) {
	e := &IDMismatchError{PathID: "row-1", BodyID: "row-2"}
	msg := e.Error()
	if !strings.Contains(msg, "row-1") || !strings.Contains(msg, "row-2") {
		t.Errorf("Error() = %q, want it to carry both ids", msg)
	}
	assertAppError(t, e.ToAppError(), apperror.CodeSheetIDMismatch)

	if !IsIDMismatchError(e) {
		t.Error("IsIDMismatchError = false, want true")
	}
	if !IsIDMismatchError(fmt.Errorf("wrap: %w", e)) {
		t.Error("IsIDMismatchError did not unwrap")
	}
	if IsIDMismatchError(errors.New("plain")) {
		t.Error("IsIDMismatchError matched a plain error")
	}
	if IsIDMismatchError(nil) {
		t.Error("IsIDMismatchError matched nil")
	}
}

func TestBatchTooLargeError(t *testing.T) {
	e := &BatchTooLargeError{Rows: 501, Limit: 500}
	msg := e.Error()
	if !strings.Contains(msg, "501") || !strings.Contains(msg, "500") {
		t.Errorf("Error() = %q, want it to carry the count and the limit", msg)
	}
	assertAppError(t, e.ToAppError(), apperror.CodeSheetBatchTooLarge)

	if !IsBatchTooLargeError(e) {
		t.Error("IsBatchTooLargeError = false, want true")
	}
	if !IsBatchTooLargeError(fmt.Errorf("wrap: %w", e)) {
		t.Error("IsBatchTooLargeError did not unwrap")
	}
	if IsBatchTooLargeError(errors.New("plain")) {
		t.Error("IsBatchTooLargeError matched a plain error")
	}
	if IsBatchTooLargeError(nil) {
		t.Error("IsBatchTooLargeError matched nil")
	}
}

func TestBatchTooLargeError_MapsTo422AndStatesTheLimit(t *testing.T) {
	app := (&BatchTooLargeError{Rows: 501, Limit: 500}).ToAppError()
	if got := app.Code(); got != "SHT028" {
		t.Errorf("Code() = %q, want %q", got, "SHT028")
	}
	if got := app.HTTPStatus(); got != http.StatusUnprocessableEntity {
		t.Errorf("HTTPStatus() = %d, want %d", got, http.StatusUnprocessableEntity)
	}
	if got := app.Message(); !strings.Contains(got, "500") {
		t.Errorf("Message() = %q, want it to state the row limit", got)
	}
}

func TestIDMismatchError_MapsTo422(t *testing.T) {
	app := (&IDMismatchError{PathID: "row-1", BodyID: "row-2"}).ToAppError()
	if got := app.Code(); got != "SHT027" {
		t.Errorf("Code() = %q, want %q", got, "SHT027")
	}
	if got := app.HTTPStatus(); got != http.StatusUnprocessableEntity {
		t.Errorf("HTTPStatus() = %d, want %d", got, http.StatusUnprocessableEntity)
	}
}

func TestSoftDeleteUnsupportedError(t *testing.T) {
	e := &SoftDeleteUnsupportedError{Slug: "prices", Tab: "Rates"}
	msg := e.Error()
	if !strings.Contains(msg, "prices") || !strings.Contains(msg, "Rates") {
		t.Errorf("Error() = %q, want it to carry the slug and the tab", msg)
	}
	assertAppError(t, e.ToAppError(), apperror.CodeSheetSoftDeleteUnsupported)

	if !IsSoftDeleteUnsupportedError(e) {
		t.Error("IsSoftDeleteUnsupportedError = false, want true")
	}
	if !IsSoftDeleteUnsupportedError(fmt.Errorf("wrap: %w", e)) {
		t.Error("IsSoftDeleteUnsupportedError did not unwrap")
	}
	if IsSoftDeleteUnsupportedError(errors.New("plain")) {
		t.Error("IsSoftDeleteUnsupportedError matched a plain error")
	}
	if IsSoftDeleteUnsupportedError(nil) {
		t.Error("IsSoftDeleteUnsupportedError matched nil")
	}
}

func TestSoftDeleteUnsupportedError_MapsTo422AndNamesTheRemedy(t *testing.T) {
	app := (&SoftDeleteUnsupportedError{Slug: "prices", Tab: "Rates"}).ToAppError()
	if got := app.Code(); got != "SHT029" {
		t.Errorf("Code() = %q, want %q", got, "SHT029")
	}
	if got := app.HTTPStatus(); got != http.StatusUnprocessableEntity {
		t.Errorf("HTTPStatus() = %d, want %d", got, http.StatusUnprocessableEntity)
	}
	if got := app.Message(); !strings.Contains(got, "deleted_at") {
		t.Errorf("Message() = %q, want it to name the column a client must add", got)
	}
}
