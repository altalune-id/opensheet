package sheet_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/gworkspace/gsheet"
	"altalune.id/opensheet/internal/platform/queue"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/testutil/fakes"
)

func snapshotInvalidated() queue.Broadcast {
	return queue.Broadcast{Name: "sheet.snapshot_invalidated", Version: 1}
}

func requireOneInvalidation(t *testing.T, q *fakes.Queue, sheetID uuid.UUID, etag string) {
	t.Helper()
	got := q.Emitted()
	require.Len(t, got, 1, "an invalidating change emits exactly once")
	assert.Equal(t, snapshotInvalidated(), got[0].Broadcast)
	body, err := json.Marshal(got[0].Data)
	require.NoError(t, err)
	want := map[string]any{"sheet_id": sheetID.String()}
	if etag != "" {
		want["etag"] = etag
	}
	wantBody, err := json.Marshal(want)
	require.NoError(t, err)
	assert.JSONEq(t, string(wantBody), string(body))
}

func newQueuedSvc(t *testing.T, store sheet.Store, snaps sheet.SnapshotStore, q *fakes.Queue) (*sheet.Service, *int) {
	t.Helper()
	svc, _, calls := newSvcGoogle(t, store, snaps, sheet.NewMemoryIdempotencyStore(), false, sheet.WithQueue(q))
	return svc, calls
}

func savedSheet(t *testing.T, store *fakes.Sheet, orgID, projectID uuid.UUID) *sheet.Sheet {
	t.Helper()
	sh := &sheet.Sheet{
		ID: uuid.Must(uuid.NewV7()), OrgID: orgID, ProjectID: projectID,
		SpreadsheetID: uuid.Must(uuid.NewV7()), Tab: "Q1", Slug: "prices",
		Visibility: sheet.VisibilityKey,
	}
	require.NoError(t, store.Save(t.Context(), sh))
	return sh
}

func TestService_InvalidatingChangesEmitOnceAfterSuccess(t *testing.T) {
	ttl := 2 * time.Minute
	tests := []struct {
		name string
		run  func(ctx context.Context, svc *sheet.Service, id uuid.UUID) error
	}{
		{"PurgeCache", func(ctx context.Context, svc *sheet.Service, id uuid.UUID) error { return svc.PurgeCache(ctx, id) }},
		{"Update", func(ctx context.Context, svc *sheet.Service, id uuid.UUID) error {
			_, err := svc.Update(ctx, id, sheet.UpdateInput{CacheTTL: &ttl})
			return err
		}},
		{"Delete", func(ctx context.Context, svc *sheet.Service, id uuid.UUID) error { return svc.Delete(ctx, id) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, snaps, q := fakes.NewSheet(), fakes.NewSheetSnapshots(), &fakes.Queue{}
			svc, unex := newQueuedSvc(t, store, snaps, q)
			ctx, tc := tenantCtx(t)
			sh := savedSheet(t, store, tc.OrgID, tc.ProjectID)
			key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Q1"}
			snaps.Seed(key, sheet.Snapshot{ETag: "e", Payload: []byte("[]")})

			require.NoError(t, tt.run(ctx, svc, sh.ID))

			requireOneInvalidation(t, q, sh.ID, "")
			_, found, err := snaps.Get(ctx, key)
			require.NoError(t, err)
			assert.False(t, found, "the local snapshot is dropped too")
			assert.Zero(t, *unex)
		})
	}
}

func TestService_FailedInvalidatingChangesDoNotEmit(t *testing.T) {
	tests := []struct {
		name string
		arm  func(store *fakes.Sheet, snaps *fakes.SheetSnapshots)
		run  func(ctx context.Context, svc *sheet.Service, id uuid.UUID) error
	}{
		{"PurgeCache purge fails", func(_ *fakes.Sheet, snaps *fakes.SheetSnapshots) { snaps.PurgeErr = stubError("down") },
			func(ctx context.Context, svc *sheet.Service, id uuid.UUID) error { return svc.PurgeCache(ctx, id) }},
		{"PurgeCache unknown sheet", func(*fakes.Sheet, *fakes.SheetSnapshots) {},
			func(ctx context.Context, svc *sheet.Service, _ uuid.UUID) error {
				return svc.PurgeCache(ctx, uuid.New())
			}},
		{"Update save fails", func(store *fakes.Sheet, _ *fakes.SheetSnapshots) {
			store.SaveFn = func(context.Context, *sheet.Sheet) error { return stubError("down") }
		}, func(ctx context.Context, svc *sheet.Service, id uuid.UUID) error {
			_, err := svc.Update(ctx, id, sheet.UpdateInput{})
			return err
		}},
		{"Delete fails", func(store *fakes.Sheet, _ *fakes.SheetSnapshots) {
			store.DeleteFn = func(context.Context, uuid.UUID) error { return stubError("down") }
		}, func(ctx context.Context, svc *sheet.Service, id uuid.UUID) error { return svc.Delete(ctx, id) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, snaps, q := fakes.NewSheet(), fakes.NewSheetSnapshots(), &fakes.Queue{}
			svc, _ := newQueuedSvc(t, store, snaps, q)
			ctx, tc := tenantCtx(t)
			sh := savedSheet(t, store, tc.OrgID, tc.ProjectID)
			tt.arm(store, snaps)

			require.Error(t, tt.run(ctx, svc, sh.ID))
			assert.Empty(t, q.Emitted(), "a change that did not happen invalidates nothing")
		})
	}
}

func TestService_EmitFailureIsReportedAndStillSucceeds(t *testing.T) {
	store, snaps, q := fakes.NewSheet(), fakes.NewSheetSnapshots(), &fakes.Queue{EmitErr: stubError("nats down")}
	svc, unex := newQueuedSvc(t, store, snaps, q)
	ctx, tc := tenantCtx(t)
	sh := savedSheet(t, store, tc.OrgID, tc.ProjectID)

	require.NoError(t, svc.PurgeCache(ctx, sh.ID), "the purge happened, so the caller sees success")
	assert.Len(t, q.Emitted(), 1)
	assert.Equal(t, 1, *unex, "the emit failure is reported")
}

func TestWriteWorkflow_InvalidatingWritesEmitOnce(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		seedRows bool
		run      func(t *testing.T, h *writeHarness, sh *sheet.Sheet) error
		etag     string
	}{
		{name: "Append purges", run: func(t *testing.T, h *writeHarness, sh *sheet.Sheet) error {
			_, err := h.wf.Append(t.Context(), sh, []any{"x"}, "", "")
			return err
		}},
		{name: "PatchRow over a short projection purges", run: func(t *testing.T, h *writeHarness, sh *sheet.Sheet) error {
			_, err := h.wf.PatchRow(t.Context(), sh, "1", map[string]any{"name": "ada2"}, "")
			return err
		}},
		{name: "PatchRow rebuild carries the new etag", seedRows: true, etag: `[{"id":"1","name":"ada2"},{"id":"2","name":"bob"}]`,
			run: func(t *testing.T, h *writeHarness, sh *sheet.Sheet) error {
				_, err := h.wf.PatchRow(t.Context(), sh, "1", map[string]any{"name": "ada2"}, "")
				return err
			}},
		{name: "CreateRow rebuild", seedRows: true, etag: "*", run: func(t *testing.T, h *writeHarness, sh *sheet.Sheet) error {
			_, err := h.wf.CreateRow(t.Context(), sh, map[string]any{"name": "cyd"}, "", "")
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			q := &fakes.Queue{}
			h := newWriteHarnessWith(t, writeOpts{queue: q})
			sh, _ := h.seed(t, "Rates", true)
			h.seedContract(sh)
			key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}
			if tt.seedRows {
				h.rows.Seed(key, []sheet.ProjectedRow{
					{RowID: "1", RowIndex: 0, Data: gsheet.Row{"id": "1", "name": "ada"}},
					{RowID: "2", RowIndex: 1, Data: gsheet.Row{"id": "2", "name": "bob"}},
				})
			}

			require.NoError(t, tt.run(t, h, sh))

			switch tt.etag {
			case "":
				requireOneInvalidation(t, q, sh.ID, "")
			case "*":
				snap, found, err := h.snaps.Get(t.Context(), key)
				require.NoError(t, err)
				require.True(t, found)
				requireOneInvalidation(t, q, sh.ID, snap.ETag)
			default:
				requireOneInvalidation(t, q, sh.ID, etagOf(t, tt.etag))
			}
			assert.Zero(t, h.unex.Load())
		})
	}
}

func TestWriteWorkflow_SoftDeleteRowEmitsOnce(t *testing.T) {
	t.Parallel()
	q := &fakes.Queue{}
	h := newWriteHarnessWith(t, writeOpts{queue: q})
	sh, _ := h.seed(t, "Rates", true)
	sh.SoftDelete = true
	h.seedContract(sh)
	h.google.setTable(200, twoLiveTable)
	key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}
	h.rows.Seed(key, []sheet.ProjectedRow{
		{RowID: "1", RowIndex: 0, Data: gsheet.Row{"id": "1", "name": "ada"}},
		{RowID: "2", RowIndex: 1, Data: gsheet.Row{"id": "2", "name": "bob"}},
	})

	require.NoError(t, h.wf.SoftDeleteRow(t.Context(), sh, "1", ""))

	requireOneInvalidation(t, q, sh.ID, etagOf(t, `[{"id":"2","name":"bob"}]`))
}

func TestWriteWorkflow_FailedWritesDoNotEmit(t *testing.T) {
	t.Parallel()
	q := &fakes.Queue{}
	h := newWriteHarnessWith(t, writeOpts{queue: q})
	sh, _ := h.seed(t, "Rates", false)

	_, err := h.wf.Append(t.Context(), sh, []any{"x"}, "", "")
	require.Error(t, err)
	_, err = h.wf.PatchRow(t.Context(), sh, "1", map[string]any{"name": "x"}, "")
	require.Error(t, err)

	sh.Writable = true
	_, err = h.wf.PatchRow(t.Context(), sh, "99", map[string]any{"name": "x"}, "")
	require.True(t, sheet.IsRowNotFoundError(err), "got %v", err)

	assert.Empty(t, q.Emitted())
}

func TestWriteWorkflow_EmitFailureIsReportedAndTheWriteStillSucceeds(t *testing.T) {
	t.Parallel()
	q := &fakes.Queue{EmitErr: stubError("nats down")}
	h := newWriteHarnessWith(t, writeOpts{queue: q})
	sh, _ := h.seed(t, "Rates", true)

	n, err := h.wf.Append(t.Context(), sh, []any{"x"}, "", "")
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Len(t, q.Emitted(), 1)
	assert.Equal(t, int64(1), h.unex.Load(), "the emit failure is reported")
}

func invalidation(t *testing.T, sheetID uuid.UUID, etag string) queue.Message {
	t.Helper()
	body, err := json.Marshal(map[string]any{"sheet_id": sheetID, "etag": etag})
	require.NoError(t, err)
	return queue.Message{ID: uuid.Must(uuid.NewV7()), Name: "sheet.snapshot_invalidated", Version: 1, Data: body}
}

func snapshotListener(t *testing.T, snaps sheet.SnapshotStore) queue.Listener {
	t.Helper()
	ls := sheet.NewSnapshotListener(snaps).Listeners()
	require.Len(t, ls, 1)
	require.Equal(t, snapshotInvalidated(), ls[0].Broadcast)
	return ls[0]
}

func seedMemory(t *testing.T, snaps sheet.SnapshotStore, k sheet.SnapshotKey, etag string) {
	t.Helper()
	require.NoError(t, snaps.Put(t.Context(), k, sheet.Snapshot{ETag: etag, FetchedAt: time.Now(), Payload: []byte("[]")}, time.Minute))
}

func cached(t *testing.T, snaps sheet.SnapshotStore, k sheet.SnapshotKey) bool {
	t.Helper()
	_, found, err := snaps.Get(t.Context(), k)
	require.NoError(t, err)
	return found
}

func TestSnapshotListener_DropsOnlyTheNamedSheetFromMemory(t *testing.T) {
	t.Parallel()
	snaps := sheet.NewMemorySnapshotStoreForTest(1 << 20)
	a, b := uuid.New(), uuid.New()
	a1, a2, b1 := sheet.SnapshotKey{SheetID: a, Tab: "Q1"}, sheet.SnapshotKey{SheetID: a, Tab: "Q2"}, sheet.SnapshotKey{SheetID: b, Tab: "Q1"}
	for _, k := range []sheet.SnapshotKey{a1, a2, b1} {
		seedMemory(t, snaps, k, "e")
	}
	l := snapshotListener(t, snaps)

	require.NoError(t, l.Handle(t.Context(), invalidation(t, a, "")))
	require.NoError(t, l.Handle(t.Context(), invalidation(t, a, "")), "the listener is idempotent")

	assert.False(t, cached(t, snaps, a1))
	assert.False(t, cached(t, snaps, a2))
	assert.True(t, cached(t, snaps, b1), "another sheet's snapshot is untouched")
}

func TestSnapshotListener_KeepsAnEntryHoldingTheAnnouncedETag(t *testing.T) {
	t.Parallel()
	snaps := sheet.NewMemorySnapshotStoreForTest(1 << 20)
	id := uuid.New()
	current, stale := sheet.SnapshotKey{SheetID: id, Tab: "Q1"}, sheet.SnapshotKey{SheetID: id, Tab: "Q2"}
	seedMemory(t, snaps, current, "new")
	seedMemory(t, snaps, stale, "old")

	require.NoError(t, snapshotListener(t, snaps).Handle(t.Context(), invalidation(t, id, "new")))

	assert.True(t, cached(t, snaps, current), "the emitter's own write-through snapshot survives hearing itself")
	assert.False(t, cached(t, snaps, stale))
}

func TestSnapshotListener_SharedStoresAreANoOp(t *testing.T) {
	t.Parallel()
	for name, snaps := range map[string]sheet.SnapshotStore{
		"postgres": sheet.NewPostgresSnapshotStoreForTest(),
		"fake":     fakes.NewSheetSnapshots(),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.NoError(t, snapshotListener(t, snaps).Handle(t.Context(), invalidation(t, uuid.New(), "")))
		})
	}
}

func TestSnapshotListener_RejectsAMalformedPayload(t *testing.T) {
	t.Parallel()
	m := queue.Message{Name: "sheet.snapshot_invalidated", Version: 1, Data: []byte(`{"sheet_id":"nope"}`)}
	require.Error(t, snapshotListener(t, sheet.NewMemorySnapshotStoreForTest(1<<20)).Handle(t.Context(), m))
}
