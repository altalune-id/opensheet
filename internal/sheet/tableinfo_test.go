package sheet_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/gworkspace/gsheet"
	"altalune.id/opensheet/internal/sheet"
)

func liveRow(id string, index int, data gsheet.Row) sheet.ProjectedRow {
	return sheet.ProjectedRow{RowID: id, RowIndex: index, Data: data}
}

func TestReadWorkflow_TableInfo_AnswersFromTheProjectionWithoutCallingGoogle(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	validatedAt := time.Date(2026, 9, 10, 4, 11, 9, 0, time.UTC)
	sh.Writable = true
	sh.Generation = 42
	sh.ValidatedAt = &validatedAt
	sh.ContractOK = true
	h.rows.Seed(sheet.SnapshotKey{SheetID: sh.ID, Tab: "Q1"}, []sheet.ProjectedRow{
		liveRow("a", 0, gsheet.Row{"id": "a", "name": "ada", "email": "ada@example.com"}),
		liveRow("b", 1, gsheet.Row{"id": "b", "name": "bo", "email": "bo@example.com"}),
	})

	got, err := h.wf.TableInfo(t.Context(), sh)
	if err != nil {
		t.Fatalf("TableInfo err = %v", err)
	}

	want := sheet.TableInfo{
		Columns:           []string{"email", "id", "name"},
		IDColumn:          true,
		Writable:          true,
		SatisfiesContract: true,
		RowCount:          2,
		Generation:        42,
		ValidatedAt:       &validatedAt,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TableInfo = %+v, want %+v", got, want)
	}
	if rows, meta := h.google.counts(); rows != 0 || meta != 0 {
		t.Errorf("Google was called %d times for values and %d for metadata, want 0 and 0", rows, meta)
	}
}

// TestReadWorkflow_TableInfo_ReportsDriftWithoutCallingGoogle is why this route exists: a client must be
// able to learn that a tab no longer satisfies the contract without paying for a Google read.
func TestReadWorkflow_TableInfo_ReportsDriftWithoutCallingGoogle(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	sh.ContractOK = false
	sh.ContractReason = "sheet: tab \"Q1\" has no column named id"
	h.rows.Seed(sheet.SnapshotKey{SheetID: sh.ID, Tab: "Q1"}, []sheet.ProjectedRow{
		liveRow("a", 0, gsheet.Row{"id": "a", "name": "ada"}),
	})

	got, err := h.wf.TableInfo(t.Context(), sh)
	if err != nil {
		t.Fatalf("TableInfo err = %v", err)
	}

	if got.SatisfiesContract {
		t.Error("SatisfiesContract = true, want false for a sheet whose contract_ok is false")
	}
	if want := "sheet: tab \"Q1\" has no column named id"; got.ContractReason != want {
		t.Errorf("ContractReason = %q, want %q", got.ContractReason, want)
	}
	if got.RowCount != 1 {
		t.Errorf("RowCount = %d, want 1 — drift leaves the projected rows alone", got.RowCount)
	}
	if rows, meta := h.google.counts(); rows != 0 || meta != 0 {
		t.Errorf("Google was called %d times for values and %d for metadata, want 0 and 0", rows, meta)
	}
}

func TestReadWorkflow_TableInfo_RowCountExcludesTombstones(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	deletedAt := time.Now().UTC()
	h.rows.Seed(sheet.SnapshotKey{SheetID: sh.ID, Tab: "Q1"}, []sheet.ProjectedRow{
		liveRow("a", 0, gsheet.Row{"id": "a"}),
		{RowID: "b", RowIndex: 1, Data: gsheet.Row{"id": "b"}, DeletedAt: &deletedAt},
		liveRow("c", 2, gsheet.Row{"id": "c"}),
	})

	got, err := h.wf.TableInfo(t.Context(), sh)
	if err != nil {
		t.Fatalf("TableInfo err = %v", err)
	}

	if got.RowCount != 2 {
		t.Errorf("RowCount = %d, want 2 — a tombstone is not a live row", got.RowCount)
	}
}

func TestReadWorkflow_TableInfo_CountsOnlyTheSheetsOwnTab(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	h.rows.Seed(sheet.SnapshotKey{SheetID: sh.ID, Tab: "Q1"}, []sheet.ProjectedRow{
		liveRow("a", 0, gsheet.Row{"id": "a"}),
	})
	h.rows.Seed(sheet.SnapshotKey{SheetID: sh.ID, Tab: "Renamed"}, []sheet.ProjectedRow{
		liveRow("a", 0, gsheet.Row{"id": "a"}),
		liveRow("b", 1, gsheet.Row{"id": "b"}),
	})

	got, err := h.wf.TableInfo(t.Context(), sh)
	if err != nil {
		t.Fatalf("TableInfo err = %v", err)
	}

	if got.RowCount != 1 {
		t.Errorf("RowCount = %d, want 1 — a renamed tab's orphaned rows are not this tab's", got.RowCount)
	}
}

func TestReadWorkflow_TableInfo_NamesDeletedAtOnlyWhenTheTabHasIt(t *testing.T) {
	deletedAt := time.Now().UTC()
	for name, tt := range map[string]struct {
		rows        []sheet.ProjectedRow
		wantCols    []string
		wantSoftDel bool
	}{
		"no soft delete": {
			rows:     []sheet.ProjectedRow{liveRow("a", 0, gsheet.Row{"id": "a", "name": "ada"})},
			wantCols: []string{"id", "name"},
		},
		"soft delete": {
			rows: []sheet.ProjectedRow{
				liveRow("a", 0, gsheet.Row{"id": "a", "name": "ada"}),
				{RowID: "b", RowIndex: 1, Data: gsheet.Row{"id": "b", "name": "bo"}, DeletedAt: &deletedAt},
			},
			wantCols:    []string{"deleted_at", "id", "name"},
			wantSoftDel: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newReadHarness(t, harnessOpts{})
			sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
			h.rows.Seed(sheet.SnapshotKey{SheetID: sh.ID, Tab: "Q1"}, tt.rows)

			got, err := h.wf.TableInfo(t.Context(), sh)
			if err != nil {
				t.Fatalf("TableInfo err = %v", err)
			}

			if !reflect.DeepEqual(got.Columns, tt.wantCols) {
				t.Errorf("Columns = %v, want %v", got.Columns, tt.wantCols)
			}
			if got.SoftDelete != tt.wantSoftDel {
				t.Errorf("SoftDelete = %v, want %v", got.SoftDelete, tt.wantSoftDel)
			}
		})
	}
}

func TestReadWorkflow_TableInfo_UnprojectedSheetReportsAnEmptyTable(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)

	got, err := h.wf.TableInfo(t.Context(), sh)
	if err != nil {
		t.Fatalf("TableInfo err = %v", err)
	}

	if got.RowCount != 0 || got.IDColumn {
		t.Errorf("TableInfo = %+v, want an empty table for a sheet nothing has read yet", got)
	}
	payload, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal err = %v", err)
	}
	if want := `{"columns":[],"idColumn":false,"softDelete":false,"writable":false,` +
		`"satisfiesContract":false,"contractReason":"","rowCount":0,"generation":0,"validatedAt":null}`; string(payload) != want {
		t.Errorf("payload = %s, want %s", payload, want)
	}
}

func TestReadWorkflow_TableInfo_PublicSheetIsRefusedWhileTheCapabilityIsOff(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityPublic, 0)

	_, err := h.wf.TableInfo(t.Context(), sh)
	if !sheet.IsPublicDisabledError(err) {
		t.Fatalf("TableInfo err = %v, want a PublicDisabledError", err)
	}
}

func TestReadWorkflow_TableInfo_StoreFailureIsReportedAsAnIncident(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	h.rows.StatsErr = &sheet.NotFoundError{ID: uuid.Nil.String()}

	if _, err := h.wf.TableInfo(t.Context(), sh); err == nil {
		t.Fatal("TableInfo err = nil, want the store failure")
	}
	if h.unex.Load() != 1 {
		t.Errorf("unexpected was reported %d times, want 1", h.unex.Load())
	}
}
