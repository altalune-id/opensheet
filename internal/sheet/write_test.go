package sheet_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

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
	appendedOne   = `{"updates":{"updatedRows":1}}`
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

func (f *fakeWriteSheets) factory() gsheet.WriterFactory {
	return func(ctx context.Context, ts oauth2.TokenSource) (*gsheet.Writer, error) {
		return gsheet.NewWriter(ctx, ts, gworkspace.WithBaseURL(f.url))
	}
}

type writeHarness struct {
	snaps    *fakes.SheetSnapshots
	attempts sheet.IdempotencyStore
	srcs     *fakes.SheetSources
	toks     *fakes.SheetTokenSources
	reauth   *fakes.SheetReauthers
	google   *fakeWriteSheets
	unex     *atomic.Int64
	wf       *sheet.WriteWorkflow
}

func newWriteHarness(t *testing.T) *writeHarness {
	t.Helper()
	h := &writeHarness{
		snaps:    fakes.NewSheetSnapshots(),
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
	h.wf = sheet.NewWriteWorkflow(
		h.snaps, h.attempts, h.srcs, h.toks, h.reauth, h.google.factory(),
		slog.New(slog.NewTextHandler(io.Discard, nil)), unexpected,
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

	_, err := h.wf.PatchRow(t.Context(), sh, "1", map[string]any{"name": "ada2"})
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

	row, err := h.wf.PatchRow(t.Context(), sh, "2", map[string]any{"name": "bob2"})
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

			_, err := h.wf.PatchRow(t.Context(), sh, tc.id, map[string]any{"name": "next"})
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

	_, err := h.wf.PatchRow(t.Context(), sh, "", map[string]any{"name": "next"})
	require.Error(t, err)
	assert.True(t, sheet.IsRowNotFoundError(err), "a blank slot carries no id and is not addressable: %v", err)
}

func TestWriteWorkflow_PatchRowReadsFreshRatherThanTheSnapshot(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)
	key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}
	h.snaps.Seed(key, sheet.Snapshot{ETag: "etag", Payload: []byte(`[{"id":"9","name":"stale"}]`)})

	_, err := h.wf.PatchRow(t.Context(), sh, "1", map[string]any{"name": "ada2"})
	require.NoError(t, err)

	got := h.google.lastOf(t, http.MethodGet)
	assert.Contains(t, decodedURL(t, got.url), "/v4/spreadsheets/FILE/values/'Rates'",
		"the row must come from Google, never from the cache")

	_, found, gErr := h.snaps.Get(t.Context(), key)
	require.NoError(t, gErr)
	assert.False(t, found, "the patch purges the snapshot it deliberately ignored")
}

func TestWriteWorkflow_PatchRowRejectsAnEmptyPatch(t *testing.T) {
	t.Parallel()
	h := newWriteHarness(t)
	sh, _ := h.seed(t, "Rates", true)

	_, err := h.wf.PatchRow(t.Context(), sh, "1", nil)
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

			_, err := h.wf.PatchRow(t.Context(), sh, tc.id, tc.patch)
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

	row, err := h.wf.PatchRow(t.Context(), sh, "1", map[string]any{"name": 1250000})
	require.NoError(t, err)
	assert.Equal(t, "1250000", row["name"])
	assert.NotContains(t, h.google.lastOf(t, http.MethodPut).body, `"1250000"`,
		"a numeric cell must reach Google as a JSON number")
}

type stubError string

func (e stubError) Error() string { return string(e) }
