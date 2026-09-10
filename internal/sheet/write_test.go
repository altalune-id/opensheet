package sheet_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
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

const (
	idNameTable   = `{"values":[["id","name"],["1","ada"],["2","bob"]]}`
	blankRowTable = `{"values":[["id","name"],["1","ada"],[],["3","cyd"]]}`
	appendedOne   = `{"updates":{"updatedRows":1,"updatedRange":"'Rates'!A4:B4"}}`
)

type recordedRequest struct {
	method string
	url    string
	body   string
}

type fakeWriteSheets struct {
	mu       sync.Mutex
	requests []recordedRequest

	tableStatus  int
	tableBody    string
	appendStatus int
	appendBody   string
	updateStatus int
	updateBody   string
	metaStatus   int
	metaBody     string

	url string
}

func newFakeWriteSheets(t *testing.T) *fakeWriteSheets {
	t.Helper()
	f := &fakeWriteSheets{
		tableStatus:  http.StatusOK,
		tableBody:    idNameTable,
		appendStatus: http.StatusOK,
		appendBody:   appendedOne,
		updateStatus: http.StatusOK,
		updateBody:   `{}`,
		metaStatus:   http.StatusOK,
		metaBody:     firstTabBody,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v4/spreadsheets/{file}/values/", f.serveValues)
	mux.HandleFunc("/v4/spreadsheets/{file}", f.serveMeta)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	f.url = srv.URL
	return f
}

func (f *fakeWriteSheets) serveValues(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.record(r, body)

	f.mu.Lock()
	status, out := f.appendStatus, f.appendBody
	switch r.Method {
	case http.MethodGet:
		status, out = f.tableStatus, f.tableBody
	case http.MethodPut:
		status, out = f.updateStatus, f.updateBody
	}
	f.mu.Unlock()
	writeJSON(w, status, out)
}

func (f *fakeWriteSheets) serveMeta(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.record(r, body)

	f.mu.Lock()
	status, out := f.metaStatus, f.metaBody
	f.mu.Unlock()
	writeJSON(w, status, out)
}

func (f *fakeWriteSheets) record(r *http.Request, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, recordedRequest{method: r.Method, url: r.URL.String(), body: string(body)})
}

func (f *fakeWriteSheets) recorded() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedRequest(nil), f.requests...)
}

func (f *fakeWriteSheets) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeWriteSheets) lastOf(t *testing.T, method string) recordedRequest {
	t.Helper()
	for _, r := range f.recorded() {
		if r.method == method {
			return r
		}
	}
	t.Fatalf("no %s request reached the fake; saw %v", method, f.recorded())
	return recordedRequest{}
}

func (f *fakeWriteSheets) setTable(status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tableStatus, f.tableBody = status, body
}

func (f *fakeWriteSheets) setAppend(status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.appendStatus, f.appendBody = status, body
}

func (f *fakeWriteSheets) setUpdate(status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateStatus, f.updateBody = status, body
}

func (f *fakeWriteSheets) factory() gsheet.WriterFactory {
	return func(ctx context.Context, ts oauth2.TokenSource) (*gsheet.Writer, error) {
		return gsheet.NewWriter(ctx, ts, gworkspace.WithBaseURL(f.url))
	}
}

func (f *fakeWriteSheets) readFactory() gsheet.Factory {
	return func(ctx context.Context, ts oauth2.TokenSource) (*gsheet.Client, error) {
		return gsheet.New(ctx, ts, gworkspace.WithBaseURL(f.url))
	}
}

// directUnits is the UnitOfWork the unit tests run under: the fakes hold no transaction, so it only passes the context through and counts the runs.
type directUnits struct {
	runs atomic.Int64
}

func (u *directUnits) Run(ctx context.Context, fn func(ctx context.Context) error) error {
	u.runs.Add(1)
	return fn(ctx)
}

type writeHarness struct {
	snaps    *fakes.SheetSnapshots
	rows     *fakes.SheetRows
	units    *directUnits
	attempts sheet.IdempotencyStore
	srcs     *fakes.SheetSources
	toks     *fakes.SheetTokenSources
	reauth   *fakes.SheetReauthers
	google   *fakeWriteSheets
	unex     *atomic.Int64
	wf       *sheet.WriteWorkflow
	rf       *sheet.ReadWorkflow
}

type writeOpts struct {
	defaultTTL      time.Duration
	maxPayloadBytes int64
}

func newWriteHarness(t *testing.T) *writeHarness {
	t.Helper()
	return newWriteHarnessWith(t, writeOpts{})
}

func newWriteHarnessWith(t *testing.T, opts writeOpts) *writeHarness {
	t.Helper()
	if opts.defaultTTL == 0 {
		opts.defaultTTL = time.Minute
	}
	if opts.maxPayloadBytes == 0 {
		opts.maxPayloadBytes = 1 << 20
	}
	h := &writeHarness{
		snaps:    fakes.NewSheetSnapshots(),
		rows:     fakes.NewSheetRows(),
		units:    &directUnits{},
		attempts: sheet.NewMemoryIdempotencyStore(),
		srcs:     fakes.NewSheetSources(),
		toks:     fakes.NewSheetTokenSources(),
		reauth:   fakes.NewSheetReauthers(),
		google:   newFakeWriteSheets(t),
		unex:     &atomic.Int64{},
	}
	unexpected := func(_ context.Context, _ string, err error, _ ...any) *apperror.AppError {
		h.unex.Add(1)
		return apperror.New("opensheet.unexpected", err.Error(), codes.Internal,
			&apperrorv1.ErrorDetail{Code: "opensheet.unexpected"}).WithCause(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.wf = sheet.NewWriteWorkflow(
		h.snaps, h.rows, h.units, h.attempts, h.srcs, h.toks, h.reauth, h.google.factory(),
		opts.defaultTTL, opts.maxPayloadBytes, log, unexpected,
	)
	h.rf = sheet.NewReadWorkflow(
		h.snaps, h.rows, h.srcs, h.toks, h.reauth, h.google.readFactory(),
		fakeCaps{public: true}, opts.defaultTTL, opts.maxPayloadBytes, log, unexpected,
	)
	return h
}

func (h *writeHarness) seed(t *testing.T, tab string, writable bool) (*sheet.Sheet, uuid.UUID) {
	t.Helper()
	credentialID := uuid.Must(uuid.NewV7())
	sh := &sheet.Sheet{
		ID:            uuid.Must(uuid.NewV7()),
		OrgID:         uuid.Must(uuid.NewV7()),
		ProjectID:     uuid.Must(uuid.NewV7()),
		SpreadsheetID: uuid.Must(uuid.NewV7()),
		Tab:           tab,
		Slug:          "rates",
		Visibility:    sheet.VisibilityKey,
		Writable:      writable,
	}
	h.srcs.Set(sh.SpreadsheetID, sheet.Source{GoogleFileID: "FILE", CredentialID: credentialID})
	return sh, credentialID
}

func decodedURL(t *testing.T, raw string) string {
	t.Helper()
	out, err := url.PathUnescape(raw)
	require.NoError(t, err)
	return out
}

func appErrorOf(t *testing.T, err error) *apperror.AppError {
	t.Helper()
	ae, ok := apperror.AsAppError(err)
	require.True(t, ok, "err must carry a wire envelope: %v", err)
	return ae
}

// SECURITY: the flag is checked before any Google call, so a non-writable sheet
// cannot be probed for existence through timing or error shape.
func TestWriteWorkflow_AppendRefusesANonWritableSheetWithoutCallingGoogle(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", false)

	_, err := h.wf.Append(t.Context(), sh, []any{"x"}, "", "")
	require.Error(t, err)
	assert.True(t, sheet.IsNotWritableError(err), "got %v", err)
	assert.Zero(t, h.google.callCount(), "no request may reach Google for a non-writable sheet")
	assert.Equal(t, http.StatusForbidden, appErrorOf(t, err).HTTPStatus())
}

func TestWriteWorkflow_PatchRowRefusesANonWritableSheetWithoutCallingGoogle(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", false)

	_, err := h.wf.PatchRow(t.Context(), sh, "1", map[string]any{"name": "ada2"}, "")
	require.Error(t, err)
	assert.True(t, sheet.IsNotWritableError(err), "got %v", err)
	assert.Zero(t, h.google.callCount(), "no request may reach Google for a non-writable sheet")
}

func TestWriteWorkflow_AppendSendsTheRowAndReportsTheCount(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)

	n, err := h.wf.Append(t.Context(), sh, []any{"Aston", 1250000}, "", "")
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	req := h.google.lastOf(t, http.MethodPost)
	assert.Contains(t, req.url, "valueInputOption=RAW")
	assert.Contains(t, req.body, "1250000")
	assert.Zero(t, h.unex.Load(), "a clean append reports no incident")
}

func TestWriteWorkflow_AppendResolvesTheFirstTabWhenTheSheetNamesNone(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "", true)

	_, err := h.wf.Append(t.Context(), sh, []any{"x"}, "", "")
	require.NoError(t, err)

	req := h.google.lastOf(t, http.MethodPost)
	assert.Contains(t, decodedURL(t, req.url), "'First'", "the leftmost tab from the metadata call")
}

func TestWriteWorkflow_AppendPurgesTheSnapshot(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}
	h.snaps.Seed(key, sheet.Snapshot{ETag: "etag", Payload: []byte(`[]`)})

	_, err := h.wf.Append(t.Context(), sh, []any{"x"}, "", "")
	require.NoError(t, err)

	_, found, err := h.snaps.Get(t.Context(), key)
	require.NoError(t, err)
	assert.False(t, found, "the snapshot the write invalidated must be gone")
}

// NOTE: Append has no write-through — it reads no header row and Writer.Append returns a count, so there is no row_id or row_index to upsert.
func TestWriteWorkflow_AppendPurgesTheProjection(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}
	h.rows.Seed(key, []sheet.ProjectedRow{{RowID: "1", RowIndex: 0, Data: gsheet.Row{"id": "1", "name": "ada"}}})

	_, err := h.wf.Append(t.Context(), sh, []any{"x"}, "", "")
	require.NoError(t, err)

	assert.Empty(t, h.rows.Projected(key), "the projection the append invalidated must be gone")
}

func TestWriteWorkflow_AppendSurvivesAPurgeFailure(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	h.snaps.PurgeErr = stubError("purge down")

	n, err := h.wf.Append(t.Context(), sh, []any{"x"}, "", "")
	require.NoError(t, err, "the row is already in the sheet; a purge failure cannot un-write it")
	assert.Equal(t, 1, n)
	assert.Equal(t, int64(1), h.unex.Load(), "the purge failure is reported as an incident")
}

// The zero-request assertion is the whole point of a replay and the easiest thing to drop.
func TestWriteWorkflow_AppendReplaysACompletedAttempt(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	k := sheet.IdempotencyKey{SheetID: sh.ID, Tab: sh.Tab, Key: "idem-1"}

	claimed, _, err := h.attempts.Reserve(t.Context(), k, "hash-a", sheet.IdempotencyTTL)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, h.attempts.Complete(t.Context(), k, []byte(`{"appended":1}`), sheet.IdempotencyTTL))

	n, err := h.wf.Append(t.Context(), sh, []any{"x"}, "idem-1", "hash-a")
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Zero(t, h.google.callCount(), "a replay must not reach Google at all")
}

func TestWriteWorkflow_AppendRefusesAnAttemptStillInFlight(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	k := sheet.IdempotencyKey{SheetID: sh.ID, Tab: sh.Tab, Key: "idem-1"}
	_, _, err := h.attempts.Reserve(t.Context(), k, "hash-a", sheet.IdempotencyTTL)
	require.NoError(t, err)

	_, err = h.wf.Append(t.Context(), sh, []any{"x"}, "idem-1", "hash-a")
	require.Error(t, err)
	assert.True(t, sheet.IsWriteInFlightError(err), "got %v", err)
	assert.Equal(t, http.StatusConflict, appErrorOf(t, err).HTTPStatus(),
		"we do not know whether Google applied the row, so success must never be fabricated")
	assert.Zero(t, h.google.callCount(), "the reservation is a lock: the second caller does not write")
}

func TestWriteWorkflow_AppendRejectsAReusedKeyCarryingADifferentBody(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	k := sheet.IdempotencyKey{SheetID: sh.ID, Tab: sh.Tab, Key: "idem-1"}
	_, _, err := h.attempts.Reserve(t.Context(), k, "hash-a", sheet.IdempotencyTTL)
	require.NoError(t, err)
	require.NoError(t, h.attempts.Complete(t.Context(), k, []byte(`{"appended":1}`), sheet.IdempotencyTTL))

	_, err = h.wf.Append(t.Context(), sh, []any{"y"}, "idem-1", "hash-b")
	require.Error(t, err)
	assert.True(t, sheet.IsIdempotencyMismatchError(err), "got %v", err)
	ae := appErrorOf(t, err)
	assert.Equal(t, apperror.CodeSheetIdempotencyMismatch, ae.Code())
	assert.Equal(t, http.StatusUnprocessableEntity, ae.HTTPStatus())
	assert.Zero(t, h.google.callCount(), "a mismatched replay must not write")
}

func TestWriteWorkflow_AppendReleasesTheReservationWhenGoogleFails(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	h.google.setAppend(http.StatusServiceUnavailable, `{"error":{"code":503}}`)

	_, err := h.wf.Append(t.Context(), sh, []any{"x"}, "idem-1", "hash-a")
	require.Error(t, err)

	k := sheet.IdempotencyKey{SheetID: sh.ID, Tab: sh.Tab, Key: "idem-1"}
	claimed, _, rErr := h.attempts.Reserve(t.Context(), k, "hash-a", sheet.IdempotencyTTL)
	require.NoError(t, rErr)
	assert.True(t, claimed, "a failed attempt must be retryable, not answered forever with a stale error")
}

func TestWriteWorkflow_AppendMarksReauthNeededOnAnExpiredGrant(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, credentialID := h.seed(t, "Rates", true)
	h.google.setAppend(http.StatusUnauthorized, `{"error":{"code":401}}`)

	_, err := h.wf.Append(t.Context(), sh, []any{"x"}, "", "")
	require.Error(t, err)
	assert.Equal(t, []uuid.UUID{credentialID}, h.reauth.MarkedIDs(),
		"a revoked grant must surface identically whichever path found it")
}

func TestWriteWorkflow_AppendRejectsARowWithNoCells(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)

	_, err := h.wf.Append(t.Context(), sh, nil, "", "")
	require.Error(t, err)
	assert.True(t, sheet.IsInvalidRowError(err), "got %v", err)
	assert.Equal(t, http.StatusBadRequest, appErrorOf(t, err).HTTPStatus())
	assert.Zero(t, h.google.callCount())
}

func TestWriteWorkflow_PatchRowLocatesTheRowByItsIDColumn(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)

	row, err := h.wf.PatchRow(t.Context(), sh, "2", map[string]any{"name": "bob2"}, "")
	require.NoError(t, err)
	assert.Equal(t, gsheet.Row{"id": "2", "name": "bob2"}, row)

	req := h.google.lastOf(t, http.MethodPut)
	assert.Contains(t, decodedURL(t, req.url), "'Rates'!A3:B3", "the second data slot is sheet row 3")
	assert.Contains(t, req.url, "valueInputOption=RAW")
	assert.Contains(t, req.body, `"bob2"`)
	assert.Contains(t, req.body, `"2"`, "unpatched cells are merged in header order, not blanked")
}

// The one test that pins the i+2 invariant. A loose "row 4" URL substring is easy to satisfy
// by accident, so assert the full range on both sides of the blank row: collapsing it would
// pull "3" onto row 3, which is where "1" already lives.
func TestWriteWorkflow_PatchRowIndexArithmeticSurvivesAnInteriorBlankRow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		id   string
		want string
	}{
		{id: "1", want: "'Rates'!A2:B2"},
		{id: "3", want: "'Rates'!A4:B4"},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			t.Parallel()
			h := newWriteHarness(t)
			sh, _ := h.seed(t, "Rates", true)
			h.google.setTable(http.StatusOK, blankRowTable)

			_, err := h.wf.PatchRow(t.Context(), sh, tc.id, map[string]any{"name": "next"}, "")
			require.NoError(t, err)

			req := h.google.lastOf(t, http.MethodPut)
			assert.Contains(t, decodedURL(t, req.url), tc.want,
				"the interior blank row occupies a slot, so rows[i] is sheet row i+2")
			assert.Contains(t, req.url, strings.ReplaceAll(
				strings.TrimPrefix(tc.want, "'Rates'!"), ":", "%3A"))
		})
	}
}

func TestWriteWorkflow_PatchRowRefusesToPatchTheInteriorBlankRowItSkipped(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	h.google.setTable(http.StatusOK, blankRowTable)

	_, err := h.wf.PatchRow(t.Context(), sh, "", map[string]any{"name": "next"}, "")
	require.Error(t, err)
	assert.True(t, sheet.IsRowNotFoundError(err), "a blank slot carries no id and is not addressable: %v", err)
}

func TestWriteWorkflow_PatchRowReadsFreshRatherThanTheSnapshot(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}
	h.snaps.Seed(key, sheet.Snapshot{ETag: "etag", Payload: []byte(`[{"id":"9","name":"stale"}]`)})
	h.rows.Seed(key, []sheet.ProjectedRow{
		{RowID: "1", RowIndex: 0, Data: gsheet.Row{"id": "1", "name": "ada"}},
		{RowID: "2", RowIndex: 1, Data: gsheet.Row{"id": "2", "name": "bob"}},
	})

	_, err := h.wf.PatchRow(t.Context(), sh, "1", map[string]any{"name": "ada2"}, "")
	require.NoError(t, err)

	got := h.google.lastOf(t, http.MethodGet)
	assert.Contains(t, decodedURL(t, got.url), "/v4/spreadsheets/FILE/values/'Rates'",
		"the row must come from Google, never from the cache")

	snap, found, gErr := h.snaps.Get(t.Context(), key)
	require.NoError(t, gErr)
	require.True(t, found, "the patch rebuilds the snapshot it deliberately ignored")
	assert.NotContains(t, string(snap.Payload), "stale", "the seeded payload must not survive the write")
}

func TestWriteWorkflow_PatchRowRejectsAnEmptyPatch(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)

	_, err := h.wf.PatchRow(t.Context(), sh, "1", nil, "")
	require.Error(t, err)
	assert.True(t, sheet.IsInvalidRowError(err), "got %v", err)
	assert.Zero(t, h.google.callCount())
}

func TestWriteWorkflow_PatchRowErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		table      string
		id         string
		patch      map[string]any
		is         func(error) bool
		wantStatus int
	}{
		{
			name:       "no id header",
			table:      `{"values":[["name","qty"],["ada","3"]]}`,
			id:         "1",
			patch:      map[string]any{"name": "ada2"},
			is:         sheet.IsNoIDColumnError,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "empty tab has no id header",
			table:      `{"values":[]}`,
			id:         "1",
			patch:      map[string]any{"name": "ada2"},
			is:         sheet.IsNoIDColumnError,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "two id headers",
			table:      `{"values":[[" ID ","name","id"],["1","ada","x"]]}`,
			id:         "1",
			patch:      map[string]any{"name": "ada2"},
			is:         sheet.IsAmbiguousIDColumnError,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "duplicate id value",
			table:      `{"values":[["id","name"],["1","ada"],["1","bob"]]}`,
			id:         "1",
			patch:      map[string]any{"name": "ada2"},
			is:         sheet.IsDuplicateIDError,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "unknown id",
			table:      idNameTable,
			id:         "99",
			patch:      map[string]any{"name": "nobody"},
			is:         sheet.IsRowNotFoundError,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "unknown column in patch",
			table:      idNameTable,
			id:         "1",
			patch:      map[string]any{"nope": "x"},
			is:         sheet.IsUnknownColumnError,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "patch targets the id column",
			table:      idNameTable,
			id:         "1",
			patch:      map[string]any{"id": "7"},
			is:         sheet.IsReadOnlyColumnError,
			wantStatus: http.StatusBadRequest,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newWriteHarness(t)
			sh, _ := h.seed(t, "Rates", true)
			h.google.setTable(http.StatusOK, tc.table)

			_, err := h.wf.PatchRow(t.Context(), sh, tc.id, tc.patch, "")
			require.Error(t, err)
			assert.True(t, tc.is(err), "got %v", err)
			assert.Equal(t, tc.wantStatus, appErrorOf(t, err).HTTPStatus())

			for _, r := range h.google.recorded() {
				assert.NotEqual(t, http.MethodPut, r.method, "a refused patch must not write")
			}
		})
	}
}

func TestWriteWorkflow_PatchRowStringifiesNonStringPatchValues(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)

	row, err := h.wf.PatchRow(t.Context(), sh, "1", map[string]any{"name": 1250000}, "")
	require.NoError(t, err)
	assert.Equal(t, "1250000", row["name"])
	assert.NotContains(t, h.google.lastOf(t, http.MethodPut).body, `"1250000"`,
		"a numeric cell must reach Google as a JSON number")
}

type stubError string

func (e stubError) Error() string { return string(e) }

// The GET path keys its rows with gsheet's header normalizer, so PATCH must key its response the same
// way or a client diffing the two sees different keys for the same row.
func TestWriteWorkflow_PatchRowKeysTheResponseLikeTheReadPath(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	h.google.setTable(http.StatusOK, `{"values":[["id","","name","name"],["1","x","ada","dup"]]}`)

	row, err := h.wf.PatchRow(t.Context(), sh, "1", map[string]any{"name": "ada2"}, "")
	require.NoError(t, err)

	names, _ := gsheet.NormalizeHeaders([]string{"id", "", "name", "name"})
	assert.Equal(t, []string{"id", "col_2", "name", "name_2"}, names)
	assert.Equal(t, gsheet.Row{"id": "1", "col_2": "x", "name": "ada2", "name_2": "dup"}, row)
}

// The zero-refetch assertion is the whole point: a test that permits a refetch would pass on the
// purge-only behaviour this replaces.
func TestWriteWorkflow_PatchRow_ReadYourWriteWithNoRefetch(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}
	h.rows.Seed(key, []sheet.ProjectedRow{
		{RowID: "1", RowIndex: 0, Data: gsheet.Row{"id": "1", "name": "ada"}},
		{RowID: "2", RowIndex: 1, Data: gsheet.Row{"id": "2", "name": "bob"}},
	})

	row, err := h.wf.PatchRow(t.Context(), sh, "1", map[string]any{"name": "ada2"}, "")
	require.NoError(t, err)
	require.Equal(t, gsheet.Row{"id": "1", "name": "ada2"}, row)

	live, err := h.rows.ListLive(t.Context(), key)
	require.NoError(t, err)
	assert.Equal(t, []sheet.ProjectedRow{
		{RowID: "1", RowIndex: 0, Data: gsheet.Row{"id": "1", "name": "ada2"}},
		{RowID: "2", RowIndex: 1, Data: gsheet.Row{"id": "2", "name": "bob"}},
	}, live, "the patched row must be readable from the projection")

	afterWrite := h.google.callCount()
	got, err := h.rf.Rows(t.Context(), sh)
	require.NoError(t, err)
	assert.Equal(t, []gsheet.Row{
		{"id": "1", "name": "ada2"},
		{"id": "2", "name": "bob"},
	}, got.Values, "the whole-tab read must carry the patched value")
	assert.Equal(t, afterWrite, h.google.callCount(),
		"the read must be answered from the rebuilt snapshot, never from a refetch")
	assert.Equal(t, etagOf(t, `[{"id":"1","name":"ada2"},{"id":"2","name":"bob"}]`), got.ETag,
		"the rebuilt ETag must be what a fresh fetch of the same rows would hash")
	assert.Zero(t, h.unex.Load(), "a clean write-through reports no incident")
}

// NOTE: LockSheet is enroll-or-own, so a patch that does not run inside one transaction takes no lock at all.
func TestWriteWorkflow_PatchRow_RunsTheLockAndTheProjectionInOneUnitOfWork(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)

	_, err := h.wf.PatchRow(t.Context(), sh, "1", map[string]any{"name": "ada2"}, "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), h.units.runs.Load(),
		"the Google read, the Google write and the projection write share one transaction")
}

// SECURITY: the projection filters tombstones out of the payload, so a patchable deleted_at would let a
// client with sheets:write make a row vanish from the tab read by patching a field. The guard compares
// column indexes, not folded names, so a "Deleted At" header cannot slip a delete through as a field.
func TestWriteWorkflow_PatchRow_RefusesToWriteTheDeletedAtColumn(t *testing.T) {
	t.Parallel()
	for _, header := range []string{"deleted_at", "Deleted At"} {
		t.Run(header, func(t *testing.T) {
			t.Parallel()
			h := newWriteHarness(t)
			sh, _ := h.seed(t, "Rates", true)
			h.google.setTable(http.StatusOK, `{"values":[["id","name","`+header+`"],["1","ada",""]]}`)

			_, err := h.wf.PatchRow(t.Context(), sh, "1", map[string]any{header: "2026-09-10T04:11:09Z"}, "")
			require.Error(t, err)
			assert.True(t, sheet.IsReadOnlyColumnError(err), "got %T: %v", err, err)
			assert.Equal(t, http.StatusBadRequest, appErrorOf(t, err).HTTPStatus())
			for _, r := range h.google.recorded() {
				assert.NotEqual(t, http.MethodPut, r.method, "delete must not be reachable as a field write")
			}
		})
	}
}

func TestWriteWorkflow_PatchRow_KeepsATombstoneItDidNotWrite(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}
	deletedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	h.google.setTable(http.StatusOK, `{"values":[["id","name","deleted_at"],["1","ada","2026-01-02T03:04:05Z"]]}`)
	h.rows.Seed(key, []sheet.ProjectedRow{
		{RowID: "1", RowIndex: 0, Data: gsheet.Row{"id": "1", "name": "ada"}, DeletedAt: &deletedAt},
	})

	row, err := h.wf.PatchRow(t.Context(), sh, "1", map[string]any{"name": "ada2"}, "")
	require.NoError(t, err)
	assert.Equal(t, gsheet.Row{"id": "1", "name": "ada2"}, row, "deleted_at is stripped from the served row")
	assert.Contains(t, h.google.lastOf(t, http.MethodPut).body, "2026-01-02T03:04:05Z",
		"the tombstone cell is merged back in, never blanked by a patch that did not name it")

	live, err := h.rows.ListLive(t.Context(), key)
	require.NoError(t, err)
	assert.Empty(t, live, "the patched row stays tombstoned in the projection")

	snap, found, err := h.snaps.Get(t.Context(), key)
	require.NoError(t, err)
	require.True(t, found)
	assert.JSONEq(t, `[]`, string(snap.Payload), "the rebuilt payload holds live rows only")
}

func TestWriteWorkflow_PatchRow_ARebuildOverTheLimitPurgesInsteadOfStoring(t *testing.T) {
	t.Parallel()
	h := newWriteHarnessWith(t, writeOpts{maxPayloadBytes: 8})
	sh, _ := h.seed(t, "Rates", true)
	key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}
	h.snaps.Seed(key, sheet.Snapshot{ETag: "etag", Payload: []byte(`[]`)})
	h.rows.Seed(key, []sheet.ProjectedRow{
		{RowID: "1", RowIndex: 0, Data: gsheet.Row{"id": "1", "name": "ada"}},
		{RowID: "2", RowIndex: 1, Data: gsheet.Row{"id": "2", "name": "bob"}},
	})

	row, err := h.wf.PatchRow(t.Context(), sh, "1", map[string]any{"name": "ada2"}, "")
	require.NoError(t, err, "the row is already in the sheet; an oversized rebuild cannot un-write it")
	assert.Equal(t, gsheet.Row{"id": "1", "name": "ada2"}, row)

	_, found, err := h.snaps.Get(t.Context(), key)
	require.NoError(t, err)
	assert.False(t, found, "a rebuild over the limit purges the snapshot so the next read refetches")

	live, err := h.rows.ListLive(t.Context(), key)
	require.NoError(t, err)
	require.Len(t, live, 2)
	assert.Equal(t, "ada2", live[0].Data["name"], "the projection keeps the write the snapshot could not hold")
	assert.Zero(t, h.unex.Load(), "the payload limit is an expected outcome, not an incident")
}

func TestWriteWorkflow_PatchRow_AFailedSnapshotPutLeavesTheProjectionAlone(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}
	h.rows.Seed(key, []sheet.ProjectedRow{
		{RowID: "1", RowIndex: 0, Data: gsheet.Row{"id": "1", "name": "ada"}},
		{RowID: "2", RowIndex: 1, Data: gsheet.Row{"id": "2", "name": "bob"}},
	})
	h.snaps.PutErr = stubError("cache down")

	_, err := h.wf.PatchRow(t.Context(), sh, "1", map[string]any{"name": "ada2"}, "")
	require.NoError(t, err)

	live, err := h.rows.ListLive(t.Context(), key)
	require.NoError(t, err)
	require.Len(t, live, 2)
	assert.Equal(t, "ada2", live[0].Data["name"],
		"the projection is the read-your-write surface, so a snapshot failure must not purge it")
	assert.Equal(t, int64(1), h.unex.Load(), "the Put failure is reported as an incident")
}

func TestWriteWorkflow_PatchRow_APartialProjectionPurgesRatherThanCachingATruncatedBody(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}
	h.snaps.Seed(key, sheet.Snapshot{ETag: "etag", Payload: []byte(`[]`)})

	_, err := h.wf.PatchRow(t.Context(), sh, "1", map[string]any{"name": "ada2"}, "")
	require.NoError(t, err)

	live, err := h.rows.ListLive(t.Context(), key)
	require.NoError(t, err)
	require.Len(t, live, 1, "the write-through still lands")

	_, found, err := h.snaps.Get(t.Context(), key)
	require.NoError(t, err)
	assert.False(t, found,
		"the tab holds two rows and the projection one, so the rebuild must purge rather than cache a truncated body")
	assert.Zero(t, h.unex.Load())
}

func TestWriteWorkflow_PatchRow_RefusesADuplicateDeletedAtColumn(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	h.google.setTable(http.StatusOK, `{"values":[["id","deleted_at","Deleted At"],["1","",""]]}`)

	_, err := h.wf.PatchRow(t.Context(), sh, "1", map[string]any{"id": "x"}, "")
	require.Error(t, err)
	assert.True(t, sheet.IsDuplicateColumnError(err), "got %T: %v", err, err)
	assert.Equal(t, http.StatusConflict, appErrorOf(t, err).HTTPStatus())
	for _, r := range h.google.recorded() {
		assert.NotEqual(t, http.MethodPut, r.method,
			"an unaddressable deleted_at column cannot be projected, so the patch is refused before the write")
	}
}

const (
	idNameNoteTable = `{"values":[["id","name","note"],["1","ada","keep"]]}`
	tombstonedTable = `{"values":[["id","name","deleted_at"],["1","ada",""],["z","zed","2026-01-02T03:04:05Z"]]}`
	softDeleteTable = `{"values":[["id","name","deleted_at"],["1","ada",""]]}`
)

func (h *writeHarness) seedContract(sh *sheet.Sheet) {
	h.rows.SeedContract(sh.ID, sheet.ContractState{OK: true})
}

// SECURITY: the flag is checked before any Google call, so a non-writable sheet
// cannot be probed for existence through timing or error shape.
func TestWriteWorkflow_CreateRowRefusesANonWritableSheetWithoutCallingGoogle(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", false)
	h.seedContract(sh)

	_, err := h.wf.CreateRow(t.Context(), sh, map[string]any{"name": "cyd"}, "", "")
	require.Error(t, err)
	assert.True(t, sheet.IsNotWritableError(err), "got %v", err)
	assert.Equal(t, http.StatusForbidden, appErrorOf(t, err).HTTPStatus())
	assert.Zero(t, h.google.callCount(), "no request may reach Google for a non-writable sheet")
}

// SECURITY: as above — the same gate, in the same position, on the replace route.
func TestWriteWorkflow_ReplaceRowRefusesANonWritableSheetWithoutCallingGoogle(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", false)
	h.seedContract(sh)

	_, err := h.wf.ReplaceRow(t.Context(), sh, "1", map[string]any{"name": "ada2"}, "")
	require.Error(t, err)
	assert.True(t, sheet.IsNotWritableError(err), "got %v", err)
	assert.Equal(t, http.StatusForbidden, appErrorOf(t, err).HTTPStatus())
	assert.Zero(t, h.google.callCount(), "no request may reach Google for a non-writable sheet")
}

func TestWriteWorkflow_CreateRow_GeneratesAnIDAndAppendsInHeaderOrder(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)

	row, err := h.wf.CreateRow(t.Context(), sh, map[string]any{"name": "cyd"}, "", "")
	require.NoError(t, err)
	assert.Len(t, row.ID, 21, "an id the body omits is a generated 21-character nanoid")
	assert.Equal(t, gsheet.Row{"id": row.ID, "name": "cyd"}, row.Data)
	assert.NotEmpty(t, row.ETag, "a created row carries the tag a conditional read revalidates against")

	req := h.google.lastOf(t, http.MethodPost)
	assert.Contains(t, req.url, "valueInputOption=RAW")
	assert.Contains(t, req.body, `["`+row.ID+`","cyd"]`, "cells are built in header order")
	assert.Zero(t, h.unex.Load(), "a clean create reports no incident")
}

func TestWriteWorkflow_CreateRow_HonoursASuppliedIDAndWritesOmittedColumnsEmpty(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	h.google.setTable(http.StatusOK, idNameNoteTable)

	row, err := h.wf.CreateRow(t.Context(), sh, map[string]any{"id": "my-own", "name": "zed"}, "", "")
	require.NoError(t, err)
	assert.Equal(t, "my-own", row.ID, "a supplied id is honoured verbatim")
	assert.Equal(t, gsheet.Row{"id": "my-own", "name": "zed", "note": ""}, row.Data)
	assert.Contains(t, h.google.lastOf(t, http.MethodPost).body, `["my-own","zed",""]`,
		"a column the body omits is written empty, as rowOf keys it")
}

func TestWriteWorkflow_CreateRow_RefusesAnIDTheTabAlreadyHolds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		table string
		id    string
	}{
		{name: "live row", table: idNameTable, id: "1"},
		{name: "tombstoned row", table: tombstonedTable, id: "z"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newWriteHarness(t)
			sh, _ := h.seed(t, "Rates", true)
			h.google.setTable(http.StatusOK, tc.table)

			_, err := h.wf.CreateRow(t.Context(), sh, map[string]any{"id": tc.id, "name": "cyd"}, "", "")
			require.Error(t, err)
			assert.True(t, sheet.IsDuplicateIDError(err), "got %T: %v", err, err)
			assert.Equal(t, http.StatusConflict, appErrorOf(t, err).HTTPStatus())
			for _, r := range h.google.recorded() {
				assert.NotEqual(t, http.MethodPost, r.method,
					"the duplicate check reads the tab, so a tombstoned id is refused before the append")
			}
		})
	}
}

func TestWriteWorkflow_CreateRow_RefusesTheDeletedAtColumn(t *testing.T) {
	t.Parallel()
	for _, header := range []string{"deleted_at", "Deleted At"} {
		t.Run(header, func(t *testing.T) {
			t.Parallel()
			h := newWriteHarness(t)
			sh, _ := h.seed(t, "Rates", true)
			h.google.setTable(http.StatusOK, `{"values":[["id","name","`+header+`"],["1","ada",""]]}`)

			_, err := h.wf.CreateRow(t.Context(), sh, map[string]any{"name": "cyd", header: "2026-09-10T04:11:09Z"}, "", "")
			require.Error(t, err)
			assert.True(t, sheet.IsReadOnlyColumnError(err), "got %T: %v", err, err)
			assert.Equal(t, http.StatusBadRequest, appErrorOf(t, err).HTTPStatus())
			for _, r := range h.google.recorded() {
				assert.NotEqual(t, http.MethodPost, r.method, "a row cannot be created already deleted")
			}
		})
	}
}

func TestWriteWorkflow_CreateRow_RefusesAnUnknownColumn(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)

	_, err := h.wf.CreateRow(t.Context(), sh, map[string]any{"nope": "x"}, "", "")
	require.Error(t, err)
	assert.True(t, sheet.IsUnknownColumnError(err), "got %T: %v", err, err)
	assert.Equal(t, http.StatusBadRequest, appErrorOf(t, err).HTTPStatus(),
		"SHT014 carries no status override, so an unknown column is 400 here as it is on a patch")
	for _, r := range h.google.recorded() {
		assert.NotEqual(t, http.MethodPost, r.method, "an unknown key is never silently dropped")
	}
}

func TestWriteWorkflow_CreateRow_RefusesABlankSuppliedID(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)

	_, err := h.wf.CreateRow(t.Context(), sh, map[string]any{"id": "  ", "name": "cyd"}, "", "")
	require.Error(t, err)
	assert.True(t, sheet.IsInvalidRowError(err), "got %T: %v", err, err)
	for _, r := range h.google.recorded() {
		assert.NotEqual(t, http.MethodPost, r.method,
			"a blank id would break the tab contract on the next refresh")
	}
}

// The zero-refetch assertion is the whole point: a test that permits a refetch would pass on the
// purge-and-refetch behaviour keyed create replaces, and would prove nothing about the append position.
func TestWriteWorkflow_CreateRow_ReadYourWriteWithNoRefetch(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	h.seedContract(sh)
	key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}
	h.rows.Seed(key, []sheet.ProjectedRow{
		{RowID: "1", RowIndex: 0, Data: gsheet.Row{"id": "1", "name": "ada"}},
		{RowID: "2", RowIndex: 1, Data: gsheet.Row{"id": "2", "name": "bob"}},
	})

	row, err := h.wf.CreateRow(t.Context(), sh, map[string]any{"name": "cyd"}, "", "")
	require.NoError(t, err)
	afterWrite := h.google.callCount()

	got, err := h.rf.RowByID(t.Context(), sh, row.ID)
	require.NoError(t, err)
	assert.Equal(t, row.Data, got.Data, "the created row must be readable by its id")
	assert.Equal(t, row.ETag, got.ETag, "a written row and a read row must tag the same bytes identically")
	assert.Equal(t, afterWrite, h.google.callCount(),
		"the read must be answered from the projection, never from a refetch")

	live, lErr := h.rows.ListLive(t.Context(), key)
	require.NoError(t, lErr)
	require.Len(t, live, 3)
	assert.Equal(t, 2, live[2].RowIndex,
		"row_index is the append's start row less two, not a position a refetch discovered")
	assert.Zero(t, h.unex.Load(), "a clean write-through reports no incident")
}

func TestWriteWorkflow_CreateRow_RunsTheLockAndTheProjectionInOneUnitOfWork(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)

	_, err := h.wf.CreateRow(t.Context(), sh, map[string]any{"name": "cyd"}, "", "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), h.units.runs.Load(),
		"the Google read, the append and the projection write share one transaction")
}

// The clear-versus-preserve pair is the only thing distinguishing PUT from PATCH.
func TestWriteWorkflow_ReplaceRow_ClearsAnOmittedColumnWhereAPatchLeavesItAlone(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	h.seedContract(sh)
	h.google.setTable(http.StatusOK, idNameNoteTable)

	replaced, err := h.wf.ReplaceRow(t.Context(), sh, "1", map[string]any{"name": "ada2"}, "")
	require.NoError(t, err)
	assert.Equal(t, gsheet.Row{"id": "1", "name": "ada2", "note": ""}, replaced.Data,
		"a known column absent from the body is cleared")
	put := h.google.lastOf(t, http.MethodPut)
	assert.Contains(t, put.url, "valueInputOption=RAW")
	assert.Contains(t, decodedURL(t, put.url), "'Rates'!A2:C2", "the row keeps its position")
	assert.Contains(t, put.body, `["1","ada2",""]`)

	patched := newWriteHarness(t)
	patchSheet, _ := patched.seed(t, "Rates", true)
	patched.google.setTable(http.StatusOK, idNameNoteTable)

	row, err := patched.wf.PatchRow(t.Context(), patchSheet, "1", map[string]any{"name": "ada2"}, "")
	require.NoError(t, err)
	assert.Equal(t, gsheet.Row{"id": "1", "name": "ada2", "note": "keep"}, row,
		"the same body through PATCH leaves the omitted column untouched")
}

// A body id equal to the path id is permitted, and mergeRow's id guard refused exactly that.
func TestWriteWorkflow_ReplaceRow_AcceptsABodyIDEqualToThePathID(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	h.seedContract(sh)

	row, err := h.wf.ReplaceRow(t.Context(), sh, "1", map[string]any{"id": "1", "name": "ada2"}, "")
	require.NoError(t, err)
	assert.Equal(t, gsheet.Row{"id": "1", "name": "ada2"}, row.Data)
	assert.Contains(t, h.google.lastOf(t, http.MethodPut).body, `["1","ada2"]`)
}

func TestWriteWorkflow_ReplaceRow_RefusesABodyIDThatRenamesTheRow(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	h.seedContract(sh)

	_, err := h.wf.ReplaceRow(t.Context(), sh, "1", map[string]any{"id": "2", "name": "ada2"}, "")
	require.Error(t, err)
	assert.True(t, sheet.IsIDMismatchError(err), "got %T: %v", err, err)
	ae := appErrorOf(t, err)
	assert.Equal(t, apperror.CodeSheetIDMismatch, ae.Code())
	assert.Equal(t, http.StatusUnprocessableEntity, ae.HTTPStatus())
	for _, r := range h.google.recorded() {
		assert.NotEqual(t, http.MethodPut, r.method, "renaming a row is a delete plus a create, not a replace")
	}
}

func TestWriteWorkflow_ReplaceRow_RefusesTheDeletedAtColumn(t *testing.T) {
	t.Parallel()
	for _, header := range []string{"deleted_at", "Deleted At"} {
		t.Run(header, func(t *testing.T) {
			t.Parallel()
			h := newWriteHarness(t)
			sh, _ := h.seed(t, "Rates", true)
			h.seedContract(sh)
			h.google.setTable(http.StatusOK, `{"values":[["id","name","`+header+`"],["1","ada",""]]}`)

			_, err := h.wf.ReplaceRow(t.Context(), sh, "1", map[string]any{header: "2026-09-10T04:11:09Z"}, "")
			require.Error(t, err)
			assert.True(t, sheet.IsReadOnlyColumnError(err), "got %T: %v", err, err)
			assert.Equal(t, http.StatusBadRequest, appErrorOf(t, err).HTTPStatus())
			for _, r := range h.google.recorded() {
				assert.NotEqual(t, http.MethodPut, r.method, "delete must not be reachable as a field write")
			}
		})
	}
}

func TestWriteWorkflow_ReplaceRow_RefusesAnAbsentOrTombstonedRow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		table string
		id    string
	}{
		{name: "unknown id", table: idNameTable, id: "99"},
		{name: "tombstoned id", table: tombstonedTable, id: "z"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newWriteHarness(t)
			sh, _ := h.seed(t, "Rates", true)
			h.seedContract(sh)
			h.google.setTable(http.StatusOK, tc.table)

			_, err := h.wf.ReplaceRow(t.Context(), sh, tc.id, map[string]any{"name": "nobody"}, "")
			require.Error(t, err)
			assert.True(t, sheet.IsRowNotFoundError(err), "got %T: %v", err, err)
			assert.Equal(t, http.StatusNotFound, appErrorOf(t, err).HTTPStatus())
			for _, r := range h.google.recorded() {
				assert.NotEqual(t, http.MethodPut, r.method, "a replace never creates and never resurrects")
			}
		})
	}
}

func TestWriteWorkflow_ReplaceRow_RefusesADriftedContractWithoutCallingGoogle(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	h.rows.SeedContract(sh.ID, sheet.ContractState{OK: false, Reason: "duplicate id"})

	_, err := h.wf.ReplaceRow(t.Context(), sh, "1", map[string]any{"name": "ada2"}, "")
	require.Error(t, err)
	assert.True(t, sheet.IsContractViolationError(err), "got %T: %v", err, err)
	assert.Equal(t, http.StatusConflict, appErrorOf(t, err).HTTPStatus())
	assert.Zero(t, h.google.callCount(), "a sheet whose rows cannot be addressed by id is refused before any read")
}

func TestWriteWorkflow_ReplaceRow_KeepsTheSoftDeleteColumnEmptyOnALiveRow(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	h.seedContract(sh)
	h.google.setTable(http.StatusOK, softDeleteTable)
	key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}
	h.rows.Seed(key, []sheet.ProjectedRow{
		{RowID: "1", RowIndex: 0, Data: gsheet.Row{"id": "1", "name": "ada"}},
	})

	row, err := h.wf.ReplaceRow(t.Context(), sh, "1", map[string]any{"name": "ada2"}, "")
	require.NoError(t, err)
	assert.Equal(t, gsheet.Row{"id": "1", "name": "ada2"}, row.Data, "deleted_at is stripped from the served row")

	live, err := h.rows.ListLive(t.Context(), key)
	require.NoError(t, err)
	require.Len(t, live, 1, "clearing an empty tombstone cell cannot delete the row")
	assert.Equal(t, "ada2", live[0].Data["name"])
}

func TestWriteWorkflow_ReplaceRow_ReadYourWriteWithNoRefetch(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	h.seedContract(sh)
	key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}
	h.google.setTable(http.StatusOK, idNameNoteTable)
	h.rows.Seed(key, []sheet.ProjectedRow{
		{RowID: "1", RowIndex: 0, Data: gsheet.Row{"id": "1", "name": "ada", "note": "keep"}},
	})

	row, err := h.wf.ReplaceRow(t.Context(), sh, "1", map[string]any{"name": "ada2"}, "")
	require.NoError(t, err)
	afterWrite := h.google.callCount()

	got, err := h.rf.RowByID(t.Context(), sh, "1")
	require.NoError(t, err)
	assert.Equal(t, row.Data, got.Data, "the replaced row must come back with its cleared column")
	assert.Equal(t, row.ETag, got.ETag, "the replace must hand back the tag a read of the same row computes")
	assert.Equal(t, afterWrite, h.google.callCount(), "the read must be answered from the projection")
}

const appendedThree = `{"updates":{"updatedRows":3,"updatedRange":"'Rates'!A4:B6"}}`

func appendCount(f *fakeWriteSheets) int {
	n := 0
	for _, r := range f.recorded() {
		if r.method == http.MethodPost {
			n++
		}
	}
	return n
}

func projectedJSON(t *testing.T, rows []sheet.ProjectedRow) string {
	t.Helper()
	raw, err := json.Marshal(rows)
	require.NoError(t, err)
	return string(raw)
}

// SECURITY: the flag is checked before any Google call, so a non-writable sheet
// cannot be probed for existence through timing or error shape.
func TestWriteWorkflow_CreateRowsRefusesANonWritableSheetWithoutCallingGoogle(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", false)

	_, err := h.wf.CreateRows(t.Context(), sh, []map[string]any{{"name": "cyd"}}, "", "")
	require.Error(t, err)
	assert.True(t, sheet.IsNotWritableError(err), "got %v", err)
	assert.Equal(t, http.StatusForbidden, appErrorOf(t, err).HTTPStatus())
	assert.Zero(t, h.google.callCount(), "no request may reach Google for a non-writable sheet")
}

func TestWriteWorkflow_CreateRows_AppendsEveryRowInOneCallAndProjectsSequentialIndexes(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	h.seedContract(sh)
	h.google.setAppend(http.StatusOK, appendedThree)
	key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}
	h.rows.Seed(key, []sheet.ProjectedRow{
		{RowID: "1", RowIndex: 0, Data: gsheet.Row{"id": "1", "name": "ada"}},
		{RowID: "2", RowIndex: 1, Data: gsheet.Row{"id": "2", "name": "bob"}},
	})

	ids, err := h.wf.CreateRows(t.Context(), sh, []map[string]any{
		{"name": "cyd"},
		{"id": "mine", "name": "dee"},
		{"name": "eve"},
	}, "", "")
	require.NoError(t, err)
	require.Len(t, ids, 3)
	assert.Equal(t, "mine", ids[1], "the ids come back in request order")
	assert.Len(t, ids[0], 21, "an id the row omits is a generated 21-character nanoid")

	assert.Equal(t, 1, appendCount(h.google), "N rows are written by one append, never one append per row")
	req := h.google.lastOf(t, http.MethodPost)
	assert.Contains(t, req.url, "valueInputOption=RAW")
	assert.Contains(t, req.body, `["`+ids[0]+`","cyd"]`)
	assert.Contains(t, req.body, `["mine","dee"]`)
	assert.Contains(t, req.body, `["`+ids[2]+`","eve"]`)

	live, lErr := h.rows.ListLive(t.Context(), key)
	require.NoError(t, lErr)
	require.Len(t, live, 5)
	assert.Equal(t, 2, live[2].RowIndex, "row_index is the block's start row less two")
	assert.Equal(t, 3, live[3].RowIndex, "each later row follows from the start row")
	assert.Equal(t, 4, live[4].RowIndex)
	assert.Equal(t, ids, []string{live[2].RowID, live[3].RowID, live[4].RowID})

	afterWrite := h.google.callCount()
	got, rErr := h.rf.RowByID(t.Context(), sh, "mine")
	require.NoError(t, rErr)
	assert.Equal(t, gsheet.Row{"id": "mine", "name": "dee"}, got.Data)
	assert.Equal(t, afterWrite, h.google.callCount(), "a batched row is readable from the projection with no refetch")
	assert.Zero(t, h.unex.Load(), "a clean batch reports no incident")
}

// Asserting only that the response was an error would pass on a half-applied batch: the whole batch is
// validated before the append, so a bad last row must leave Google and the projection untouched.
func TestBatchCreate_InvalidLastRowWritesNothing(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	h.google.setAppend(http.StatusOK, appendedThree)
	key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}
	h.rows.Seed(key, []sheet.ProjectedRow{
		{RowID: "1", RowIndex: 0, Data: gsheet.Row{"id": "1", "name": "ada"}},
		{RowID: "2", RowIndex: 1, Data: gsheet.Row{"id": "2", "name": "bob"}},
	})
	before := projectedJSON(t, h.rows.Projected(key))
	beforeGen := h.rows.Generation(sh.ID)

	_, err := h.wf.CreateRows(t.Context(), sh, []map[string]any{
		{"name": "cyd"},
		{"name": "dee"},
		{"nope": "eve"},
	}, "", "")
	require.Error(t, err)
	assert.True(t, sheet.IsUnknownColumnError(err), "got %T: %v", err, err)

	assert.Zero(t, appendCount(h.google), "not one write may reach Google when any row of the batch is invalid")
	assert.Equal(t, before, projectedJSON(t, h.rows.Projected(key)), "the projection must be byte-identical")
	assert.Equal(t, beforeGen, h.rows.Generation(sh.ID), "a refused batch bumps no generation")
}

func TestWriteWorkflow_CreateRows_RefusesADuplicateIDWithinTheBatch(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)

	_, err := h.wf.CreateRows(t.Context(), sh, []map[string]any{
		{"id": "same", "name": "cyd"},
		{"id": "same", "name": "dee"},
	}, "", "")
	require.Error(t, err)
	assert.True(t, sheet.IsDuplicateIDError(err), "got %T: %v", err, err)
	assert.Equal(t, http.StatusConflict, appErrorOf(t, err).HTTPStatus())
	assert.Zero(t, appendCount(h.google), "two rows of one batch cannot claim one id")
}

func TestWriteWorkflow_CreateRows_RefusesAnIDTheTabAlreadyHolds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		table string
		id    string
	}{
		{name: "live row", table: idNameTable, id: "1"},
		{name: "tombstoned row", table: tombstonedTable, id: "z"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newWriteHarness(t)
			sh, _ := h.seed(t, "Rates", true)
			h.google.setTable(http.StatusOK, tc.table)

			_, err := h.wf.CreateRows(t.Context(), sh, []map[string]any{
				{"name": "cyd"},
				{"id": tc.id, "name": "dee"},
			}, "", "")
			require.Error(t, err)
			assert.True(t, sheet.IsDuplicateIDError(err), "got %T: %v", err, err)
			assert.Equal(t, http.StatusConflict, appErrorOf(t, err).HTTPStatus())
			assert.Zero(t, appendCount(h.google),
				"the duplicate check reads the tab, so a tombstoned id is refused before the append")
		})
	}
}

func TestWriteWorkflow_CreateRows_RefusesTheDeletedAtColumn(t *testing.T) {
	t.Parallel()
	for _, header := range []string{"deleted_at", "Deleted At"} {
		t.Run(header, func(t *testing.T) {
			t.Parallel()
			h := newWriteHarness(t)
			sh, _ := h.seed(t, "Rates", true)
			h.google.setTable(http.StatusOK, `{"values":[["id","name","`+header+`"],["1","ada",""]]}`)

			_, err := h.wf.CreateRows(t.Context(), sh, []map[string]any{
				{"name": "cyd"},
				{"name": "dee", header: "2026-09-10T04:11:09Z"},
			}, "", "")
			require.Error(t, err)
			assert.True(t, sheet.IsReadOnlyColumnError(err), "got %T: %v", err, err)
			assert.Equal(t, http.StatusBadRequest, appErrorOf(t, err).HTTPStatus())
			assert.Zero(t, appendCount(h.google), "a row cannot be created already deleted")
		})
	}
}

func TestWriteWorkflow_CreateRows_RefusesAnUnknownColumnAndABlankSuppliedID(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		batch []map[string]any
		is    func(error) bool
	}{
		"unknown column": {
			batch: []map[string]any{{"nope": "x"}},
			is:    sheet.IsUnknownColumnError,
		},
		"blank id": {
			batch: []map[string]any{{"id": "  ", "name": "cyd"}},
			is:    sheet.IsInvalidRowError,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newWriteHarness(t)
			sh, _ := h.seed(t, "Rates", true)

			_, err := h.wf.CreateRows(t.Context(), sh, tc.batch, "", "")
			require.Error(t, err)
			assert.True(t, tc.is(err), "got %T: %v", err, err)
			assert.Equal(t, http.StatusBadRequest, appErrorOf(t, err).HTTPStatus())
			assert.Zero(t, appendCount(h.google))
		})
	}
}

func TestWriteWorkflow_CreateRows_WritesOmittedColumnsEmpty(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	h.google.setTable(http.StatusOK, idNameNoteTable)
	h.google.setAppend(http.StatusOK, `{"updates":{"updatedRows":2,"updatedRange":"'Rates'!A3:C4"}}`)

	ids, err := h.wf.CreateRows(t.Context(), sh, []map[string]any{
		{"id": "one", "name": "cyd"},
		{"id": "two", "note": "kept"},
	}, "", "")
	require.NoError(t, err)
	assert.Equal(t, []string{"one", "two"}, ids)
	body := h.google.lastOf(t, http.MethodPost).body
	assert.Contains(t, body, `["one","cyd",""]`, "a column the row omits is written empty")
	assert.Contains(t, body, `["two","","kept"]`)
}

func TestWriteWorkflow_CreateRows_RefusesAnEmptyBatchAndOneOverTheRowLimit(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)

	_, err := h.wf.CreateRows(t.Context(), sh, nil, "", "")
	require.Error(t, err)
	assert.True(t, sheet.IsInvalidRowError(err), "got %T: %v", err, err)

	over := make([]map[string]any, 501)
	for i := range over {
		over[i] = map[string]any{"name": "cyd"}
	}
	_, err = h.wf.CreateRows(t.Context(), sh, over, "", "")
	require.Error(t, err)
	assert.True(t, sheet.IsBatchTooLargeError(err), "got %T: %v", err, err)
	ae := appErrorOf(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, ae.HTTPStatus())
	assert.Contains(t, ae.Message(), "500", "the refusal must state the limit it enforces")
	assert.Zero(t, h.google.callCount(), "the row cap is enforced before any Google call")
}

func TestWriteWorkflow_CreateRows_RunsTheLockAndTheProjectionInOneUnitOfWork(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	h.google.setAppend(http.StatusOK, appendedThree)

	_, err := h.wf.CreateRows(t.Context(), sh, []map[string]any{
		{"name": "cyd"}, {"name": "dee"}, {"name": "eve"},
	}, "", "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), h.units.runs.Load(),
		"the Google read, the one append and every projection write share one transaction")
}

const twoLiveTable = `{"values":[["id","name","deleted_at"],["1","ada",""],["2","bob",""]]}`

func updateCount(f *fakeWriteSheets) int {
	n := 0
	for _, r := range f.recorded() {
		if r.method == http.MethodPut {
			n++
		}
	}
	return n
}

func sentCells(t *testing.T, req recordedRequest) []any {
	t.Helper()
	var sent struct {
		Values [][]any `json:"values"`
	}
	require.NoError(t, json.Unmarshal([]byte(req.body), &sent))
	require.Len(t, sent.Values, 1, "one row per update request")
	return sent.Values[0]
}

// SECURITY: the flag is checked before any Google call, so a non-writable sheet
// cannot be probed for existence through timing or error shape.
func TestWriteWorkflow_SoftDeleteRowRefusesANonWritableSheetWithoutCallingGoogle(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", false)
	sh.SoftDelete = true
	h.seedContract(sh)

	err := h.wf.SoftDeleteRow(t.Context(), sh, "1", "")
	require.Error(t, err)
	assert.True(t, sheet.IsNotWritableError(err), "got %v", err)
	assert.Equal(t, http.StatusForbidden, appErrorOf(t, err).HTTPStatus())
	assert.Zero(t, h.google.callCount(), "no request may reach Google for a non-writable sheet")
}

func TestWriteWorkflow_SoftDeleteRow_WritesAnRFC3339TombstoneAndDropsTheRowFromThePayload(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	sh.SoftDelete = true
	h.seedContract(sh)
	h.google.setTable(http.StatusOK, twoLiveTable)
	key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}
	h.rows.Seed(key, []sheet.ProjectedRow{
		{RowID: "1", RowIndex: 0, Data: gsheet.Row{"id": "1", "name": "ada"}},
		{RowID: "2", RowIndex: 1, Data: gsheet.Row{"id": "2", "name": "bob"}},
	})
	before := h.rows.Generation(sh.ID)

	require.NoError(t, h.wf.SoftDeleteRow(t.Context(), sh, "1", ""))

	req := h.google.lastOf(t, http.MethodPut)
	assert.Contains(t, req.url, "valueInputOption=RAW", "a tombstone is a cell value like any other, so it is written RAW")
	assert.Contains(t, decodedURL(t, req.url), "'Rates'!A2:C2", "the row is addressed at row_index+2, never by deleteDimension")
	cells := sentCells(t, req)
	require.Len(t, cells, 3)
	assert.Equal(t, []any{"1", "ada"}, cells[:2], "the columns the delete does not touch are written back unchanged")
	stamp, ok := cells[2].(string)
	require.True(t, ok, "the tombstone cell = %#v, want a string", cells[2])
	at, err := time.Parse(time.RFC3339, stamp)
	require.NoError(t, err, "the tombstone cell must be an RFC3339 instant, so its text order is chronological")
	assert.WithinDuration(t, time.Now(), at, time.Minute)

	projected := h.rows.Projected(key)
	require.Len(t, projected, 2, "a tombstoned row stays in the projection")
	require.Equal(t, "1", projected[0].RowID)
	assert.NotNil(t, projected[0].DeletedAt, "the projection records the tombstone")

	live, err := h.rows.ListLive(t.Context(), key)
	require.NoError(t, err)
	require.Len(t, live, 1)
	assert.Equal(t, "2", live[0].RowID)

	snap, found, err := h.snaps.Get(t.Context(), key)
	require.NoError(t, err)
	require.True(t, found)
	assert.JSONEq(t, `[{"id":"2","name":"bob"}]`, string(snap.Payload), "the rebuilt payload excludes the deleted row")
	assert.Equal(t, before+1, h.rows.Generation(sh.ID), "the write bumps the generation once")
	assert.Zero(t, h.unex.Load(), "a clean delete reports no incident")
}

// The write count is the assertion: a second delete answered from the projection proves the idempotent
// case short-circuits, where a rewritten tombstone would pass a status-only check and lose the first instant.
func TestWriteWorkflow_SoftDeleteRow_IsIdempotentAndWritesOnce(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	sh.SoftDelete = true
	h.seedContract(sh)
	h.google.setTable(http.StatusOK, softDeleteTable)
	key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}
	h.rows.Seed(key, []sheet.ProjectedRow{
		{RowID: "1", RowIndex: 0, Data: gsheet.Row{"id": "1", "name": "ada"}},
	})

	require.NoError(t, h.wf.SoftDeleteRow(t.Context(), sh, "1", ""))
	afterFirst := h.google.callCount()
	require.Equal(t, 1, updateCount(h.google))
	first := h.rows.Projected(key)
	require.Len(t, first, 1)
	require.NotNil(t, first[0].DeletedAt)

	require.NoError(t, h.wf.SoftDeleteRow(t.Context(), sh, "1", ""), "a delete of a tombstoned row is 204, not an error")
	assert.Equal(t, 1, updateCount(h.google), "the second delete must be answered from the projection")
	assert.Equal(t, afterFirst, h.google.callCount(), "an already-tombstoned row costs no Google call at all")

	again := h.rows.Projected(key)
	require.Len(t, again, 1)
	require.NotNil(t, again[0].DeletedAt)
	assert.Equal(t, *first[0].DeletedAt, *again[0].DeletedAt, "the first tombstone's instant must survive the retry")
}

func TestWriteWorkflow_SoftDeleteRow_RefusesAnUnknownID(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	sh.SoftDelete = true
	h.seedContract(sh)
	h.google.setTable(http.StatusOK, softDeleteTable)

	err := h.wf.SoftDeleteRow(t.Context(), sh, "99", "")
	require.Error(t, err)
	assert.True(t, sheet.IsRowNotFoundError(err), "got %T: %v", err, err)
	assert.Equal(t, http.StatusNotFound, appErrorOf(t, err).HTTPStatus())
	assert.Zero(t, updateCount(h.google), "an unknown id is refused before the write")
}

// The persisted flag is what task 1 exists for: the refusal names the remedy and costs no Google call,
// which is the case capabilities.softDelete warns a client about.
func TestWriteWorkflow_SoftDeleteRow_RefusesATabWithNoDeletedAtColumnWithoutCallingGoogle(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	h.seedContract(sh)

	err := h.wf.SoftDeleteRow(t.Context(), sh, "1", "")
	require.Error(t, err)
	assert.True(t, sheet.IsSoftDeleteUnsupportedError(err), "got %T: %v", err, err)
	ae := appErrorOf(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, ae.HTTPStatus())
	assert.Equal(t, apperror.CodeSheetSoftDeleteUnsupported, ae.Code())
	assert.Contains(t, ae.Message(), "deleted_at", "the copy must name the remedy a client can act on")
	assert.Zero(t, h.google.callCount(), "a tab that cannot record a deletion is refused before any Google call")
}

// The flag mirrors the last refresh, so the fresh read inside the lock is what refuses a column dropped since.
func TestWriteWorkflow_SoftDeleteRow_RefusesADeletedAtColumnDroppedSinceTheLastRefresh(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	sh.SoftDelete = true
	h.seedContract(sh)
	h.google.setTable(http.StatusOK, idNameTable)

	err := h.wf.SoftDeleteRow(t.Context(), sh, "1", "")
	require.Error(t, err)
	assert.True(t, sheet.IsSoftDeleteUnsupportedError(err), "got %T: %v", err, err)
	assert.Zero(t, updateCount(h.google), "the refusal precedes the write")
}

func TestWriteWorkflow_SoftDeleteRow_RefusesADriftedContractWithoutCallingGoogle(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	sh.SoftDelete = true
	h.rows.SeedContract(sh.ID, sheet.ContractState{OK: false, Reason: "duplicate id"})

	err := h.wf.SoftDeleteRow(t.Context(), sh, "1", "")
	require.Error(t, err)
	assert.True(t, sheet.IsContractViolationError(err), "got %T: %v", err, err)
	assert.Equal(t, http.StatusConflict, appErrorOf(t, err).HTTPStatus())
	assert.Zero(t, h.google.callCount(), "a sheet whose rows cannot be addressed by id is refused before any read")
}

// NOTE: LockSheet is enroll-or-own, so a delete that does not run inside one transaction takes no lock at all.
func TestWriteWorkflow_SoftDeleteRow_RunsTheLockAndTheProjectionInOneUnitOfWork(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	sh.SoftDelete = true
	h.seedContract(sh)
	h.google.setTable(http.StatusOK, softDeleteTable)

	require.NoError(t, h.wf.SoftDeleteRow(t.Context(), sh, "1", ""))
	assert.Equal(t, int64(1), h.units.runs.Load(),
		"the Google read, the Google write and the projection write share one transaction")
}

// The tab GET is the surface a delete must clear the row from, and the row GET is what still answers 404.
func TestWriteWorkflow_SoftDeleteRow_RemovesTheRowFromBothReadsWithNoRefetch(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	sh.SoftDelete = true
	h.seedContract(sh)
	h.google.setTable(http.StatusOK, twoLiveTable)
	key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}
	h.rows.Seed(key, []sheet.ProjectedRow{
		{RowID: "1", RowIndex: 0, Data: gsheet.Row{"id": "1", "name": "ada"}},
		{RowID: "2", RowIndex: 1, Data: gsheet.Row{"id": "2", "name": "bob"}},
	})

	require.NoError(t, h.wf.SoftDeleteRow(t.Context(), sh, "1", ""))
	afterWrite := h.google.callCount()

	got, err := h.rf.Rows(t.Context(), sh)
	require.NoError(t, err)
	assert.Equal(t, []gsheet.Row{{"id": "2", "name": "bob"}}, got.Values, "the tab read must not carry the deleted row")

	_, err = h.rf.RowByID(t.Context(), sh, "1")
	assert.True(t, sheet.IsRowNotFoundError(err), "a tombstoned row reads back as not found: %v", err)
	assert.Equal(t, afterWrite, h.google.callCount(), "both reads must be answered from the rebuilt snapshot")
}

const tombstonedFirstTable = `{"values":[["id","name","deleted_at"],["1","ada","2026-01-02T03:04:05Z"]]}`

func quotedTag(tag string) string {
	return `"` + tag + `"`
}

func TestWriteWorkflow_PatchRow_ProceedsOnAMatchingIfMatch(t *testing.T) {
	t.Parallel()
	for _, header := range []string{quotedTag(etagOf(t, `{"id":"1","name":"ada"}`)), "*",
		`"aaaa", ` + quotedTag(etagOf(t, `{"id":"1","name":"ada"}`))} {
		t.Run(header, func(t *testing.T) {
			t.Parallel()
			h := newWriteHarness(t)
			sh, _ := h.seed(t, "Rates", true)
			h.seedContract(sh)

			row, err := h.wf.PatchRow(t.Context(), sh, "1", map[string]any{"name": "ada2"}, header)
			require.NoError(t, err)
			assert.Equal(t, gsheet.Row{"id": "1", "name": "ada2"}, row)
			assert.Equal(t, 1, updateCount(h.google))
		})
	}
}

// The tag is compared against the tab read taken inside the lock, so a stale tag refuses the write
// before Values.Update is called — Google has no conditional write of its own.
func TestWriteWorkflow_PatchRow_RefusesAStaleIfMatchWithoutWriting(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	h.seedContract(sh)

	_, err := h.wf.PatchRow(t.Context(), sh, "1", map[string]any{"name": "ada2"},
		quotedTag(etagOf(t, `{"id":"1","name":"stale"}`)))
	require.Error(t, err)
	assert.True(t, sheet.IsPreconditionFailedError(err), "got %T: %v", err, err)
	assert.Equal(t, http.StatusPreconditionFailed, appErrorOf(t, err).HTTPStatus())
	assert.Equal(t, apperror.CodeSheetPreconditionFailed, appErrorOf(t, err).Code())
	assert.Zero(t, updateCount(h.google), "a refused precondition must not reach Values.Update")
}

func TestWriteWorkflow_ReplaceRow_RefusesAStaleIfMatchWithoutWriting(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	h.seedContract(sh)

	_, err := h.wf.ReplaceRow(t.Context(), sh, "1", map[string]any{"name": "ada2"},
		quotedTag(etagOf(t, `{"id":"1","name":"stale"}`)))
	require.Error(t, err)
	assert.True(t, sheet.IsPreconditionFailedError(err), "got %T: %v", err, err)
	assert.Zero(t, updateCount(h.google))
}

func TestWriteWorkflow_SoftDeleteRow_RefusesAStaleIfMatchOnALiveRow(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	sh.SoftDelete = true
	h.seedContract(sh)
	h.google.setTable(http.StatusOK, softDeleteTable)

	err := h.wf.SoftDeleteRow(t.Context(), sh, "1", quotedTag(etagOf(t, `{"id":"1","name":"stale"}`)))
	require.Error(t, err)
	assert.True(t, sheet.IsPreconditionFailedError(err), "got %T: %v", err, err)
	assert.Zero(t, updateCount(h.google))
}

// A tombstoned row's hash differs from any tag a client still holds, so evaluating the precondition
// would 412 exactly the retry idempotency exists to answer 204. Idempotency wins, on both paths.
func TestWriteWorkflow_SoftDeleteRow_AnswersATombstonedRowBeforeEvaluatingIfMatch(t *testing.T) {
	t.Parallel()
	deletedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cases := []struct {
		name      string
		projected []sheet.ProjectedRow
	}{
		{
			name: "answered from the projection",
			projected: []sheet.ProjectedRow{
				{RowID: "1", RowIndex: 0, Data: gsheet.Row{"id": "1", "name": "ada"}, DeletedAt: &deletedAt},
			},
		},
		{name: "answered from the fresh read inside the lock", projected: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newWriteHarness(t)
			sh, _ := h.seed(t, "Rates", true)
			sh.SoftDelete = true
			h.seedContract(sh)
			h.google.setTable(http.StatusOK, tombstonedFirstTable)
			key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}
			h.rows.Seed(key, tc.projected)

			require.NoError(t,
				h.wf.SoftDeleteRow(t.Context(), sh, "1", quotedTag(etagOf(t, `{"id":"1","name":"held-before-the-delete"}`))),
				"a delete of a tombstoned row is 204 whatever tag it carries")
			assert.Zero(t, updateCount(h.google), "the tombstone stands, so nothing is rewritten")
		})
	}
}

// Without the key a retried create with a server-generated id silently duplicates the row.
func TestWriteWorkflow_CreateRow_ReplaysAKeyedRetryRatherThanDuplicating(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	h.seedContract(sh)

	first, err := h.wf.CreateRow(t.Context(), sh, map[string]any{"name": "cyd"}, "key-1", "hash-1")
	require.NoError(t, err)
	require.Equal(t, 1, appendCount(h.google))

	second, err := h.wf.CreateRow(t.Context(), sh, map[string]any{"name": "cyd"}, "key-1", "hash-1")
	require.NoError(t, err)
	assert.Equal(t, first, second, "the retry replays the first attempt's row, id and tag")
	assert.Equal(t, 1, appendCount(h.google), "a replayed create must not reach Google a second time")
	assert.Zero(t, h.unex.Load())
}

func TestWriteWorkflow_CreateRow_RefusesADifferentBodyUnderTheSameKey(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	h.seedContract(sh)

	_, err := h.wf.CreateRow(t.Context(), sh, map[string]any{"name": "cyd"}, "key-1", "hash-1")
	require.NoError(t, err)

	_, err = h.wf.CreateRow(t.Context(), sh, map[string]any{"name": "dee"}, "key-1", "hash-2")
	require.Error(t, err)
	assert.True(t, sheet.IsIdempotencyMismatchError(err), "got %T: %v", err, err)
	assert.Equal(t, http.StatusUnprocessableEntity, appErrorOf(t, err).HTTPStatus())
	assert.Equal(t, 1, appendCount(h.google))
}

// A reservation left held by a failed append would answer every later retry with a stale error.
func TestWriteWorkflow_CreateRow_ReleasesTheKeyWhenTheAppendFails(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	h.seedContract(sh)
	h.google.setAppend(http.StatusInternalServerError, `{"error":{"code":500}}`)

	_, err := h.wf.CreateRow(t.Context(), sh, map[string]any{"name": "cyd"}, "key-1", "hash-1")
	require.Error(t, err)

	h.google.setAppend(http.StatusOK, appendedOne)
	row, err := h.wf.CreateRow(t.Context(), sh, map[string]any{"name": "cyd"}, "key-1", "hash-1")
	require.NoError(t, err, "the released key must be claimable by a genuine retry")
	assert.Equal(t, gsheet.Row{"id": row.ID, "name": "cyd"}, row.Data)
}

func TestWriteWorkflow_CreateRows_ReplaysAKeyedRetryRatherThanDuplicating(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	h.seedContract(sh)
	h.google.setAppend(http.StatusOK, appendedThree)
	batch := []map[string]any{{"id": "a", "name": "cyd"}, {"id": "b", "name": "dee"}, {"id": "c", "name": "eve"}}

	first, err := h.wf.CreateRows(t.Context(), sh, batch, "key-1", "hash-1")
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b", "c"}, first)
	require.Equal(t, 1, appendCount(h.google))

	second, err := h.wf.CreateRows(t.Context(), sh, batch, "key-1", "hash-1")
	require.NoError(t, err)
	assert.Equal(t, first, second, "the retry replays the batch's ids in request order")
	assert.Equal(t, 1, appendCount(h.google), "a replayed batch must not reach Google a second time")
	assert.Zero(t, h.unex.Load())
}
