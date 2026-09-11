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

func sortedCursor(t *testing.T, digest string, page int) string {
	t.Helper()
	raw, err := sheet.EncodeSortedRowCursor(
		sheet.RowCursor{Digest: digest, RowIndex: 0, NullRank: 1, Page: page})
	if err != nil {
		t.Fatalf("EncodeSortedRowCursor err = %v", err)
	}
	return raw
}

func unsortedCursor(t *testing.T, digest string) string {
	t.Helper()
	raw, err := sheet.EncodeRowCursor(sheet.RowCursor{Digest: digest, RowIndex: 0})
	if err != nil {
		t.Fatalf("EncodeRowCursor err = %v", err)
	}
	return raw
}

func TestQueryRows_MalformedSortIsRefusedBeforeAnyGoogleCall(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)

	for _, tc := range []struct {
		name string
		sort []string
	}{
		{"given twice", []string{"name:asc", "qty:desc"}},
		{"no direction", []string{"name"}},
		{"a third field", []string{"name:num:asc"}},
		{"unknown direction", []string{"name:sideways"}},
		{"an upper-case direction", []string{"name:ASC"}},
		{"no column", []string{":asc"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.wf.QueryRows(t.Context(), sh, sheet.RowFilter{Sort: tc.sort})
			if !sheet.IsInvalidSortError(err) {
				t.Fatalf("err = %v, want InvalidSortError", err)
			}
			if rows, _ := h.google.counts(); rows != 0 {
				t.Errorf("Google row reads = %d, want a cheap refusal", rows)
			}
		})
	}
}

// NOTE: the pairing sits at step 2 with the window, so a mismatch needs neither the column list nor a refresh.
func TestQueryRows_TheCursorVersionMustPairWithTheSortedness(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)

	for _, tc := range []struct {
		name   string
		filter sheet.RowFilter
	}{
		{"an unsorted cursor on a sorted read", sheet.RowFilter{
			Sort: []string{"name:asc"}, Cursor: unsortedCursor(t, "d"),
		}},
		{"a sorted cursor on an unsorted read", sheet.RowFilter{
			Cursor: sortedCursor(t, "d", 1),
		}},
		{"a sorted cursor on a bare ?sort=", sheet.RowFilter{
			Sort: []string{""}, Cursor: sortedCursor(t, "d", 1),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.wf.QueryRows(t.Context(), sh, tc.filter)
			if !sheet.IsInvalidCursorError(err) {
				t.Fatalf("err = %v, want InvalidCursorError", err)
			}
			if rows, _ := h.google.counts(); rows != 0 {
				t.Errorf("Google row reads = %d, want a cheap refusal", rows)
			}
		})
	}
}

// NOTE: the envelope carries no MAC, so both malformations are reachable by hand and neither may cost a Google call before it is refused.
func TestQueryRows_AForgedSortedCursorIsRefusedBeforeAnyGoogleCall(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	valued := func(t *testing.T, value *string) string {
		t.Helper()
		raw, err := sheet.EncodeSortedRowCursor(
			sheet.RowCursor{Digest: "d", RowIndex: 1, SortValue: value, Page: 1})
		if err != nil {
			t.Fatalf("EncodeSortedRowCursor err = %v", err)
		}
		return raw
	}
	notANumber := "abc"

	for _, tc := range []struct {
		name   string
		filter sheet.RowFilter
	}{
		{"it ranks a value and carries none", sheet.RowFilter{
			Sort: []string{"name:asc"}, Cursor: valued(t, nil),
		}},
		{"its num sort value is outside the grammar", sheet.RowFilter{
			Sort: []string{"qty:num.asc"}, Cursor: valued(t, &notANumber),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.wf.QueryRows(t.Context(), sh, tc.filter)
			if !sheet.IsInvalidCursorError(err) {
				t.Fatalf("err = %v, want InvalidCursorError", err)
			}
			if rows, _ := h.google.counts(); rows != 0 {
				t.Errorf("Google row reads = %d, want the driver's refusal hoisted ahead of the gate", rows)
			}
		})
	}
}

func TestQueryRows_TheSortedWalkDepthIsCapped(t *testing.T) {
	h := newReadHarness(t, harnessOpts{maxSortPages: 3})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	seedFreshProjection(t, h, sh, projectedRows())
	state, err := h.rows.StateOf(t.Context(), sh.ID)
	if err != nil {
		t.Fatalf("StateOf err = %v", err)
	}

	if _, err = h.wf.QueryRows(t.Context(), sh, sheet.RowFilter{
		Sort: []string{"name:asc"}, Cursor: sortedCursor(t, state.Digest, 2),
	}); err != nil {
		t.Fatalf("err = %v, want the page one short of the cap served", err)
	}

	_, err = h.wf.QueryRows(t.Context(), sh, sheet.RowFilter{
		Sort: []string{"name:asc"}, Cursor: sortedCursor(t, state.Digest, 3),
	})
	var depth *sheet.SortDepthError
	if !errors.As(err, &depth) {
		t.Fatalf("err = %v, want SortDepthError at the cap", err)
	}
	if depth.Pages != 3 {
		t.Errorf("Pages = %d, want the plumbed sheets.maxSortPages", depth.Pages)
	}
}

// TestQueryRows_TheSortPageCapIsRefusedAheadOfTheFreshnessGate pins the cap to step 2: behind the gate every capped request pays a refresh, and behind the conditional check a client holding the tag collects a 304 instead of the refusal.
func TestQueryRows_TheSortPageCapIsRefusedAheadOfTheFreshnessGate(t *testing.T) {
	h := newReadHarness(t, harnessOpts{maxSortPages: 3})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)

	// NOTE: nothing is projected yet, so a read reaching the gate would fetch from Google.
	_, err := h.wf.QueryRows(t.Context(), sh,
		sheet.RowFilter{Sort: []string{"name:asc"}, Cursor: sortedCursor(t, "some digest", 3)})
	if !sheet.IsSortDepthError(err) {
		t.Fatalf("err = %v, want SortDepthError", err)
	}
	if rows, _ := h.google.counts(); rows != 0 {
		t.Errorf("Google row reads = %d, want the cap refused before the gate", rows)
	}

	seedFreshProjection(t, h, sh, projectedRows())
	state, err := h.rows.StateOf(t.Context(), sh.ID)
	if err != nil {
		t.Fatalf("StateOf err = %v", err)
	}
	// NOTE: the tag hashes the cursor's row index and not its page count, so a page inside the cap and one past it share a tag — which is what makes the refusal's position observable.
	served, err := h.wf.QueryRows(t.Context(), sh,
		sheet.RowFilter{Sort: []string{"name:asc"}, Cursor: sortedCursor(t, state.Digest, 1)})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	_, err = h.wf.QueryRows(t.Context(), sh, sheet.RowFilter{
		Sort:        []string{"name:asc"},
		Cursor:      sortedCursor(t, state.Digest, 3),
		IfNoneMatch: `"` + served.ETag + `"`,
	})
	if !sheet.IsSortDepthError(err) {
		t.Fatalf("err = %v, want SortDepthError rather than a 304 answered from the page inside the cap", err)
	}
}

// NOTE: an unset sheets.maxSortPages falls back through the one site that owns the default, never an inlined <= 0.
func TestQueryRows_AnUnsetSortPageCapFallsBackToTheDefault(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	seedFreshProjection(t, h, sh, projectedRows())
	state, err := h.rows.StateOf(t.Context(), sh.ID)
	if err != nil {
		t.Fatalf("StateOf err = %v", err)
	}

	if _, err = h.wf.QueryRows(t.Context(), sh, sheet.RowFilter{
		Sort:   []string{"name:asc"},
		Cursor: sortedCursor(t, state.Digest, sheet.DefaultMaxSortPages-1),
	}); err != nil {
		t.Fatalf("err = %v, want the page one short of the default cap served", err)
	}

	_, err = h.wf.QueryRows(t.Context(), sh, sheet.RowFilter{
		Sort:   []string{"name:asc"},
		Cursor: sortedCursor(t, state.Digest, sheet.DefaultMaxSortPages),
	})
	var depth *sheet.SortDepthError
	if !errors.As(err, &depth) {
		t.Fatalf("err = %v, want SortDepthError at the default cap", err)
	}
	if depth.Pages != sheet.DefaultMaxSortPages {
		t.Errorf("Pages = %d, want %d", depth.Pages, sheet.DefaultMaxSortPages)
	}
}

// NOTE: an unsorted walk is bounded by nothing but the row count, and its cursor carries no page count to bound it with.
func TestQueryRows_AnUnsortedWalkIsNotCapped(t *testing.T) {
	h := newReadHarness(t, harnessOpts{maxSortPages: 1})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	seedFreshProjection(t, h, sh, projectedRows())
	state, err := h.rows.StateOf(t.Context(), sh.ID)
	if err != nil {
		t.Fatalf("StateOf err = %v", err)
	}

	if _, err = h.wf.QueryRows(t.Context(), sh,
		sheet.RowFilter{Limit: "1", Cursor: unsortedCursor(t, state.Digest)}); err != nil {
		t.Fatalf("err = %v, want the sort page cap to leave 3a's walk alone", err)
	}
}

func TestQueryRows_AnUnknownSortColumnIsRefused(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	seedFreshProjection(t, h, sh, projectedRows())

	_, err := h.wf.QueryRows(t.Context(), sh, sheet.RowFilter{Sort: []string{"nope:asc"}})
	if !sheet.IsUnknownSortColumnError(err) {
		t.Fatalf("err = %v, want UnknownSortColumnError rather than the unknown-clause-column code", err)
	}
	if sheet.IsUnknownColumnError(err) {
		t.Error("a bad sort column is indistinguishable from a bad clause column")
	}
}

// NOTE: the same carve-out the clauses get — a tab with no live row names no column, so validating the sort column there would 400 every sorted read of a wholly tombstoned or never-refreshed tab.
func TestQueryRows_ATabWithNoLiveRowLeavesTheSortColumnUnvalidated(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	at := time.Now().UTC().Add(-time.Hour)
	seedFreshProjection(t, h, sh, []sheet.ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "apple"}, DeletedAt: &at},
	})

	got, err := h.wf.QueryRows(t.Context(), sh, sheet.RowFilter{Sort: []string{"nope:asc"}})
	if err != nil {
		t.Fatalf("QueryRows err = %v, want an empty page rather than a refusal", err)
	}
	if string(got.Payload) != "[]" {
		t.Errorf("Payload = %s, want []", got.Payload)
	}
}

// TestQueryRows_TheTagSeparatesTheHintAndTheSort exists because every pair below answers a different body at the same generation: a shared tag is a wrong 304, not a slow read.
func TestQueryRows_TheTagSeparatesTheHintAndTheSort(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	seedFreshProjection(t, h, sh, projectedRows())

	tagOf := func(t *testing.T, f sheet.RowFilter) string {
		t.Helper()
		got, err := h.wf.QueryRows(t.Context(), sh, f)
		if err != nil {
			t.Fatalf("QueryRows err = %v", err)
		}
		return got.ETag
	}

	for _, tc := range []struct {
		name string
		one  sheet.RowFilter
		othr sheet.RowFilter
	}{
		{
			name: "a hinted clause against an unhinted one",
			one:  sheet.RowFilter{Where: []string{"qty:gt:10"}},
			othr: sheet.RowFilter{Where: []string{"qty:num.gt:10"}},
		},
		{
			name: "a num hint against a date one",
			one:  sheet.RowFilter{Where: []string{"qty:gte:2026-01-01"}},
			othr: sheet.RowFilter{Where: []string{"qty:date.gte:2026-01-01"}},
		},
		{
			name: "a sorted read against an unsorted one",
			one:  sheet.RowFilter{},
			othr: sheet.RowFilter{Sort: []string{"name:asc"}},
		},
		{
			name: "the two directions",
			one:  sheet.RowFilter{Sort: []string{"name:asc"}},
			othr: sheet.RowFilter{Sort: []string{"name:desc"}},
		},
		{
			name: "the two sort columns",
			one:  sheet.RowFilter{Sort: []string{"name:asc"}},
			othr: sheet.RowFilter{Sort: []string{"qty:asc"}},
		},
		{
			name: "a hinted sort against an unhinted one",
			one:  sheet.RowFilter{Sort: []string{"qty:asc"}},
			othr: sheet.RowFilter{Sort: []string{"qty:num.asc"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if one, othr := tagOf(t, tc.one), tagOf(t, tc.othr); one == othr {
				t.Errorf("both reads tag %q, so one would answer 304 from the other's cache", one)
			}
		})
	}
}

// NOTE: a bare ?sort= is 3a's unsorted read, so it must also be 3a's tag.
func TestQueryRows_ABareSortTagsAsAnUnsortedRead(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	seedFreshProjection(t, h, sh, projectedRows())

	bare, err := h.wf.QueryRows(t.Context(), sh, sheet.RowFilter{Sort: []string{""}})
	if err != nil {
		t.Fatalf("QueryRows err = %v", err)
	}
	none, err := h.wf.QueryRows(t.Context(), sh, sheet.RowFilter{})
	if err != nil {
		t.Fatalf("QueryRows err = %v", err)
	}
	if bare.ETag != none.ETag {
		t.Errorf("ETag = %q with a bare sort and %q with none", bare.ETag, none.ETag)
	}
}
