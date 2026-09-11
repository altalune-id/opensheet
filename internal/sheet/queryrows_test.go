package sheet_test

import (
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"altalune.id/opensheet/gworkspace/gsheet"
	"altalune.id/opensheet/internal/sheet"
)

func projectedRows() []sheet.ProjectedRow {
	return []sheet.ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "apple", "qty": "3"}},
		{RowID: "b", RowIndex: 1, Data: gsheet.Row{"id": "b", "name": "pear", "qty": "5"}},
	}
}

// seedFreshProjection leaves the fake holding rows, a digest and a contract, and sh looking freshly validated.
func seedFreshProjection(t *testing.T, h *readHarness, sh *sheet.Sheet, rows []sheet.ProjectedRow) {
	t.Helper()
	key := sheet.SnapshotKey{SheetID: sh.ID, Tab: sh.Tab}
	applied, err := h.rows.Replace(t.Context(), key, sh.Generation, rows, sheet.ContractState{OK: true})
	if err != nil || !applied {
		t.Fatalf("Replace = applied:%v err:%v", applied, err)
	}
	sh.Generation = h.rows.Generation(sh.ID)
	at := time.Now().UTC()
	sh.ValidatedAt = &at
	sh.ContentDigest = "seeded"
}

func TestQueryRows_FreshProjectionAnswersWithoutAGoogleCall(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	seedFreshProjection(t, h, sh, projectedRows())

	got, err := h.wf.QueryRows(t.Context(), sh, sheet.RowFilter{Limit: "10"})
	if err != nil {
		t.Fatalf("QueryRows err = %v", err)
	}
	if string(got.Payload) != twoRowJSON {
		t.Errorf("Payload = %s, want %s", got.Payload, twoRowJSON)
	}
	if got.ETag == "" {
		t.Error("ETag is empty, want the filtered tag")
	}
	if !got.Cached {
		t.Error("Cached = false, want a read answered from the projection")
	}
	if rows, _ := h.google.counts(); rows != 0 {
		t.Errorf("Google row reads = %d, want 0 for a projection still inside its TTL", rows)
	}
}

func TestQueryRows_StaleProjectionIsRefreshedBeforeTheClausesAreValidated(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)

	got, err := h.wf.QueryRows(t.Context(), sh, sheet.RowFilter{Where: []string{"name:eq:apple"}})
	if err != nil {
		t.Fatalf("QueryRows err = %v, want a never-refreshed sheet to project first", err)
	}
	if got.Cached {
		t.Error("Cached = true, want a read that fetched from Google")
	}
	if rows, _ := h.google.counts(); rows != 1 {
		t.Errorf("Google row reads = %d, want exactly 1", rows)
	}
	if h.rows.ReplaceCount() != 1 {
		t.Errorf("ReplaceCount = %d, want the projection written once", h.rows.ReplaceCount())
	}
}

func TestQueryRows_MalformedWindowIsRefusedBeforeAnyGoogleCall(t *testing.T) {
	h := newReadHarness(t, harnessOpts{maxQueryRows: 5})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)

	for _, tc := range []struct {
		name   string
		filter sheet.RowFilter
		pred   func(error) bool
	}{
		{"over the cap", sheet.RowFilter{Limit: "6"}, sheet.IsInvalidLimitError},
		{"not a number", sheet.RowFilter{Limit: "many"}, sheet.IsInvalidLimitError},
		{"unreadable cursor", sheet.RowFilter{Cursor: "not-a-cursor"}, sheet.IsInvalidCursorError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.wf.QueryRows(t.Context(), sh, tc.filter)
			if !tc.pred(err) {
				t.Fatalf("err = %v, want the window refusal", err)
			}
			if rows, _ := h.google.counts(); rows != 0 {
				t.Errorf("Google row reads = %d, want a cheap refusal", rows)
			}
		})
	}
}

func TestQueryRows_InvalidLimitNamesTheConfiguredCap(t *testing.T) {
	h := newReadHarness(t, harnessOpts{maxQueryRows: 5})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)

	_, err := h.wf.QueryRows(t.Context(), sh, sheet.RowFilter{Limit: "6"})
	var limit *sheet.InvalidLimitError
	if !errors.As(err, &limit) {
		t.Fatalf("err = %v, want InvalidLimitError", err)
	}
	if limit.MaxRows != 5 {
		t.Errorf("MaxRows = %d, want the plumbed sheets.maxQueryRows", limit.MaxRows)
	}
}

func TestQueryRows_AbsentLimitDefaultsToTheCap(t *testing.T) {
	h := newReadHarness(t, harnessOpts{maxQueryRows: 5})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	seedFreshProjection(t, h, sh, projectedRows())

	bare, err := h.wf.QueryRows(t.Context(), sh, sheet.RowFilter{Where: []string{"name:eq:apple"}})
	if err != nil {
		t.Fatalf("QueryRows err = %v", err)
	}
	capped, err := h.wf.QueryRows(t.Context(), sh,
		sheet.RowFilter{Where: []string{"name:eq:apple"}, Limit: "5"})
	if err != nil {
		t.Fatalf("QueryRows err = %v", err)
	}
	if bare.ETag != capped.ETag {
		t.Errorf("ETag = %q with no limit and %q at the cap, want the cap to be the default",
			bare.ETag, capped.ETag)
	}
}

func TestQueryRows_UnknownColumnIsRefused(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	seedFreshProjection(t, h, sh, projectedRows())

	_, err := h.wf.QueryRows(t.Context(), sh, sheet.RowFilter{Where: []string{"nope:eq:1"}})
	if !sheet.IsUnknownColumnError(err) {
		t.Fatalf("err = %v, want UnknownColumnError rather than an empty page", err)
	}
}

// NOTE: the deliberate answer for a tab holding a complete projection whose every row is tombstoned; docs/BACKLOG.md carries the store-level gap that makes it necessary.
func TestQueryRows_ATabWithNoLiveRowAnswersWithAnEmptyPage(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	at := time.Now().UTC().Add(-time.Hour)
	seedFreshProjection(t, h, sh, []sheet.ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "apple"}, DeletedAt: &at},
	})

	got, err := h.wf.QueryRows(t.Context(), sh, sheet.RowFilter{Where: []string{"name:eq:apple"}})
	if err != nil {
		t.Fatalf("QueryRows err = %v, want an empty page rather than a refusal", err)
	}
	if string(got.Payload) != "[]" {
		t.Errorf("Payload = %s, want []", got.Payload)
	}
}

// NOTE: the filtered path never marshals the whole tab, so maxPayloadBytes cannot refuse it — and the !applied branch must not route through committed, which would.
func TestQueryRows_ATabOverThePayloadCapIsStillQueryable(t *testing.T) {
	h := newReadHarness(t, harnessOpts{maxPayloadBytes: 8})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)

	got, err := h.wf.QueryRows(t.Context(), sh, sheet.RowFilter{Limit: "10"})
	if err != nil {
		t.Fatalf("QueryRows err = %v, want a tab too large to snapshot still queryable", err)
	}
	if string(got.Payload) != twoRowJSON {
		t.Errorf("Payload = %s, want %s", got.Payload, twoRowJSON)
	}
}

func TestQueryRows_ADriftedSheetIsRefused(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	seedFreshProjection(t, h, sh, projectedRows())
	h.rows.SeedContract(sh.ID, sheet.ContractState{OK: false, Reason: "the id column is gone"})

	_, err := h.wf.QueryRows(t.Context(), sh, sheet.RowFilter{Limit: "10"})
	if !sheet.IsContractViolationError(err) {
		t.Fatalf("err = %v, want ContractViolationError", err)
	}
}

func TestQueryRows_ACursorFromAnotherContentDigestIsRefused(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	seedFreshProjection(t, h, sh, projectedRows())

	stale, err := sheet.EncodeRowCursor(sheet.RowCursor{Digest: "a digest nobody issued", RowIndex: 0})
	if err != nil {
		t.Fatalf("EncodeRowCursor err = %v", err)
	}
	_, err = h.wf.QueryRows(t.Context(), sh, sheet.RowFilter{Cursor: stale})
	if !sheet.IsStaleCursorError(err) {
		t.Fatalf("err = %v, want StaleCursorError rather than a silently resumed walk", err)
	}
}

func TestQueryRows_AMatchingIfNoneMatchSkipsTheQuery(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	seedFreshProjection(t, h, sh, projectedRows())

	first, err := h.wf.QueryRows(t.Context(), sh, sheet.RowFilter{Limit: "10"})
	if err != nil {
		t.Fatalf("QueryRows err = %v", err)
	}
	again, err := h.wf.QueryRows(t.Context(), sh,
		sheet.RowFilter{Limit: "10", IfNoneMatch: `"` + first.ETag + `"`})
	if err != nil {
		t.Fatalf("QueryRows err = %v", err)
	}
	if !again.NotModified {
		t.Error("NotModified = false, want the tag to answer the request")
	}
	if again.Payload != nil {
		t.Errorf("Payload = %s, want no body behind a 304", again.Payload)
	}
	if again.ETag != first.ETag {
		t.Errorf("ETag = %q, want %q", again.ETag, first.ETag)
	}
}

func TestQueryRows_TheTagIgnoresClauseOrderAndTracksTheGeneration(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	seedFreshProjection(t, h, sh, projectedRows())

	one, err := h.wf.QueryRows(t.Context(), sh,
		sheet.RowFilter{Where: []string{"name:eq:apple", "qty:eq:3"}})
	if err != nil {
		t.Fatalf("QueryRows err = %v", err)
	}
	other, err := h.wf.QueryRows(t.Context(), sh,
		sheet.RowFilter{Where: []string{"qty:eq:3", "name:eq:apple"}})
	if err != nil {
		t.Fatalf("QueryRows err = %v", err)
	}
	if one.ETag != other.ETag {
		t.Errorf("ETag = %q and %q, want clause order not to matter", one.ETag, other.ETag)
	}

	narrower, err := h.wf.QueryRows(t.Context(), sh, sheet.RowFilter{Where: []string{"name:eq:apple"}})
	if err != nil {
		t.Fatalf("QueryRows err = %v", err)
	}
	if narrower.ETag == one.ETag {
		t.Error("two different filters share one tag")
	}

	h.rows.Bump(sh.ID)
	moved, err := h.wf.QueryRows(t.Context(), sh,
		sheet.RowFilter{Where: []string{"name:eq:apple", "qty:eq:3"}})
	if err != nil {
		t.Fatalf("QueryRows err = %v", err)
	}
	if moved.ETag == one.ETag {
		t.Error("the tag survived a generation bump, so a changed sheet would 304")
	}
}

func TestQueryRows_APageIsADifferentResource(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	seedFreshProjection(t, h, sh, projectedRows())

	first, err := h.wf.QueryRows(t.Context(), sh, sheet.RowFilter{Limit: "1"})
	if err != nil {
		t.Fatalf("QueryRows err = %v", err)
	}
	state, err := h.rows.StateOf(t.Context(), sh.ID)
	if err != nil {
		t.Fatalf("StateOf err = %v", err)
	}
	cursor, err := sheet.EncodeRowCursor(sheet.RowCursor{Digest: state.Digest, RowIndex: 0})
	if err != nil {
		t.Fatalf("EncodeRowCursor err = %v", err)
	}
	next, err := h.wf.QueryRows(t.Context(), sh, sheet.RowFilter{Limit: "1", Cursor: cursor})
	if err != nil {
		t.Fatalf("QueryRows err = %v", err)
	}
	if next.ETag == first.ETag {
		t.Error("two pages share one tag, so page 2 would answer 304 from page 1's cache")
	}
}

func TestQueryRows_APublicSheetIsRefusedWhenTheCapabilityIsOff(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityPublic, 0)
	seedFreshProjection(t, h, sh, projectedRows())

	_, err := h.wf.QueryRows(t.Context(), sh, sheet.RowFilter{Limit: "10"})
	if !sheet.IsPublicDisabledError(err) {
		t.Fatalf("err = %v, want PublicDisabledError", err)
	}
}

func TestQueryRows_AnUnreachableGoogleStillAnswersFromTheProjection(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	seedFreshProjection(t, h, sh, projectedRows())
	past := time.Now().UTC().Add(-time.Hour)
	sh.ValidatedAt = &past
	h.google.setRows(http.StatusServiceUnavailable, `{"error":{"code":503}}`)

	got, err := h.wf.QueryRows(t.Context(), sh, sheet.RowFilter{Limit: "10"})
	if err != nil {
		t.Fatalf("QueryRows err = %v, want the projection to answer", err)
	}
	if !got.Stale {
		t.Error("Stale = false, want the answer marked stale")
	}
	if string(got.Payload) != twoRowJSON {
		t.Errorf("Payload = %s, want %s", got.Payload, twoRowJSON)
	}
}

func TestQueryRows_AnUnreachableGoogleWithNothingProjectedFails(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	h.google.setRows(http.StatusServiceUnavailable, `{"error":{"code":503}}`)

	if _, err := h.wf.QueryRows(t.Context(), sh, sheet.RowFilter{Limit: "10"}); err == nil {
		t.Fatal("QueryRows err = nil, want the outage reported rather than an empty page")
	}
}

// NOTE: a filtered read must never serve a concurrent unfiltered caller, which fetch's discarded type assertion would let it do if both shared one singleflight key.
func TestQueryRows_DoesNotShareTheWholeTabSingleflightKey(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	release := make(chan struct{})
	h.google.blockOn(release)

	entered := make(chan struct{}, 2)
	var (
		wg      sync.WaitGroup
		tab     sheet.Rows
		tabErr  error
		pageErr error
	)
	wg.Go(func() {
		entered <- struct{}{}
		tab, tabErr = h.wf.Rows(t.Context(), sh)
	})
	wg.Go(func() {
		entered <- struct{}{}
		_, pageErr = h.wf.QueryRows(t.Context(), sh, sheet.RowFilter{Limit: "10"})
	})
	<-entered
	<-entered
	// NOTE: both loaders must be in flight at once, so the window is widened deliberately rather than raced.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if tabErr != nil || pageErr != nil {
		t.Fatalf("Rows err = %v, QueryRows err = %v", tabErr, pageErr)
	}
	if rows, _ := h.google.counts(); rows != 2 {
		t.Errorf("google row calls = %d, want one per path", rows)
	}
	if tab.ETag != etagOf(t, twoRowJSON) || string(tab.Payload) != twoRowJSON {
		t.Errorf("Rows = etag:%q payload:%s, want the whole-tab answer", tab.ETag, tab.Payload)
	}
}
