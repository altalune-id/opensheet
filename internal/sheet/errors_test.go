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
