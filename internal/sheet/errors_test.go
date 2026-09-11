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

func TestInvalidClauseError(t *testing.T) {
	e := &InvalidClauseError{Clause: "note:nope:x", Reason: `unknown operator "nope"`}
	msg := e.Error()
	if !strings.Contains(msg, "note:nope:x") || !strings.Contains(msg, "nope") {
		t.Errorf("Error() = %q, want it to carry the clause and the reason", msg)
	}
	assertAppError(t, e.ToAppError(), apperror.CodeSheetInvalidClause)
	if !IsInvalidClauseError(fmt.Errorf("wrapped: %w", e)) {
		t.Error("IsInvalidClauseError does not see through a wrap")
	}
	if got := (&InvalidClauseError{}).Error(); !strings.Contains(got, "invalid") {
		t.Errorf("Error() with no reason = %q, want a fallback reason", got)
	}
}

func TestUnqueryableColumnError(t *testing.T) {
	e := &UnqueryableColumnError{Column: "deleted_at", Tab: "Sheet1"}
	msg := e.Error()
	if !strings.Contains(msg, "deleted_at") || !strings.Contains(msg, "Sheet1") {
		t.Errorf("Error() = %q, want it to carry the column and the tab", msg)
	}
	assertAppError(t, e.ToAppError(), apperror.CodeSheetUnqueryableColumn)
	if !IsUnqueryableColumnError(fmt.Errorf("wrapped: %w", e)) {
		t.Error("IsUnqueryableColumnError does not see through a wrap")
	}
}

func TestInvalidLimitError(t *testing.T) {
	e := &InvalidLimitError{Limit: "5000", MaxRows: 1000}
	msg := e.Error()
	if !strings.Contains(msg, "5000") || !strings.Contains(msg, "1000") {
		t.Errorf("Error() = %q, want it to carry the limit and the cap", msg)
	}
	assertAppError(t, e.ToAppError(), apperror.CodeSheetInvalidLimit)
	if !IsInvalidLimitError(fmt.Errorf("wrapped: %w", e)) {
		t.Error("IsInvalidLimitError does not see through a wrap")
	}
}

func TestInvalidCursorError(t *testing.T) {
	e := &InvalidCursorError{Reason: "unsupported version 2"}
	if msg := e.Error(); !strings.Contains(msg, "unsupported version 2") {
		t.Errorf("Error() = %q, want it to carry the reason", msg)
	}
	assertAppError(t, e.ToAppError(), apperror.CodeSheetInvalidCursor)
	if !IsInvalidCursorError(fmt.Errorf("wrapped: %w", e)) {
		t.Error("IsInvalidCursorError does not see through a wrap")
	}
	if got := (&InvalidCursorError{}).Error(); !strings.Contains(got, "invalid") {
		t.Errorf("Error() with no reason = %q, want a fallback reason", got)
	}
}

func TestNewFilterErrorsMapToHTTP400(t *testing.T) {
	for _, e := range []interface{ ToAppError() *apperror.AppError }{
		&InvalidClauseError{Clause: "x", Reason: "y"},
		&UnqueryableColumnError{Column: "deleted_at", Tab: "Sheet1"},
		&InvalidLimitError{Limit: "0", MaxRows: 1000},
		&InvalidCursorError{Reason: "not base64"},
	} {
		ae := e.ToAppError()
		if got := ae.HTTPStatus(); got != http.StatusBadRequest {
			t.Errorf("%T HTTPStatus = %d, want %d", e, got, http.StatusBadRequest)
		}
	}
}

func TestStaleCursorError(t *testing.T) {
	e := &StaleCursorError{Slug: "prices"}
	if msg := e.Error(); !strings.Contains(msg, "prices") {
		t.Errorf("Error() = %q, want it to carry the slug", msg)
	}
	assertAppError(t, e.ToAppError(), apperror.CodeSheetStaleCursor)
	if !IsStaleCursorError(fmt.Errorf("wrapped: %w", e)) {
		t.Error("IsStaleCursorError does not see through a wrap")
	}
	if IsStaleCursorError(&InvalidCursorError{}) {
		t.Error("IsStaleCursorError matched the wrong type")
	}
	if got := e.ToAppError().HTTPStatus(); got != http.StatusConflict {
		t.Errorf("HTTPStatus = %d, want %d — the walk must restart, not be retried", got, http.StatusConflict)
	}
}

func TestHintNotApplicableError(t *testing.T) {
	e := &HintNotApplicableError{Column: "qty", Op: RowOpContains, Hint: RowHintNum}
	msg := e.Error()
	if !strings.Contains(msg, "qty") || !strings.Contains(msg, "contains") || !strings.Contains(msg, "num") {
		t.Errorf("Error() = %q, want it to carry the column, the operator and the hint", msg)
	}
	assertAppError(t, e.ToAppError(), apperror.CodeSheetHintNotApplicable)
	if !IsHintNotApplicableError(fmt.Errorf("wrapped: %w", e)) {
		t.Error("IsHintNotApplicableError does not see through a wrap")
	}
	if IsHintNotApplicableError(&HintOperandError{}) {
		t.Error("IsHintNotApplicableError matched the wrong type")
	}
}

func TestHintOperandError(t *testing.T) {
	e := &HintOperandError{Column: "qty", Hint: RowHintNum, Value: "abc"}
	msg := e.Error()
	if !strings.Contains(msg, "qty") || !strings.Contains(msg, "abc") || !strings.Contains(msg, "num") {
		t.Errorf("Error() = %q, want it to carry the column, the value and the hint", msg)
	}
	assertAppError(t, e.ToAppError(), apperror.CodeSheetHintOperand)
	if !IsHintOperandError(fmt.Errorf("wrapped: %w", e)) {
		t.Error("IsHintOperandError does not see through a wrap")
	}
	if IsHintOperandError(&HintNotApplicableError{}) {
		t.Error("IsHintOperandError matched the wrong type")
	}
}

// The data-plane envelope is {code, message} and the message is all a client
// reads, so the two hint refusals must be distinguishable from the message alone.
func TestHintErrors_MessagesTellTheTwoRefusalsApart(t *testing.T) {
	notApplicable := (&HintNotApplicableError{Column: "qty", Op: RowOpContains, Hint: RowHintNum}).
		ToAppError().Error()
	badOperand := (&HintOperandError{Column: "qty", Hint: RowHintNum, Value: "abc"}).
		ToAppError().Error()
	if notApplicable == badOperand {
		t.Fatalf("both refusals render %q", notApplicable)
	}
	if !strings.Contains(notApplicable, "contains") {
		t.Errorf("SHT037 message = %q, want the operator that cannot be hinted", notApplicable)
	}
	if !strings.Contains(badOperand, "abc") {
		t.Errorf("SHT038 message = %q, want the operand the grammar refused", badOperand)
	}
	for _, msg := range []string{notApplicable, badOperand} {
		if !strings.Contains(msg, "qty") || !strings.Contains(msg, "num") {
			t.Errorf("message = %q, want the column and the hint", msg)
		}
	}
}

func TestHintErrorsMapToHTTP400(t *testing.T) {
	for _, e := range []interface{ ToAppError() *apperror.AppError }{
		&HintNotApplicableError{Column: "qty", Op: RowOpIn, Hint: RowHintDate},
		&HintOperandError{Column: "qty", Hint: RowHintDate, Value: "01/02/2026"},
	} {
		if got := e.ToAppError().HTTPStatus(); got != http.StatusBadRequest {
			t.Errorf("%T HTTPStatus = %d, want %d", e, got, http.StatusBadRequest)
		}
	}
}

func TestInvalidSortError(t *testing.T) {
	e := &InvalidSortError{Sort: "qty:up", Reason: `unknown direction "up"`}
	msg := e.Error()
	if !strings.Contains(msg, "qty:up") || !strings.Contains(msg, "up") {
		t.Errorf("Error() = %q, want it to carry the sort and the reason", msg)
	}
	assertAppError(t, e.ToAppError(), apperror.CodeSheetInvalidSort)
	if !IsInvalidSortError(fmt.Errorf("wrapped: %w", e)) {
		t.Error("IsInvalidSortError does not see through a wrap")
	}
	if IsInvalidSortError(&UnknownSortColumnError{}) {
		t.Error("IsInvalidSortError matched the wrong type")
	}
	if got := (&InvalidSortError{}).Error(); !strings.Contains(got, "invalid") {
		t.Errorf("Error() with no reason = %q, want a fallback reason", got)
	}
	if got := (&InvalidSortError{}).ToAppError().Error(); !strings.Contains(got, "column:asc") {
		t.Errorf("message with no reason = %q, want the accepted form", got)
	}
}

func TestSortDepthError(t *testing.T) {
	e := &SortDepthError{Pages: 20}
	if msg := e.Error(); !strings.Contains(msg, "20") {
		t.Errorf("Error() = %q, want it to carry the cap", msg)
	}
	assertAppError(t, e.ToAppError(), apperror.CodeSheetSortDepth)
	if got := e.ToAppError().Message(); !strings.Contains(got, "20") {
		t.Errorf("message = %q, want it to name the cap", got)
	}
	if got := e.ToAppError().HTTPStatus(); got != http.StatusBadRequest {
		t.Errorf("HTTPStatus = %d, want %d", got, http.StatusBadRequest)
	}
	if !IsSortDepthError(fmt.Errorf("wrapped: %w", e)) {
		t.Error("IsSortDepthError does not see through a wrap")
	}
	if IsSortDepthError(&InvalidSortError{}) {
		t.Error("IsSortDepthError matched the wrong type")
	}
}

// SHT034 covers both cursor/sortedness directions, and the data-plane envelope
// carries no meta, so the message is the only thing that can tell them apart.
func TestInvalidCursorError_MessageTellsTheTwoPairingDirectionsApart(t *testing.T) {
	onSorted := (&InvalidCursorError{Reason: reasonUnsortedCursorOnSortedRead}).ToAppError().Message()
	onUnsorted := (&InvalidCursorError{Reason: reasonSortedCursorOnUnsortedRead}).ToAppError().Message()
	if onSorted == onUnsorted {
		t.Fatalf("both directions render %q", onSorted)
	}
	if got := (&InvalidCursorError{}).ToAppError().Message(); !strings.Contains(got, "start the walk again") {
		t.Errorf("message with no reason = %q, want the restart advice", got)
	}
}

func TestUnknownSortColumnError(t *testing.T) {
	e := &UnknownSortColumnError{Column: "qty", Tab: "Sheet1"}
	msg := e.Error()
	if !strings.Contains(msg, "qty") || !strings.Contains(msg, "Sheet1") {
		t.Errorf("Error() = %q, want it to carry the column and the tab", msg)
	}
	assertAppError(t, e.ToAppError(), apperror.CodeSheetUnknownSortColumn)
	if !IsUnknownSortColumnError(fmt.Errorf("wrapped: %w", e)) {
		t.Error("IsUnknownSortColumnError does not see through a wrap")
	}
	if IsUnknownSortColumnError(&UnknownColumnError{}) {
		t.Error("IsUnknownSortColumnError matched the wrong type")
	}
}

// SHT041 exists rather than reusing SHT014 because the data-plane envelope is
// {code, message} with a static message, so only a distinct code tells a client
// sending ?where=a:eq:1&sort=b:asc which parameter is wrong.
func TestUnknownSortColumnError_IsDistinctFromUnknownColumnError(t *testing.T) {
	sortErr := (&UnknownSortColumnError{Column: "qty", Tab: "Sheet1"}).ToAppError()
	whereErr := (&UnknownColumnError{Column: "qty", Tab: "Sheet1"}).ToAppError()
	if sortErr.Code() == whereErr.Code() {
		t.Fatalf("both refusals carry the code %q", sortErr.Code())
	}
	if sortErr.Error() == whereErr.Error() {
		t.Errorf("both refusals render %q", sortErr.Error())
	}
}

// SHT039 collapses three conditions, so its message must say which one fired —
// the envelope carries no meta, and Reason-style detail never reaches a client.
func TestInvalidSortError_MessageTellsTheThreeConditionsApart(t *testing.T) {
	messages := map[string]string{}
	for name, raw := range map[string][]string{
		"repeated":          {"qty:asc", "name:desc"},
		"malformed":         {"qty"},
		"unknown direction": {"qty:up"},
	} {
		_, err := ParseRowSort(raw)
		if !IsInvalidSortError(err) {
			t.Fatalf("%s: error = %v, want InvalidSortError", name, err)
		}
		var appErr interface{ ToAppError() *apperror.AppError }
		if !errors.As(err, &appErr) {
			t.Fatalf("%s: %v carries no AppError", name, err)
		}
		messages[name] = appErr.ToAppError().Error()
	}
	if !strings.Contains(messages["repeated"], "given 2 times") {
		t.Errorf("repeated message = %q, want it to name the repeat", messages["repeated"])
	}
	if !strings.Contains(messages["malformed"], "no direction") {
		t.Errorf("malformed message = %q, want it to name the missing direction", messages["malformed"])
	}
	if !strings.Contains(messages["unknown direction"], `unknown direction "up"`) {
		t.Errorf("direction message = %q, want the direction it refused", messages["unknown direction"])
	}
	seen := map[string]string{}
	for name, msg := range messages {
		if prev, dup := seen[msg]; dup {
			t.Errorf("%s and %s render the identical message %q", prev, name, msg)
		}
		seen[msg] = name
	}
}

func TestSortErrorsMapToHTTP400(t *testing.T) {
	for _, e := range []interface{ ToAppError() *apperror.AppError }{
		&InvalidSortError{Sort: "qty", Reason: "no direction"},
		&UnknownSortColumnError{Column: "qty", Tab: "Sheet1"},
	} {
		if got := e.ToAppError().HTTPStatus(); got != http.StatusBadRequest {
			t.Errorf("%T HTTPStatus = %d, want %d", e, got, http.StatusBadRequest)
		}
	}
}
