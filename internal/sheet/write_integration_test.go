//go:build integration

package sheet_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/gworkspace"
	"altalune.id/opensheet/gworkspace/gsheet"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/testutil/fakes"
)

var updateRowRange = regexp.MustCompile(`!A(\d+):`)

// liveSheet is a Google stub that actually holds the tab, so a second patch reads whatever the first wrote. A static body could not tell a serialized pair of writes from a lost one.
type liveSheet struct {
	mu     sync.Mutex
	values [][]string
	gets   int
	puts   int

	delay time.Duration
	url   string
}

func newLiveSheet(t *testing.T, values [][]string, delay time.Duration) *liveSheet {
	t.Helper()
	f := &liveSheet{values: values, delay: delay}
	mux := http.NewServeMux()
	mux.HandleFunc("/v4/spreadsheets/{file}/values/", f.serveValues)
	mux.HandleFunc("/v4/spreadsheets/{file}", f.serveMeta)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	f.url = srv.URL
	return f
}

func (f *liveSheet) serveValues(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut {
		f.applyUpdate(w, r)
		return
	}
	f.mu.Lock()
	f.gets++
	snapshot := make([][]string, len(f.values))
	for i := range f.values {
		snapshot[i] = slices.Clone(f.values[i])
	}
	delay := f.delay
	f.mu.Unlock()

	// NOTE: the delay widens the window an unserialized pair of patches would race in, so dropping the lock makes the assertion fail rather than flake.
	time.Sleep(delay)
	body, err := json.Marshal(map[string]any{"values": snapshot})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, `{"error":{"code":500}}`)
		return
	}
	writeJSON(w, http.StatusOK, string(body))
}

func (f *liveSheet) applyUpdate(w http.ResponseWriter, r *http.Request) {
	match := updateRowRange.FindStringSubmatch(r.URL.Path)
	if match == nil {
		writeJSON(w, http.StatusBadRequest, `{"error":{"code":400}}`)
		return
	}
	row, err := strconv.Atoi(match[1])
	if err != nil || row < 1 {
		writeJSON(w, http.StatusBadRequest, `{"error":{"code":400}}`)
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, `{"error":{"code":400}}`)
		return
	}
	var payload struct {
		Values [][]any `json:"values"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || len(payload.Values) != 1 {
		writeJSON(w, http.StatusBadRequest, `{"error":{"code":400}}`)
		return
	}
	cells := make([]string, len(payload.Values[0]))
	for i, cell := range payload.Values[0] {
		cells[i] = liveCellText(cell)
	}

	f.mu.Lock()
	f.puts++
	for len(f.values) < row {
		f.values = append(f.values, nil)
	}
	f.values[row-1] = cells
	f.mu.Unlock()
	writeJSON(w, http.StatusOK, `{}`)
}

func (f *liveSheet) serveMeta(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, firstTabBody)
}

func (f *liveSheet) row(index int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if index < 0 || index >= len(f.values) {
		return nil
	}
	return slices.Clone(f.values[index])
}

func (f *liveSheet) counts() (gets, puts int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gets, f.puts
}

func (f *liveSheet) factory() gsheet.WriterFactory {
	return func(ctx context.Context, ts oauth2.TokenSource) (*gsheet.Writer, error) {
		return gsheet.NewWriter(ctx, ts, gworkspace.WithBaseURL(f.url))
	}
}

func liveCellText(cell any) string {
	switch t := cell.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	}
	return ""
}

type pgWriteHarness struct {
	snaps *fakes.SheetSnapshots
	rows  sheet.RowStore
	unex  *atomic.Int64
	wf    *sheet.WriteWorkflow
}

func newPgWriteHarness(t *testing.T, f *pgFixture, sh *sheet.Sheet, google *liveSheet) *pgWriteHarness {
	t.Helper()
	rows, units := f.concurrent(t, 4)
	srcs := fakes.NewSheetSources()
	srcs.Set(sh.SpreadsheetID, sheet.Source{GoogleFileID: "FILE", CredentialID: sh.ID})
	h := &pgWriteHarness{snaps: fakes.NewSheetSnapshots(), rows: rows, unex: &atomic.Int64{}}
	unexpected := func(_ context.Context, _ string, err error, _ ...any) *apperror.AppError {
		h.unex.Add(1)
		return apperror.New("opensheet.unexpected", err.Error(), codes.Internal,
			&apperrorv1.ErrorDetail{Code: "opensheet.unexpected"}).WithCause(err)
	}
	h.wf = sheet.NewWriteWorkflow(
		h.snaps, rows, units, sheet.NewMemoryIdempotencyStore(),
		srcs, fakes.NewSheetTokenSources(), fakes.NewSheetReauthers(), google.factory(),
		time.Minute, 1<<20, slog.New(slog.NewTextHandler(io.Discard, nil)), unexpected,
	)
	return h
}

func pgSeedWritableSheet(t *testing.T, f *pgFixture, tree orgTree, slug, tab string) *sheet.Sheet {
	t.Helper()
	sh, err := sheet.New(sheet.NewParams{
		OrgID: tree.orgID, ProjectID: tree.projectID, SpreadsheetID: tree.spreadsheetID,
		Tab: tab, Slug: slug, Visibility: sheet.VisibilityKey, Writable: true,
	})
	require.NoError(t, err)
	require.NoError(t, f.store.Save(tree.ctx(t), sh))
	return sh
}

// TestWriteWorkflow_PatchRow_ConcurrentWritesSerialize is the test the write lock rests on. Drop the transaction the patch enrolls and LockSheet degrades to a plain generation read: both patches then read the pre-write row and one effect is lost.
func TestWriteWorkflow_PatchRow_ConcurrentWritesSerialize(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedWritableSheet(t, f, f.a, "rates", "Rates")
	key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	google := newLiveSheet(t, [][]string{{"id", "name", "qty"}, {"1", "ada", "3"}}, 100*time.Millisecond)
	h := newPgWriteHarness(t, f, sh, google)

	ok, err := h.rows.Replace(ctx, key, 0, []sheet.ProjectedRow{
		{RowID: "1", RowIndex: 0, Data: gsheet.Row{"id": "1", "name": "ada", "qty": "3"}},
	}, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	patches := []map[string]any{{"name": "grace"}, {"qty": "9"}}
	errs := make([]error, len(patches))
	var wg sync.WaitGroup
	for i := range patches {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = h.wf.PatchRow(ctx, sh, "1", patches[i], "")
		}()
	}
	wg.Wait()
	for i := range errs {
		require.NoError(t, errs[i], "patch %d", i)
	}

	live, err := h.rows.ListLive(ctx, key)
	require.NoError(t, err)
	require.Len(t, live, 1)
	assert.Equal(t, gsheet.Row{"id": "1", "name": "grace", "qty": "9"}, live[0].Data,
		"both patches must survive: Values.Update takes no If-Match, so an unserialized pair loses one")
	assert.Equal(t, []string{"1", "grace", "9"}, google.row(1),
		"the spreadsheet itself must carry both effects")

	gets, puts := google.counts()
	assert.Equal(t, 2, gets, "each patch reads the tab once, inside its own lock")
	assert.Equal(t, 2, puts)

	reread, err := f.store.ByID(ctx, sh.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 3, reread.Generation, "the refresh and both patches each bump the generation once")
	assert.Zero(t, h.unex.Load(), "two clean writes report no incident")
}

// TestWriteWorkflow_PatchRow_ReadYourWriteOnTheRealProjection is the postgres twin of the unit-level read-your-write: the patched row must come back from sheet_rows with no further Google call.
func TestWriteWorkflow_PatchRow_ReadYourWriteOnTheRealProjection(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedWritableSheet(t, f, f.a, "rates", "Rates")
	key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	google := newLiveSheet(t, [][]string{{"id", "name"}, {"1", "ada"}, {"2", "bob"}}, 0)
	h := newPgWriteHarness(t, f, sh, google)

	ok, err := h.rows.Replace(ctx, key, 0, []sheet.ProjectedRow{
		{RowID: "1", RowIndex: 0, Data: gsheet.Row{"id": "1", "name": "ada"}},
		{RowID: "2", RowIndex: 1, Data: gsheet.Row{"id": "2", "name": "bob"}},
	}, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	row, err := h.wf.PatchRow(ctx, sh, "1", map[string]any{"name": "ada2"}, "")
	require.NoError(t, err)
	require.Equal(t, gsheet.Row{"id": "1", "name": "ada2"}, row)
	gets, _ := google.counts()

	live, err := h.rows.ListLive(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, []sheet.ProjectedRow{
		{RowID: "1", RowIndex: 0, Data: gsheet.Row{"id": "1", "name": "ada2"}},
		{RowID: "2", RowIndex: 1, Data: gsheet.Row{"id": "2", "name": "bob"}},
	}, live, "the projection must answer the read")

	afterRead, _ := google.counts()
	assert.Equal(t, gets, afterRead, "reading the projection must not reach Google")

	snap, found, err := h.snaps.Get(ctx, key)
	require.NoError(t, err)
	require.True(t, found, "the rebuilt snapshot must be stored, not purged")
	assert.JSONEq(t, `[{"id":"1","name":"ada2"},{"id":"2","name":"bob"}]`, string(snap.Payload))
	assert.Equal(t, etagOf(t, `[{"id":"1","name":"ada2"},{"id":"2","name":"bob"}]`), snap.ETag)
	assert.Zero(t, h.unex.Load())
}

// The comparison must happen inside the per-sheet lock: Google has no conditional write, so two writers
// holding the same tag would both pass an unserialized check and both write, losing one effect. On its
// own pool — appDB caps at one connection, which would serialize the pair at the pool and pass vacuously.
func TestWriteWorkflow_PatchRow_IfMatchRefusesTheLoserUnderRealConcurrency(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedWritableSheet(t, f, f.a, "rates", "Rates")
	key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	google := newLiveSheet(t, [][]string{{"id", "name", "qty"}, {"1", "ada", "3"}}, 100*time.Millisecond)
	h := newPgWriteHarness(t, f, sh, google)

	ok, err := h.rows.Replace(ctx, key, 0, []sheet.ProjectedRow{
		{RowID: "1", RowIndex: 0, Data: gsheet.Row{"id": "1", "name": "ada", "qty": "3"}},
	}, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	tag := `"` + etagOf(t, `{"id":"1","name":"ada","qty":"3"}`) + `"`
	patches := []map[string]any{{"name": "grace"}, {"qty": "9"}}
	errs := make([]error, len(patches))
	var wg sync.WaitGroup
	for i := range patches {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = h.wf.PatchRow(ctx, sh, "1", patches[i], tag)
		}()
	}
	wg.Wait()

	won, refused := 0, 0
	for i, e := range errs {
		if e == nil {
			won++
			continue
		}
		require.True(t, sheet.IsPreconditionFailedError(e), "patch %d failed with %T: %v", i, e, e)
		require.Equal(t, http.StatusPreconditionFailed, appErrorOf(t, e).HTTPStatus())
		refused++
	}
	assert.Equal(t, 1, won, "exactly one writer may win the tag")
	assert.Equal(t, 1, refused, "the loser must be refused, never silently applied")

	_, puts := google.counts()
	assert.Equal(t, 1, puts, "the refused writer must not reach Values.Update")

	live, err := h.rows.ListLive(ctx, key)
	require.NoError(t, err)
	require.Len(t, live, 1)
	assert.Equal(t, "1", live[0].RowID)
	assert.Zero(t, h.unex.Load(), "a refused precondition is a client error, not an incident")
}
