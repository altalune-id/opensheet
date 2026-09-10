package handlers

import (
	"strconv"
	"strings"
	"testing"

	"altalune.id/opensheet/gworkspace"
	"altalune.id/opensheet/internal/i18n"
	"altalune.id/opensheet/internal/sheet"
)

const genericPublishFailure = "Could not publish that sheet."

type contractCase struct {
	name  string
	err   error
	wants []string
}

func contractCases() []contractCase {
	return []contractCase{
		{
			"no id column",
			&sheet.NoIDColumnError{Tab: "Q1"},
			[]string{"no id column", "headed id", "row 1", "unique value", "write scope"},
		},
		{
			"two id columns",
			&sheet.AmbiguousIDColumnError{Tab: "Q1", Columns: []int{0, 3}},
			[]string{"2 columns headed id", "Rename or remove all but one"},
		},
		{
			"two deleted_at columns",
			&sheet.DuplicateColumnError{Tab: "Q1", Column: "deleted_at", Columns: []int{2, 5}},
			[]string{"2 columns headed deleted_at", "Rename or remove all but one"},
		},
		{
			"content but empty id",
			&sheet.EmptyIDError{Tab: "Q1", RowIndex: 3},
			[]string{"Row 5", "no id", "Fill in its id"},
		},
		{
			"duplicate id",
			&sheet.DuplicateIDError{ID: "r1", Count: 2},
			[]string{"2 rows", "share the id r1", "unique id"},
		},
	}
}

func TestPublishMessage_NamesTheRemedyForEveryContractError(t *testing.T) {
	t.Parallel()
	for _, tc := range contractCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := publishMessage(nil, tc.err)
			if got == genericPublishFailure {
				t.Fatalf("publishMessage(%T) fell through to the generic copy", tc.err)
			}
			for _, want := range tc.wants {
				if !strings.Contains(got, want) {
					t.Fatalf("publishMessage(%T) = %q, want it to contain %q", tc.err, got, want)
				}
			}
		})
	}
}

func TestPublishMessage_EmptyIDNamesTheSpreadsheetRowNotTheIndex(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ index, want int }{{0, 2}, {3, 5}, {98, 100}} {
		got := publishMessage(nil, &sheet.EmptyIDError{Tab: "Q1", RowIndex: tc.index})
		if !strings.Contains(got, "Row "+strconv.Itoa(tc.want)) {
			t.Fatalf("publishMessage(RowIndex %d) = %q, want it to name spreadsheet row %d", tc.index, got, tc.want)
		}
	}
}

func TestPublishMessage_TranslatedCopyMatchesTheFallback(t *testing.T) {
	t.Parallel()
	tr := i18n.NewEmbeddedBundle(i18n.EnUS).For(i18n.EnUS)
	for _, tc := range contractCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, want := publishMessage(tr, tc.err), publishMessage(nil, tc.err)
			if got != want {
				t.Fatalf("publishMessage(en-US, %T) = %q, want the vetted copy %q", tc.err, got, want)
			}
		})
	}
}

func TestPublishErrorKeys_ResolveInEveryLocale(t *testing.T) {
	t.Parallel()
	args := []any{"Columns", 2, "Column", "deleted_at", "Row", 5, "Count", 2, "ID", "r1"}
	keys := []string{
		"sheets.publish_error.no_id_column",
		"sheets.publish_error.ambiguous_id_column",
		"sheets.publish_error.duplicate_column",
		"sheets.publish_error.empty_id",
		"sheets.publish_error.duplicate_id",
	}
	bundle := i18n.NewEmbeddedBundle(i18n.EnUS)
	for _, loc := range bundle.All() {
		tr := bundle.For(loc)
		for _, key := range keys {
			got := tr.T(key, args...)
			if got == key {
				t.Errorf("%s: %s is untranslated", loc, key)
				continue
			}
			if strings.Contains(got, "{{") || strings.Contains(got, "<no value>") {
				t.Errorf("%s: %s = %q, want its template data rendered", loc, key, got)
			}
		}
	}
}

const genericFixFailure = "Could not add an id column to this tab."

func fixCases() []contractCase {
	return []contractCase{
		{
			"read-only credential",
			&gworkspace.PermissionDeniedError{FileID: "F"},
			[]string{"refused", "headed id", "row 1", "unique value", "write scope"},
		},
		{
			"nothing to fix",
			&sheet.NothingToFixError{Tab: "Q1"},
			[]string{"already has an id column", "Publish again"},
		},
		{
			"target column holds data",
			&sheet.ColumnNotEmptyError{Tab: "Q1", Column: "C", Row: 4},
			[]string{"Column C", "row 4", "will not overwrite", "headed id"},
		},
	}
}

func TestFixMessage_NamesTheRemedyForEveryRefusal(t *testing.T) {
	t.Parallel()
	for _, tc := range fixCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := fixMessage(nil, tc.err)
			if got == genericFixFailure {
				t.Fatalf("fixMessage(%T) fell through to the generic copy", tc.err)
			}
			for _, want := range tc.wants {
				if !strings.Contains(got, want) {
					t.Fatalf("fixMessage(%T) = %q, want it to contain %q", tc.err, got, want)
				}
			}
		})
	}
}

func TestFixMessage_TranslatedCopyMatchesTheFallback(t *testing.T) {
	t.Parallel()
	tr := i18n.NewEmbeddedBundle(i18n.EnUS).For(i18n.EnUS)
	for _, tc := range fixCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got, want := fixMessage(tr, tc.err), fixMessage(nil, tc.err); got != want {
				t.Fatalf("fixMessage(en-US, %T) = %q, want the vetted copy %q", tc.err, got, want)
			}
		})
	}
}

func TestFixKeys_ResolveInEveryLocale(t *testing.T) {
	t.Parallel()
	args := []any{"Tab", "Q1", "Rows", 3, "Column", "C", "Row", 4}
	keys := []string{
		"sheets.fix_id_column",
		"sheets.fix_id_column_hint",
		"sheets.fix_id_column_done",
		"sheets.fix_error.denied",
		"sheets.fix_error.nothing_to_fix",
		"sheets.fix_error.column_not_empty",
		"sheets.fix_error.failed",
	}
	bundle := i18n.NewEmbeddedBundle(i18n.EnUS)
	for _, loc := range bundle.All() {
		tr := bundle.For(loc)
		for _, key := range keys {
			got := tr.T(key, args...)
			if got == key {
				t.Errorf("%s: %s is untranslated", loc, key)
				continue
			}
			if strings.Contains(got, "{{") || strings.Contains(got, "<no value>") {
				t.Errorf("%s: %s = %q, want its template data rendered", loc, key, got)
			}
		}
	}
}
