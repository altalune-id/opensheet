package sheet_test

import (
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"altalune.id/opensheet/gworkspace/gsheet"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/testutil/fakes"
)

const tombstonedBody = `{"values":[["id","name","deleted_at"],["a","apple",""],["b","pear","2026-01-02T03:04:05Z"]]}`

func TestReadWorkflow_RowByID_ServesTheRowAndItsETag(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)

	got, err := h.wf.RowByID(t.Context(), sh, "b")
	if err != nil {
		t.Fatalf("RowByID err = %v", err)
	}
	want := gsheet.Row{"id": "b", "name": "pear", "qty": "5"}
	if !reflect.DeepEqual(got.Data, want) {
		t.Errorf("Data = %#v, want %#v", got.Data, want)
	}
	if string(got.Payload) != `{"id":"b","name":"pear","qty":"5"}` {
		t.Errorf("Payload = %s, want the row's own JSON", got.Payload)
	}
	if got.ETag != etagOf(t, `{"id":"b","name":"pear","qty":"5"}`) {
		t.Errorf("ETag = %q, want the payload hash the tab ETag is taken with", got.ETag)
	}
	if got.FetchedAt.IsZero() {
		t.Error("FetchedAt is zero, want the tab read's instant")
	}
}

func TestReadWorkflow_RowByID_TombstonedRowIsNotFound(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	h.google.setRows(http.StatusOK, tombstonedBody)
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)

	live, err := h.wf.RowByID(t.Context(), sh, "a")
	if err != nil {
		t.Fatalf("RowByID(a) err = %v, want the live row", err)
	}
	if live.Data["name"] != "apple" {
		t.Errorf("Data = %#v, want the live row", live.Data)
	}
	if _, ok := live.Data["deleted_at"]; ok {
		t.Errorf("Data = %#v, want deleted_at stripped", live.Data)
	}

	_, err = h.wf.RowByID(t.Context(), sh, "b")
	if !sheet.IsRowNotFoundError(err) {
		t.Errorf("RowByID(b) err = %v, want RowNotFoundError for a tombstoned row", err)
	}
}

func TestReadWorkflow_RowByID_UnknownIDIsNotFound(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)

	_, err := h.wf.RowByID(t.Context(), sh, "nobody")
	if !sheet.IsRowNotFoundError(err) {
		t.Errorf("err = %v, want RowNotFoundError", err)
	}
}

// TestReadWorkflow_RowByID_DriftFoundByTheRefreshIsAContractViolation is the case a pre-refresh contract
// read gets wrong: the aggregate still says the contract holds, and only the refresh discovers otherwise.
func TestReadWorkflow_RowByID_DriftFoundByTheRefreshIsAContractViolation(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	sh.ContractOK = true
	key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Q1"}
	h.rows.Seed(key, []sheet.ProjectedRow{{RowID: "a", Data: gsheet.Row{"id": "a", "name": "apple"}}})
	h.rows.SeedContract(sh.ID, sheet.ContractState{OK: true})
	h.snaps.Seed(key, sheet.Snapshot{
		ETag:      "cached-etag",
		FetchedAt: time.Now().UTC().Add(-time.Hour),
		ExpiresAt: time.Now().UTC().Add(-time.Minute),
		Payload:   []byte(`[{"id":"a","name":"apple"}]`),
	})
	h.google.setRows(http.StatusOK, `{"values":[["name","qty"],["apple","3"]]}`)

	_, err := h.wf.RowByID(t.Context(), sh, "a")
	if !sheet.IsContractViolationError(err) {
		t.Fatalf("err = %v, want ContractViolationError from the contract the refresh persisted", err)
	}
	if state := h.rows.Contract(sh.ID); state.OK {
		t.Error("the refresh must have persisted the drift, or this test proves nothing")
	}
	if rows, _ := h.google.counts(); rows != 1 {
		t.Errorf("google row calls = %d, want 1 — the refusal must follow a refresh", rows)
	}
}

func TestReadWorkflow_RowByID_FreshSnapshotCostsNoGoogleCall(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Q1"}
	h.rows.Seed(key, []sheet.ProjectedRow{{RowID: "a", Data: gsheet.Row{"id": "a", "name": "apple"}}})
	h.rows.SeedContract(sh.ID, sheet.ContractState{OK: true})
	h.snaps.Seed(key, sheet.Snapshot{
		ETag:      "cached-etag",
		FetchedAt: time.Now().UTC().Add(-time.Second),
		ExpiresAt: time.Now().UTC().Add(time.Hour),
		Payload:   []byte(`[{"id":"a","name":"apple"}]`),
	})

	got, err := h.wf.RowByID(t.Context(), sh, "a")
	if err != nil {
		t.Fatalf("RowByID err = %v", err)
	}
	if got.Data["name"] != "apple" || !got.Cached {
		t.Errorf("Data/Cached = %#v/%v, want the projected row from the cache", got.Data, got.Cached)
	}
	if rows, meta := h.google.counts(); rows != 0 || meta != 0 {
		t.Errorf("google calls = rows:%d meta:%d, want none for a fresh snapshot", rows, meta)
	}
}

// TestReadWorkflow_RowByID_UnnamedTabResolvesFromTheProjection covers an empty sheets.tab, whose real
// name only Google knows: the busiest projected tab stands in, as Stats resolves it.
func TestReadWorkflow_RowByID_UnnamedTabResolvesFromTheProjection(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "", sheet.VisibilityKey, 0)

	got, err := h.wf.RowByID(t.Context(), sh, "a")
	if err != nil {
		t.Fatalf("RowByID err = %v", err)
	}
	if got.Data["name"] != "apple" {
		t.Errorf("Data = %#v, want the row from the tab Google named first", got.Data)
	}
	if projected := h.rows.Projected(sheet.SnapshotKey{SheetID: sh.ID, Tab: "First"}); len(projected) != 2 {
		t.Errorf("projected rows under %q = %d, want the refreshed tab", "First", len(projected))
	}
}

// TestReadWorkflow_RowByID_ProjectionFailureIsAnIncident keeps a broken store off the not-found path: a
// 404 would tell the caller the row is gone.
func TestReadWorkflow_RowByID_ProjectionFailureIsAnIncident(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*fakes.SheetRows)
	}{
		{"contract read", func(f *fakes.SheetRows) { f.StateOfErr = errors.New("contract read failed") }},
		{"row read", func(f *fakes.SheetRows) { f.RowByIDErr = errors.New("row read failed") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newReadHarness(t, harnessOpts{})
			sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
			tc.set(h.rows)

			_, err := h.wf.RowByID(t.Context(), sh, "a")
			if sheet.IsRowNotFoundError(err) || sheet.IsContractViolationError(err) {
				t.Fatalf("err = %v, want an incident rather than a client refusal", err)
			}
			if h.unex.Load() != 1 {
				t.Errorf("unexpected called %d times, want 1 for the failed projection read", h.unex.Load())
			}
		})
	}
}

func TestReadWorkflow_RowByID_PublicSheetNeedsTheCapability(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityPublic, 0)

	_, err := h.wf.RowByID(t.Context(), sh, "a")
	if !sheet.IsPublicDisabledError(err) {
		t.Errorf("err = %v, want PublicDisabledError", err)
	}
}
