package sheet_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
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
	twoRowBody   = `{"values":[["id","name","qty"],["a","apple","3"],["b","pear","5"]]}`
	twoRowJSON   = `[{"id":"a","name":"apple","qty":"3"},{"id":"b","name":"pear","qty":"5"}]`
	firstTabBody = `{"properties":{"title":"Doc"},"sheets":[{"properties":{"title":"First"}},{"properties":{"title":"Second"}}]}`
)

type fakeSheets struct {
	mu         sync.Mutex
	rowsCalls  int
	metaCalls  int
	rowsStatus int
	rowsBody   string
	metaStatus int
	metaBody   string
	block      chan struct{}

	url string
}

func newFakeSheets(t *testing.T) *fakeSheets {
	t.Helper()
	f := &fakeSheets{
		rowsStatus: http.StatusOK,
		rowsBody:   twoRowBody,
		metaStatus: http.StatusOK,
		metaBody:   firstTabBody,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v4/spreadsheets/{file}/values/", f.serveValues)
	mux.HandleFunc("/v4/spreadsheets/{file}", f.serveMeta)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	f.url = srv.URL
	return f
}

func (f *fakeSheets) serveValues(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	f.rowsCalls++
	status, body, block := f.rowsStatus, f.rowsBody, f.block
	f.mu.Unlock()
	if block != nil {
		<-block
	}
	writeJSON(w, status, body)
}

func (f *fakeSheets) serveMeta(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	f.metaCalls++
	status, body := f.metaStatus, f.metaBody
	f.mu.Unlock()
	writeJSON(w, status, body)
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func (f *fakeSheets) setRows(status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rowsStatus, f.rowsBody = status, body
}

func (f *fakeSheets) setMeta(status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.metaStatus, f.metaBody = status, body
}

func (f *fakeSheets) blockOn(ch chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.block = ch
}

func (f *fakeSheets) counts() (rows, meta int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rowsCalls, f.metaCalls
}

func (f *fakeSheets) factory() gsheet.Factory {
	return func(ctx context.Context, ts oauth2.TokenSource) (*gsheet.Client, error) {
		return gsheet.New(ctx, ts, gworkspace.WithBaseURL(f.url))
	}
}

type readHarness struct {
	snaps  *fakes.SheetSnapshots
	rows   *fakes.SheetRows
	srcs   *fakes.SheetSources
	toks   *fakes.SheetTokenSources
	reauth *fakes.SheetReauthers
	google *fakeSheets
	unex   *atomic.Int64
	wf     *sheet.ReadWorkflow
}

type harnessOpts struct {
	publicEnabled   bool
	defaultTTL      time.Duration
	maxPayloadBytes int64
}

func newReadHarness(t *testing.T, opts harnessOpts) *readHarness {
	t.Helper()
	if opts.defaultTTL == 0 {
		opts.defaultTTL = time.Minute
	}
	if opts.maxPayloadBytes == 0 {
		opts.maxPayloadBytes = 1 << 20
	}
	h := &readHarness{
		snaps:  fakes.NewSheetSnapshots(),
		rows:   fakes.NewSheetRows(),
		srcs:   fakes.NewSheetSources(),
		toks:   fakes.NewSheetTokenSources(),
		reauth: fakes.NewSheetReauthers(),
		google: newFakeSheets(t),
		unex:   &atomic.Int64{},
	}
	unexpected := func(_ context.Context, _ string, err error, _ ...any) *apperror.AppError {
		h.unex.Add(1)
		return apperror.New("opensheet.unexpected", err.Error(), codes.Internal,
			&apperrorv1.ErrorDetail{Code: "opensheet.unexpected"}).WithCause(err)
	}
	h.wf = sheet.NewReadWorkflow(
		h.snaps, h.rows, h.srcs, h.toks, h.reauth, h.google.factory(),
		fakeCaps{public: opts.publicEnabled},
		opts.defaultTTL, opts.maxPayloadBytes,
		slog.New(slog.NewTextHandler(io.Discard, nil)), unexpected,
	)
	return h
}

func (h *readHarness) seed(t *testing.T, tab string, vis sheet.Visibility, ttl time.Duration) (*sheet.Sheet, uuid.UUID) {
	t.Helper()
	credentialID := uuid.Must(uuid.NewV7())
	sh := &sheet.Sheet{
		ID:            uuid.Must(uuid.NewV7()),
		OrgID:         uuid.Must(uuid.NewV7()),
		ProjectID:     uuid.Must(uuid.NewV7()),
		SpreadsheetID: uuid.Must(uuid.NewV7()),
		Tab:           tab,
		Slug:          "prices",
		Visibility:    vis,
		CacheTTL:      ttl,
	}
	h.srcs.Set(sh.SpreadsheetID, sheet.Source{GoogleFileID: "FILE", CredentialID: credentialID})
	return sh, credentialID
}

func etagOf(t *testing.T, payload string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])[:32]
}

func TestReadWorkflow_MissFetchesSerializesAndCaches(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)

	got, err := h.wf.Rows(t.Context(), sh)
	if err != nil {
		t.Fatalf("Rows err = %v", err)
	}
	if got.Cached || got.Stale {
		t.Errorf("Cached/Stale = %v/%v, want false/false", got.Cached, got.Stale)
	}
	if len(got.Values) != 2 || got.Values[0]["name"] != "apple" || got.Values[1]["qty"] != "5" {
		t.Errorf("Values = %#v", got.Values)
	}
	if got.ETag != etagOf(t, twoRowJSON) {
		t.Errorf("ETag = %q, want sha256 of %s truncated to 32 hex", got.ETag, twoRowJSON)
	}
	if got.FetchedAt.IsZero() || got.FetchedAt.Location() != time.UTC {
		t.Errorf("FetchedAt = %v, want a UTC instant", got.FetchedAt)
	}

	snap, ok, err := h.snaps.Get(t.Context(), sheet.SnapshotKey{SheetID: sh.ID, Tab: "Q1"})
	if err != nil || !ok {
		t.Fatalf("snapshot Get = ok:%v err:%v, want cached", ok, err)
	}
	if string(snap.Payload) != twoRowJSON {
		t.Errorf("Payload = %s, want %s", snap.Payload, twoRowJSON)
	}
	if snap.ETag != got.ETag {
		t.Errorf("snapshot ETag = %q, want %q", snap.ETag, got.ETag)
	}
}

func TestReadWorkflow_WarningsFromHeaderNormalizationAreCarried(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	h.google.setRows(http.StatusOK, `{"values":[["name","","name"],["a","b","c"]]}`)
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)

	got, err := h.wf.Rows(t.Context(), sh)
	if err != nil {
		t.Fatalf("Rows err = %v", err)
	}
	if len(got.Warnings) != 2 {
		t.Fatalf("Warnings = %v, want 2", got.Warnings)
	}
	if got.Values[0]["col_2"] != "b" || got.Values[0]["name_2"] != "c" {
		t.Errorf("Values = %#v, want normalized headers", got.Values)
	}
}

func TestReadWorkflow_EmptyGridSerializesAsAnEmptyArray(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	h.google.setRows(http.StatusOK, `{}`)
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)

	got, err := h.wf.Rows(t.Context(), sh)
	if err != nil {
		t.Fatalf("Rows err = %v", err)
	}
	if len(got.Values) != 0 {
		t.Errorf("Values = %#v, want none", got.Values)
	}
	snap, _, _ := h.snaps.Get(t.Context(), sheet.SnapshotKey{SheetID: sh.ID, Tab: "Q1"})
	if string(snap.Payload) != "[]" {
		t.Errorf("Payload = %s, want []", snap.Payload)
	}
	if got.ETag != etagOf(t, "[]") {
		t.Errorf("ETag = %q, want sha256 of []", got.ETag)
	}
}

func TestReadWorkflow_FreshSnapshotIsServedWithoutCallingGoogle(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	fetchedAt := time.Now().UTC().Add(-time.Second)
	h.snaps.Seed(sheet.SnapshotKey{SheetID: sh.ID, Tab: "Q1"}, sheet.Snapshot{
		ETag:      "cached-etag",
		FetchedAt: fetchedAt,
		ExpiresAt: time.Now().UTC().Add(time.Hour),
		Payload:   []byte(twoRowJSON),
	})

	got, err := h.wf.Rows(t.Context(), sh)
	if err != nil {
		t.Fatalf("Rows err = %v", err)
	}
	if !got.Cached || got.Stale {
		t.Errorf("Cached/Stale = %v/%v, want true/false", got.Cached, got.Stale)
	}
	if got.ETag != "cached-etag" || !got.FetchedAt.Equal(fetchedAt) {
		t.Errorf("ETag/FetchedAt = %q/%v, want the snapshot's", got.ETag, got.FetchedAt)
	}
	if len(got.Values) != 2 || got.Values[0]["name"] != "apple" {
		t.Errorf("Values = %#v, want the snapshot payload decoded", got.Values)
	}
	if rows, meta := h.google.counts(); rows != 0 || meta != 0 {
		t.Errorf("google calls = rows:%d meta:%d, want none", rows, meta)
	}
	if ids := h.toks.AskedIDs(); len(ids) != 0 {
		t.Errorf("token source resolved %v on a cache hit, want none", ids)
	}
	if ids := h.srcs.AskedIDs(); len(ids) != 1 || ids[0] != sh.SpreadsheetID {
		t.Errorf("AskedIDs = %v, want [%v]", ids, sh.SpreadsheetID)
	}
}

func TestReadWorkflow_ConcurrentMissesCauseOneGoogleCall(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	release := make(chan struct{})
	h.google.blockOn(release)

	const n = 20
	entered := make(chan struct{}, n)
	results := make([]sheet.Rows, n)
	errs := make([]error, n)

	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			entered <- struct{}{}
			results[i], errs[i] = h.wf.Rows(t.Context(), sh)
		})
	}
	for range n {
		<-entered
	}
	// NOTE: the leader's fetch must still be in flight when the last follower reaches
	// singleflight, so the window is widened deliberately rather than raced.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if rows, _ := h.google.counts(); rows != 1 {
		t.Fatalf("google row calls = %d, want exactly 1 for %d concurrent misses", rows, n)
	}
	want := etagOf(t, twoRowJSON)
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("goroutine %d err = %v", i, errs[i])
		}
		if results[i].ETag != want {
			t.Errorf("goroutine %d ETag = %q, want %q", i, results[i].ETag, want)
		}
	}
}

func TestReadWorkflow_ServesStaleWhenGoogleFails(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	fetchedAt := time.Now().UTC().Add(-2 * time.Hour)
	h.snaps.Seed(sheet.SnapshotKey{SheetID: sh.ID, Tab: "Q1"}, sheet.Snapshot{
		ETag:      "stale-etag",
		FetchedAt: fetchedAt,
		ExpiresAt: time.Now().UTC().Add(-time.Hour),
		Payload:   []byte(`[{"name":"old"}]`),
	})
	h.google.setRows(http.StatusServiceUnavailable, `{"error":{"code":503,"message":"down"}}`)

	got, err := h.wf.Rows(t.Context(), sh)
	if err != nil {
		t.Fatalf("Rows err = %v, want the stale snapshot served", err)
	}
	if !got.Stale || !got.Cached {
		t.Errorf("Cached/Stale = %v/%v, want true/true", got.Cached, got.Stale)
	}
	if got.ETag != "stale-etag" || !got.FetchedAt.Equal(fetchedAt) {
		t.Errorf("ETag/FetchedAt = %q/%v, want the snapshot's", got.ETag, got.FetchedAt)
	}
	if len(got.Values) != 1 || got.Values[0]["name"] != "old" {
		t.Errorf("Values = %#v, want the stale payload", got.Values)
	}
}

func TestReadWorkflow_GoogleFailureWithNoSnapshotReturnsTheTypedError(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		pred   func(error) bool
	}{
		{"not found", http.StatusNotFound, `{"error":{"code":404,"message":"gone"}}`, gworkspace.IsNotFoundError},
		{"permission denied", http.StatusForbidden, `{"error":{"code":403,"message":"nope"}}`, gworkspace.IsPermissionDeniedError},
		{"quota", http.StatusTooManyRequests, `{"error":{"code":429,"message":"slow"}}`, gworkspace.IsQuotaExceededError},
		{"unavailable", http.StatusServiceUnavailable, `{"error":{"code":503,"message":"down"}}`, gworkspace.IsUnavailableError},
		{"tab gone", http.StatusBadRequest, `{"error":{"code":400,"message":"bad range"}}`, gsheet.IsTabNotFoundError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newReadHarness(t, harnessOpts{})
			sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
			h.google.setRows(tc.status, tc.body)

			_, err := h.wf.Rows(t.Context(), sh)
			if !tc.pred(err) {
				t.Fatalf("err = %v, want the typed google error unchanged", err)
			}
			if h.unex.Load() != 0 {
				t.Errorf("unexpected reported %d times for an expected Google failure", h.unex.Load())
			}
		})
	}
}

func TestReadWorkflow_AuthFailureMarksTheCredentialForReauth(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, credentialID := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	h.google.setRows(http.StatusUnauthorized, `{"error":{"code":401,"message":"invalid_grant"}}`)

	_, err := h.wf.Rows(t.Context(), sh)
	if !gworkspace.IsAuthExpiredError(err) {
		t.Fatalf("err = %v, want AuthExpiredError", err)
	}
	marked := h.reauth.MarkedIDs()
	if len(marked) != 1 || marked[0] != credentialID {
		t.Fatalf("MarkedIDs = %v, want [%v]", marked, credentialID)
	}
}

func TestReadWorkflow_AuthFailureReportsAFailedReauthMarkAndStillReturnsTheGoogleError(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	h.google.setRows(http.StatusUnauthorized, `{"error":{"code":401,"message":"invalid_grant"}}`)
	h.reauth.Err = errors.New("write failed")

	_, err := h.wf.Rows(t.Context(), sh)
	if !gworkspace.IsAuthExpiredError(err) {
		t.Fatalf("err = %v, want AuthExpiredError", err)
	}
	if h.unex.Load() != 1 {
		t.Errorf("unexpected called %d times, want 1 for the failed reauth mark", h.unex.Load())
	}
}

func TestReadWorkflow_AuthFailureStillServesStale(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, credentialID := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	h.snaps.Seed(sheet.SnapshotKey{SheetID: sh.ID, Tab: "Q1"}, sheet.Snapshot{
		ETag:      "stale-etag",
		FetchedAt: time.Now().UTC().Add(-time.Hour),
		ExpiresAt: time.Now().UTC().Add(-time.Minute),
		Payload:   []byte(`[{"name":"old"}]`),
	})
	h.google.setRows(http.StatusUnauthorized, `{"error":{"code":401,"message":"invalid_grant"}}`)

	got, err := h.wf.Rows(t.Context(), sh)
	if err != nil {
		t.Fatalf("Rows err = %v, want stale served", err)
	}
	if !got.Stale {
		t.Error("Stale = false, want true")
	}
	if marked := h.reauth.MarkedIDs(); len(marked) != 1 || marked[0] != credentialID {
		t.Errorf("MarkedIDs = %v, want [%v]", marked, credentialID)
	}
}

func TestReadWorkflow_PublicSheetRefusedWhenCapabilityIsOff(t *testing.T) {
	h := newReadHarness(t, harnessOpts{publicEnabled: false})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	sh.Visibility = sheet.VisibilityPublic

	_, err := h.wf.Rows(t.Context(), sh)
	if !sheet.IsPublicDisabledError(err) {
		t.Fatalf("err = %v, want PublicDisabledError", err)
	}
	if rows, meta := h.google.counts(); rows != 0 || meta != 0 {
		t.Errorf("google calls = rows:%d meta:%d, want none", rows, meta)
	}

	keyed, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	if _, err := h.wf.Rows(t.Context(), keyed); err != nil {
		t.Fatalf("key-visibility sheet err = %v, want a normal read", err)
	}
}

func TestReadWorkflow_PublicSheetReadsWhenCapabilityIsOn(t *testing.T) {
	h := newReadHarness(t, harnessOpts{publicEnabled: true})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityPublic, 0)

	if _, err := h.wf.Rows(t.Context(), sh); err != nil {
		t.Fatalf("Rows err = %v", err)
	}
}

func TestReadWorkflow_OversizePayloadIsNotCached(t *testing.T) {
	h := newReadHarness(t, harnessOpts{maxPayloadBytes: 8})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)

	_, err := h.wf.Rows(t.Context(), sh)
	if !sheet.IsPayloadTooLargeError(err) {
		t.Fatalf("err = %v, want PayloadTooLargeError", err)
	}
	if _, ok, _ := h.snaps.Get(t.Context(), sheet.SnapshotKey{SheetID: sh.ID, Tab: "Q1"}); ok {
		t.Error("an oversize payload was cached")
	}
	if h.snaps.PutCount() != 0 {
		t.Errorf("PutCount = %d, want 0", h.snaps.PutCount())
	}
}

func TestReadWorkflow_NonPositiveMaxPayloadBytesMeansUnbounded(t *testing.T) {
	h := newReadHarness(t, harnessOpts{maxPayloadBytes: -1})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)

	if _, err := h.wf.Rows(t.Context(), sh); err != nil {
		t.Fatalf("Rows err = %v, want an unbounded payload budget", err)
	}
}

func TestReadWorkflow_SnapshotPutFailureStillReturnsRows(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	h.snaps.PutErr = &sheet.SnapshotTooLargeError{Bytes: 64, MaxBytes: 16}

	got, err := h.wf.Rows(t.Context(), sh)
	if err != nil {
		t.Fatalf("Rows err = %v, want the rows returned despite a cache write failure", err)
	}
	if got.Cached || len(got.Values) != 2 {
		t.Errorf("Cached = %v, Values = %#v, want false and two rows", got.Cached, got.Values)
	}
	if h.unex.Load() != 1 {
		t.Errorf("unexpected called %d times, want 1 for the rejected Put", h.unex.Load())
	}
}

func TestReadWorkflow_SnapshotGetFailureFallsBackToGoogle(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	h.snaps.GetErr = errors.New("cache down")

	got, err := h.wf.Rows(t.Context(), sh)
	if err != nil {
		t.Fatalf("Rows err = %v, want a Google read", err)
	}
	if got.Cached || len(got.Values) != 2 {
		t.Errorf("Cached = %v, Values = %#v, want a fresh fetch", got.Cached, got.Values)
	}
	if h.unex.Load() != 1 {
		t.Errorf("unexpected called %d times, want 1 for the failed cache read", h.unex.Load())
	}
}

func TestReadWorkflow_EmptyTabResolvesToFirstTabAndKeysTheSnapshotByIt(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "", sheet.VisibilityKey, 0)

	if _, err := h.wf.Rows(t.Context(), sh); err != nil {
		t.Fatalf("Rows err = %v", err)
	}
	if _, ok, _ := h.snaps.Get(t.Context(), sheet.SnapshotKey{SheetID: sh.ID, Tab: "First"}); !ok {
		t.Fatal("snapshot was not keyed by the resolved first tab")
	}
	if _, ok, _ := h.snaps.Get(t.Context(), sheet.SnapshotKey{SheetID: sh.ID, Tab: ""}); ok {
		t.Error("snapshot was keyed by the empty tab name")
	}
	if _, meta := h.google.counts(); meta != 1 {
		t.Errorf("metadata calls = %d, want 1", meta)
	}
}

func TestReadWorkflow_FirstTabFailurePropagates(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "", sheet.VisibilityKey, 0)
	h.google.setMeta(http.StatusNotFound, `{"error":{"code":404,"message":"gone"}}`)

	if _, err := h.wf.Rows(t.Context(), sh); !gworkspace.IsNotFoundError(err) {
		t.Fatalf("err = %v, want the typed google error", err)
	}
	if h.unex.Load() != 0 {
		t.Errorf("unexpected reported %d times for an expected Google failure", h.unex.Load())
	}
}

func TestReadWorkflow_SpreadsheetWithNoTabsIsATabNotFound(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "", sheet.VisibilityKey, 0)
	h.google.setMeta(http.StatusOK, `{"properties":{"title":"Doc"}}`)

	if _, err := h.wf.Rows(t.Context(), sh); !gsheet.IsTabNotFoundError(err) {
		t.Fatalf("err = %v, want TabNotFoundError", err)
	}
	if h.unex.Load() != 0 {
		t.Errorf("unexpected reported %d times for an expected Google failure", h.unex.Load())
	}
}

func TestReadWorkflow_SourceLookupFailurePropagates(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	h.srcs.Err = errors.New("spreadsheet store down")

	_, err := h.wf.Rows(t.Context(), sh)
	if err == nil {
		t.Fatal("Rows err = nil, want the source lookup failure")
	}
	if h.unex.Load() != 1 {
		t.Errorf("unexpected called %d times, want 1", h.unex.Load())
	}
}

func TestReadWorkflow_TokenSourceFailurePassesAnAppErrorThrough(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	want := apperror.New(apperror.CodeCredentialReauthNeeded, "reconnect it", codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{Code: apperror.CodeCredentialReauthNeeded})
	h.toks.Err = want

	_, err := h.wf.Rows(t.Context(), sh)
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want the credential AppError unchanged", err)
	}
	if h.unex.Load() != 0 {
		t.Errorf("unexpected called %d times, want 0 for an already-wired AppError", h.unex.Load())
	}
}

func TestReadWorkflow_ClientFactoryFailureIsReported(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	h.toks.Source = nil

	_, err := h.wf.Rows(t.Context(), sh)
	if err == nil {
		t.Fatal("Rows err = nil, want the client build failure")
	}
	if h.unex.Load() != 1 {
		t.Errorf("unexpected called %d times, want 1", h.unex.Load())
	}
}

func TestReadWorkflow_PerSheetTTLOverridesTheDefault(t *testing.T) {
	h := newReadHarness(t, harnessOpts{defaultTTL: time.Hour})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 30*time.Second)

	got, err := h.wf.Rows(t.Context(), sh)
	if err != nil {
		t.Fatalf("Rows err = %v", err)
	}
	snap, _, _ := h.snaps.Get(t.Context(), sheet.SnapshotKey{SheetID: sh.ID, Tab: "Q1"})
	if want := got.FetchedAt.Add(30 * time.Second); !snap.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v from the per-sheet TTL", snap.ExpiresAt, want)
	}
}

func TestReadWorkflow_ZeroSheetTTLUsesTheConfiguredDefault(t *testing.T) {
	h := newReadHarness(t, harnessOpts{defaultTTL: 12 * time.Minute})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)

	got, err := h.wf.Rows(t.Context(), sh)
	if err != nil {
		t.Fatalf("Rows err = %v", err)
	}
	snap, _, _ := h.snaps.Get(t.Context(), sheet.SnapshotKey{SheetID: sh.ID, Tab: "Q1"})
	if want := got.FetchedAt.Add(12 * time.Minute); !snap.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v from the default TTL", snap.ExpiresAt, want)
	}
}

func TestReadWorkflow_CorruptStaleSnapshotReturnsTheGoogleError(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	h.snaps.Seed(sheet.SnapshotKey{SheetID: sh.ID, Tab: "Q1"}, sheet.Snapshot{
		ETag:      "stale-etag",
		FetchedAt: time.Now().UTC().Add(-time.Hour),
		ExpiresAt: time.Now().UTC().Add(-time.Minute),
		Payload:   []byte("not json"),
	})
	h.google.setRows(http.StatusServiceUnavailable, `{"error":{"code":503,"message":"down"}}`)

	if _, err := h.wf.Rows(t.Context(), sh); !gworkspace.IsUnavailableError(err) {
		t.Fatalf("err = %v, want the Google failure when the stale payload cannot be decoded", err)
	}
	if h.unex.Load() != 1 {
		t.Errorf("unexpected called %d times, want 1 for the undecodable payload", h.unex.Load())
	}
}

func TestReadWorkflow_CorruptFreshSnapshotIsReported(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	h.snaps.Seed(sheet.SnapshotKey{SheetID: sh.ID, Tab: "Q1"}, sheet.Snapshot{
		ETag:      "cached-etag",
		FetchedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(time.Hour),
		Payload:   []byte("not json"),
	})

	if _, err := h.wf.Rows(t.Context(), sh); err == nil {
		t.Fatal("Rows err = nil, want the undecodable snapshot reported")
	}
	if h.unex.Load() != 1 {
		t.Errorf("unexpected called %d times, want 1", h.unex.Load())
	}
}

func TestPayloadTooLargeError(t *testing.T) {
	err := &sheet.PayloadTooLargeError{Bytes: 900, MaxBytes: 100}
	if got := err.Error(); got != "sheet: payload: 900 bytes over the 100 byte limit" {
		t.Errorf("Error() = %q", got)
	}
	ae := err.ToAppError()
	if ae.Code() != apperror.CodeSheetPayloadTooLarge {
		t.Errorf("Code = %q, want %q", ae.Code(), apperror.CodeSheetPayloadTooLarge)
	}
	if ae.GRPCCode() != codes.InvalidArgument {
		t.Errorf("GRPCCode = %v, want InvalidArgument", ae.GRPCCode())
	}
	if !sheet.IsPayloadTooLargeError(err) {
		t.Error("IsPayloadTooLargeError = false")
	}
	if sheet.IsPayloadTooLargeError(errors.New("other")) {
		t.Error("IsPayloadTooLargeError accepted a foreign error")
	}
}

func TestService_PurgeCache(t *testing.T) {
	newSheet := func(t *testing.T, store *fakes.Sheet, orgID, projectID uuid.UUID) *sheet.Sheet {
		t.Helper()
		sh := &sheet.Sheet{
			ID: uuid.Must(uuid.NewV7()), OrgID: orgID, ProjectID: projectID,
			SpreadsheetID: uuid.Must(uuid.NewV7()), Tab: "Q1", Slug: "prices",
			Visibility: sheet.VisibilityKey,
		}
		if err := store.Save(t.Context(), sh); err != nil {
			t.Fatalf("Save: %v", err)
		}
		return sh
	}

	t.Run("drops every cached tab of the sheet", func(t *testing.T) {
		store, snaps := fakes.NewSheet(), fakes.NewSheetSnapshots()
		svc, unexCalls := newSvcSnaps(t, store, snaps, false)
		ctx, tc := tenantCtx(t)
		sh := newSheet(t, store, tc.OrgID, tc.ProjectID)
		for _, tab := range []string{"Q1", "Q2"} {
			snaps.Seed(sheet.SnapshotKey{SheetID: sh.ID, Tab: tab}, sheet.Snapshot{ETag: "e", Payload: []byte("[]")})
		}

		if err := svc.PurgeCache(ctx, sh.ID); err != nil {
			t.Fatalf("PurgeCache err = %v", err)
		}
		for _, tab := range []string{"Q1", "Q2"} {
			if _, ok, _ := snaps.Get(ctx, sheet.SnapshotKey{SheetID: sh.ID, Tab: tab}); ok {
				t.Errorf("tab %q survived PurgeCache", tab)
			}
		}
		if *unexCalls != 0 {
			t.Errorf("unexpected called %d times", *unexCalls)
		}
	})

	t.Run("a sheet in another project is not found", func(t *testing.T) {
		store, snaps := fakes.NewSheet(), fakes.NewSheetSnapshots()
		svc, _ := newSvcSnaps(t, store, snaps, false)
		other := newSheet(t, store, uuid.New(), uuid.New())
		snaps.Seed(sheet.SnapshotKey{SheetID: other.ID, Tab: "Q1"}, sheet.Snapshot{ETag: "e", Payload: []byte("[]")})
		ctx, _ := tenantCtx(t)

		err := svc.PurgeCache(ctx, other.ID)
		if !sheet.IsNotFoundError(err) {
			t.Fatalf("err = %v, want NotFoundError", err)
		}
		if _, ok, _ := snaps.Get(ctx, sheet.SnapshotKey{SheetID: other.ID, Tab: "Q1"}); !ok {
			t.Error("another project's snapshot was purged")
		}
	})

	t.Run("an unknown sheet is not found", func(t *testing.T) {
		svc, _ := newSvcSnaps(t, fakes.NewSheet(), fakes.NewSheetSnapshots(), false)
		ctx, _ := tenantCtx(t)
		if err := svc.PurgeCache(ctx, uuid.New()); !sheet.IsNotFoundError(err) {
			t.Fatalf("err = %v, want NotFoundError", err)
		}
	})

	t.Run("requires a tenant scope", func(t *testing.T) {
		svc, _ := newSvcSnaps(t, fakes.NewSheet(), fakes.NewSheetSnapshots(), false)
		if err := svc.PurgeCache(context.Background(), uuid.New()); err == nil {
			t.Fatal("PurgeCache err = nil without a tenant scope")
		}
	})

	t.Run("a store failure is reported", func(t *testing.T) {
		store := fakes.NewSheet()
		store.ByIDFn = func(context.Context, uuid.UUID) (*sheet.Sheet, error) {
			return nil, errors.New("db down")
		}
		svc, unexCalls := newSvcSnaps(t, store, fakes.NewSheetSnapshots(), false)
		ctx, _ := tenantCtx(t)
		if err := svc.PurgeCache(ctx, uuid.New()); err == nil {
			t.Fatal("PurgeCache err = nil")
		}
		if *unexCalls != 1 {
			t.Errorf("unexpected called %d times, want 1", *unexCalls)
		}
	})

	t.Run("a cache failure is reported", func(t *testing.T) {
		store, snaps := fakes.NewSheet(), fakes.NewSheetSnapshots()
		snaps.PurgeErr = errors.New("cache down")
		svc, unexCalls := newSvcSnaps(t, store, snaps, false)
		ctx, tc := tenantCtx(t)
		sh := newSheet(t, store, tc.OrgID, tc.ProjectID)

		if err := svc.PurgeCache(ctx, sh.ID); err == nil {
			t.Fatal("PurgeCache err = nil")
		}
		if *unexCalls != 1 {
			t.Errorf("unexpected called %d times, want 1", *unexCalls)
		}
	})
}

func TestRows_PayloadRoundTripsThroughTheSnapshot(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)

	fresh, err := h.wf.Rows(t.Context(), sh)
	if err != nil {
		t.Fatalf("Rows err = %v", err)
	}
	cached, err := h.wf.Rows(t.Context(), sh)
	if err != nil {
		t.Fatalf("second Rows err = %v", err)
	}
	if !cached.Cached {
		t.Fatal("second read was not served from the snapshot")
	}
	if cached.ETag != fresh.ETag {
		t.Errorf("ETag = %q, want %q", cached.ETag, fresh.ETag)
	}
	got, err := json.Marshal(cached.Values)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != twoRowJSON {
		t.Errorf("re-serialized cached rows = %s, want %s", got, twoRowJSON)
	}
	if rows, _ := h.google.counts(); rows != 1 {
		t.Errorf("google row calls = %d, want 1", rows)
	}
}

func TestReadWorkflow_TokenFailureOnAnEmptyTabPropagates(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "", sheet.VisibilityKey, 0)
	h.toks.Err = errors.New("credential store down")

	_, err := h.wf.Rows(t.Context(), sh)
	if err == nil {
		t.Fatal("Rows err = nil, want the token source failure")
	}
	if _, meta := h.google.counts(); meta != 0 {
		t.Errorf("metadata calls = %d, want none without a client", meta)
	}
}

func TestReadWorkflow_NullSnapshotPayloadDecodesToNoRows(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	h.snaps.Seed(sheet.SnapshotKey{SheetID: sh.ID, Tab: "Q1"}, sheet.Snapshot{
		ETag:      "cached-etag",
		FetchedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(time.Hour),
		Payload:   []byte("null"),
	})

	got, err := h.wf.Rows(t.Context(), sh)
	if err != nil {
		t.Fatalf("Rows err = %v", err)
	}
	if got.Values == nil {
		t.Error("Values = nil, want an empty slice so the wire shape stays an array")
	}
	if len(got.Values) != 0 {
		t.Errorf("Values = %#v, want none", got.Values)
	}
}

func TestReadWorkflow_Load_ProjectsRows(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)

	got, err := h.wf.Rows(t.Context(), sh)
	if err != nil {
		t.Fatalf("Rows err = %v", err)
	}
	if string(got.Payload) != twoRowJSON {
		t.Errorf("Payload = %s, want %s", got.Payload, twoRowJSON)
	}

	key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Q1"}
	want := []sheet.ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "apple", "qty": "3"}},
		{RowID: "b", RowIndex: 1, Data: gsheet.Row{"id": "b", "name": "pear", "qty": "5"}},
	}
	if projected := h.rows.Projected(key); !reflect.DeepEqual(projected, want) {
		t.Errorf("projection = %#v, want %#v", projected, want)
	}
	if state := h.rows.Contract(sh.ID); !state.OK || state.Reason != "" {
		t.Errorf("contract = %+v, want a satisfied contract", state)
	}
	if gen := h.rows.Generation(sh.ID); gen != 1 {
		t.Errorf("generation = %d, want 1 after one refresh", gen)
	}
}

func TestReadWorkflow_Load_ProjectsTombstonesButServesLiveRowsOnly(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	h.google.setRows(http.StatusOK,
		`{"values":[["id","name","deleted_at"],["a","ada",""],["b","bo","2026-09-01T10:00:00Z"],[],["c","cyd",""]]}`)
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)

	got, err := h.wf.Rows(t.Context(), sh)
	if err != nil {
		t.Fatalf("Rows err = %v", err)
	}
	const wantPayload = `[{"id":"a","name":"ada"},{"id":"c","name":"cyd"}]`
	if string(got.Payload) != wantPayload {
		t.Errorf("Payload = %s, want live rows only with deleted_at stripped: %s", got.Payload, wantPayload)
	}

	deletedAt := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	want := []sheet.ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "ada"}},
		{RowID: "b", RowIndex: 1, Data: gsheet.Row{"id": "b", "name": "bo"}, DeletedAt: &deletedAt},
		{RowID: "c", RowIndex: 3, Data: gsheet.Row{"id": "c", "name": "cyd"}},
	}
	projected := h.rows.Projected(sheet.SnapshotKey{SheetID: sh.ID, Tab: "Q1"})
	if !reflect.DeepEqual(projected, want) {
		t.Errorf("projection = %#v, want the tombstone kept and the blank row's index consumed: %#v", projected, want)
	}
}

func TestReadWorkflow_Load_DiscardedRefreshServesTheCommittedSnapshot(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Q1"}

	const committedJSON = `[{"id":"a","name":"quince","qty":"9"}]`
	committed := []sheet.ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "quince", "qty": "9"}},
	}
	h.rows.Seed(key, committed)
	fetchedAt := time.Now().UTC().Add(-time.Hour)
	h.snaps.Seed(key, sheet.Snapshot{
		ETag:      etagOf(t, committedJSON),
		FetchedAt: fetchedAt,
		ExpiresAt: time.Now().UTC().Add(-time.Minute),
		Payload:   []byte(committedJSON),
	})
	// a write lands while this refresh is fetching, so the captured generation no longer matches
	h.rows.Bump(sh.ID)

	got, err := h.wf.Rows(t.Context(), sh)
	if err != nil {
		t.Fatalf("Rows err = %v, want the committed picture served", err)
	}
	if string(got.Payload) != committedJSON {
		t.Errorf("Payload = %s, want the committed snapshot %s, not this fetch", got.Payload, committedJSON)
	}
	if got.ETag != etagOf(t, committedJSON) {
		t.Errorf("ETag = %q, want the committed snapshot's", got.ETag)
	}
	if len(got.Values) != 1 || got.Values[0]["name"] != "quince" {
		t.Errorf("Values = %#v, want the committed rows", got.Values)
	}
	if !got.FetchedAt.Equal(fetchedAt) {
		t.Errorf("FetchedAt = %v, want the committed snapshot's %v", got.FetchedAt, fetchedAt)
	}
	if h.rows.ReplaceCount() != 0 {
		t.Error("the discarded refresh was applied to the projection")
	}
	if projected := h.rows.Projected(key); !reflect.DeepEqual(projected, committed) {
		t.Errorf("projection = %#v, want the committed rows untouched", projected)
	}
}

func TestReadWorkflow_Load_DriftServesStaleNot409(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	key := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Q1"}
	h.snaps.Seed(key, sheet.Snapshot{
		ETag:      "good-etag",
		FetchedAt: time.Now().UTC().Add(-time.Hour),
		ExpiresAt: time.Now().UTC().Add(-time.Minute),
		Payload:   []byte(`[{"id":"a","name":"apple","qty":"3"}]`),
	})
	// the id column was deleted from the tab after it was published
	h.google.setRows(http.StatusOK, `{"values":[["name","qty"],["apple","3"]]}`)

	got, err := h.wf.Rows(t.Context(), sh)
	if err != nil {
		t.Fatalf("Rows err = %v, want the previous rows served stale rather than a conflict", err)
	}
	if !got.Stale || !got.Cached {
		t.Errorf("Cached/Stale = %v/%v, want true/true", got.Cached, got.Stale)
	}
	if got.ETag != "good-etag" {
		t.Errorf("ETag = %q, want the last good snapshot's", got.ETag)
	}
	if len(got.Values) != 1 || got.Values[0]["id"] != "a" {
		t.Errorf("Values = %#v, want the previous rows", got.Values)
	}
	state := h.rows.Contract(sh.ID)
	if state.OK || state.Reason == "" {
		t.Errorf("contract = %+v, want the drift persisted with a reason", state)
	}
	if h.rows.ReplaceCount() != 0 {
		t.Error("a drifted refresh replaced the projected rows")
	}
	if h.snaps.PutCount() != 0 {
		t.Error("a drifted refresh overwrote the last good snapshot")
	}
}

func TestReadWorkflow_Load_DriftWithNothingCachedStillServesTheTab(t *testing.T) {
	h := newReadHarness(t, harnessOpts{})
	sh, _ := h.seed(t, "Q1", sheet.VisibilityKey, 0)
	h.google.setRows(http.StatusOK, `{"values":[["name","qty"],["apple","3"]]}`)

	got, err := h.wf.Rows(t.Context(), sh)
	if err != nil {
		t.Fatalf("Rows err = %v, want a whole-tab read to survive a missing id column", err)
	}
	if got.Stale || len(got.Values) != 1 || got.Values[0]["name"] != "apple" {
		t.Errorf("Stale = %v, Values = %#v, want the fetched rows", got.Stale, got.Values)
	}
	state := h.rows.Contract(sh.ID)
	if state.OK || state.Reason == "" {
		t.Errorf("contract = %+v, want the drift persisted with a reason", state)
	}
	if h.rows.ReplaceCount() != 0 {
		t.Error("a drifted refresh replaced the projected rows")
	}
}
