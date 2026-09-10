package gsheet

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"altalune.id/opensheet/gworkspace"
)

type sentRequest struct {
	mu     sync.Mutex
	calls  int
	method string
	path   string
	query  string
	body   string
}

func (s *sentRequest) record(r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.method = r.Method
	s.path = r.URL.Path
	s.query = r.URL.RawQuery
	s.body = string(body)
}

func (s *sentRequest) snapshot() sentRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sentRequest{calls: s.calls, method: s.method, path: s.path, query: s.query, body: s.body}
}

func newTestWriter(t *testing.T, handle func(*http.Request) (int, string)) *Writer {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status, body := handle(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	wr, err := NewWriter(context.Background(), staticTokenSource{}, gworkspace.WithBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	return wr
}

func TestWriter_AppendSendsRawAndInsertsRows(t *testing.T) {
	t.Parallel()
	var sent sentRequest
	w := newTestWriter(t, func(r *http.Request) (int, string) {
		sent.record(r)
		return http.StatusOK, `{"updates":{"updatedRows":1}}`
	})

	n, err := w.Append(context.Background(), "FILE", "Rates", []any{"Aston", 1250000})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if n != 1 {
		t.Errorf("Append = %d, want 1", n)
	}

	got := sent.snapshot()
	if got.calls != 1 {
		t.Fatalf("Append made %d requests, want exactly 1", got.calls)
	}
	if got.method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.method)
	}
	if want := "/v4/spreadsheets/FILE/values/'Rates':append"; got.path != want {
		t.Errorf("path = %q, want %q", got.path, want)
	}
	if !strings.Contains(got.query, "valueInputOption=RAW") {
		t.Errorf("query = %q, want it to carry valueInputOption=RAW", got.query)
	}
	if !strings.Contains(got.query, "insertDataOption=INSERT_ROWS") {
		t.Errorf("query = %q, want it to carry insertDataOption=INSERT_ROWS", got.query)
	}
	if !strings.Contains(got.body, `1250000`) {
		t.Errorf("body = %q, want it to carry the numeric cell", got.body)
	}
	if strings.Contains(got.body, `"1250000"`) {
		t.Errorf("body = %q; a numeric cell must be sent as a JSON number, not a quoted string", got.body)
	}
}

// SECURITY: this is the control from spec 3.3. Under USER_ENTERED a caller could inject
// =Payroll!A1 to read another tab, or =IMPORTXML to exfiltrate through Google's servers.
func TestWriter_FormulaPrefixesAreSentAsLiteralTextUnderRaw(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"=Payroll!A1", "+1+1", "-1-1", `=IMPORTXML("http://x/","//a")`} {
		t.Run(v, func(t *testing.T) {
			t.Parallel()
			var sent sentRequest
			w := newTestWriter(t, func(r *http.Request) (int, string) {
				sent.record(r)
				return http.StatusOK, `{"updates":{"updatedRows":1}}`
			})

			if _, err := w.Append(context.Background(), "FILE", "Rates", []any{v}); err != nil {
				t.Fatalf("Append: %v", err)
			}

			got := sent.snapshot()
			if !strings.Contains(got.query, "valueInputOption=RAW") {
				t.Errorf("query = %q; RAW is the only thing stopping Google from evaluating %q as a formula", got.query, v)
			}
			if strings.Contains(got.query, "USER_ENTERED") {
				t.Errorf("query = %q, want no USER_ENTERED anywhere in the request", got.query)
			}
			encoded, err := json.Marshal(v)
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}
			if !strings.Contains(got.body, string(encoded)) {
				t.Errorf("body = %q, want the cell %s sent verbatim", got.body, encoded)
			}
		})
	}
}

func TestWriter_AppendToleratesAMissingUpdatesObject(t *testing.T) {
	t.Parallel()
	w := newTestWriter(t, func(*http.Request) (int, string) { return http.StatusOK, `{}` })

	n, err := w.Append(context.Background(), "FILE", "Rates", []any{"a"})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if n != 0 {
		t.Errorf("Append = %d, want 0 when Google omits the updates object", n)
	}
}

func TestWriter_AppendRejectsABlankTab(t *testing.T) {
	t.Parallel()
	var sent sentRequest
	w := newTestWriter(t, func(r *http.Request) (int, string) {
		sent.record(r)
		return http.StatusOK, `{}`
	})

	if _, err := w.Append(context.Background(), "FILE", "  ", []any{"a"}); !IsTabNotFoundError(err) {
		t.Fatalf("error = %v, want TabNotFoundError", err)
	}
	if got := sent.snapshot(); got.calls != 0 {
		t.Errorf("made %d requests, want none for a blank tab", got.calls)
	}
}

func TestWriter_UpdateRowSendsTheFullA1Range(t *testing.T) {
	t.Parallel()
	var sent sentRequest
	w := newTestWriter(t, func(r *http.Request) (int, string) {
		sent.record(r)
		return http.StatusOK, `{}`
	})

	if err := w.UpdateRow(context.Background(), "FILE", "Rates", 7, []any{"a", "b", "c"}); err != nil {
		t.Fatalf("UpdateRow: %v", err)
	}

	got := sent.snapshot()
	if got.method != http.MethodPut {
		t.Errorf("method = %q, want PUT", got.method)
	}
	if want := "/v4/spreadsheets/FILE/values/'Rates'!A7:C7"; got.path != want {
		t.Errorf("path = %q, want %q (quoted tab, start row, and the end column matching len(cells))", got.path, want)
	}
	if !strings.Contains(got.query, "valueInputOption=RAW") {
		t.Errorf("query = %q, want it to carry valueInputOption=RAW", got.query)
	}
}

// SECURITY: a tab named A'!A1:Z must not break out of the range expression a write targets.
func TestWriter_UpdateRowQuotesTheTabIntoTheRange(t *testing.T) {
	t.Parallel()
	var sent sentRequest
	w := newTestWriter(t, func(r *http.Request) (int, string) {
		sent.record(r)
		return http.StatusOK, `{}`
	})

	if err := w.UpdateRow(context.Background(), "FILE", `A'!A1:Z`, 2, []any{"a"}); err != nil {
		t.Fatalf("UpdateRow: %v", err)
	}

	got := sent.snapshot()
	if want := `/v4/spreadsheets/FILE/values/'A''!A1:Z'!A2:A2`; got.path != want {
		t.Errorf("path = %q, want %q", got.path, want)
	}
}

func TestWriter_UpdateRowRejectsANonPositiveRowBeforeCallingGoogle(t *testing.T) {
	t.Parallel()
	var sent sentRequest
	w := newTestWriter(t, func(r *http.Request) (int, string) {
		sent.record(r)
		return http.StatusOK, `{}`
	})

	for _, row := range []int{0, -1} {
		if err := w.UpdateRow(context.Background(), "FILE", "Rates", row, []any{"a"}); !IsInvalidRowIndexError(err) {
			t.Errorf("UpdateRow(row=%d) error = %v, want InvalidRowIndexError", row, err)
		}
	}
	if got := sent.snapshot(); got.calls != 0 {
		t.Errorf("made %d requests, want none: row 0 would otherwise render A0 and come back as an unparseable range", got.calls)
	}
}

func TestWriter_UpdateRowRejectsABlankTab(t *testing.T) {
	t.Parallel()
	w := newTestWriter(t, func(*http.Request) (int, string) { return http.StatusOK, `{}` })

	if err := w.UpdateRow(context.Background(), "FILE", " ", 1, []any{"a"}); !IsTabNotFoundError(err) {
		t.Fatalf("error = %v, want TabNotFoundError", err)
	}
}

func TestWriter_UpdateRangeSendsRawAndOneRequest(t *testing.T) {
	t.Parallel()
	var sent sentRequest
	w := newTestWriter(t, func(r *http.Request) (int, string) {
		sent.record(r)
		return http.StatusOK, `{}`
	})

	rows := [][]any{{"id"}, {"k3mQ8v"}, {"p9xR4j"}, {"zt4LpB"}}
	if err := w.UpdateRange(context.Background(), "FILE", "Rates", "C1:C4", rows); err != nil {
		t.Fatalf("UpdateRange: %v", err)
	}

	got := sent.snapshot()
	if got.calls != 1 {
		t.Fatalf("UpdateRange made %d requests, want exactly 1 for the whole span", got.calls)
	}
	if got.method != http.MethodPut {
		t.Errorf("method = %q, want PUT", got.method)
	}
	if want := "/v4/spreadsheets/FILE/values/'Rates'!C1:C4"; got.path != want {
		t.Errorf("path = %q, want %q", got.path, want)
	}
	// SECURITY: RAW is the formula-injection control — under USER_ENTERED a crafted existing cell would evaluate on write.
	if !strings.Contains(got.query, "valueInputOption=RAW") {
		t.Errorf("query = %q, want it to carry valueInputOption=RAW", got.query)
	}
	var payload struct {
		Values [][]any `json:"values"`
	}
	if err := json.Unmarshal([]byte(got.body), &payload); err != nil {
		t.Fatalf("decode body %q: %v", got.body, err)
	}
	if len(payload.Values) != 4 {
		t.Errorf("body carried %d rows, want 4", len(payload.Values))
	}
}

// SECURITY: a tab named A'!A1:Z must not break out of the range expression a write targets.
func TestWriter_UpdateRangeQuotesTheTabIntoTheRange(t *testing.T) {
	t.Parallel()
	var sent sentRequest
	w := newTestWriter(t, func(r *http.Request) (int, string) {
		sent.record(r)
		return http.StatusOK, `{}`
	})

	if err := w.UpdateRange(context.Background(), "FILE", `A'!A1:Z`, "B2", [][]any{{"x"}}); err != nil {
		t.Fatalf("UpdateRange: %v", err)
	}

	got := sent.snapshot()
	if want := `/v4/spreadsheets/FILE/values/'A''!A1:Z'!B2`; got.path != want {
		t.Errorf("path = %q, want %q", got.path, want)
	}
}

func TestWriter_UpdateRangeRejectsBadInputBeforeCallingGoogle(t *testing.T) {
	t.Parallel()
	var sent sentRequest
	w := newTestWriter(t, func(r *http.Request) (int, string) {
		sent.record(r)
		return http.StatusOK, `{}`
	})

	for _, span := range []string{"", "C", "C0", "A1:", "'Other'!A1", "A1:B2:C3", "a1:b2"} {
		if err := w.UpdateRange(context.Background(), "FILE", "Rates", span, [][]any{{"x"}}); !IsInvalidRangeError(err) {
			t.Errorf("UpdateRange(span=%q) error = %v, want InvalidRangeError", span, err)
		}
	}
	if err := w.UpdateRange(context.Background(), "FILE", "Rates", "C1:C4", nil); !IsInvalidRangeError(err) {
		t.Errorf("UpdateRange(no rows) error = %v, want InvalidRangeError", err)
	}
	if err := w.UpdateRange(context.Background(), "FILE", " ", "C1", [][]any{{"x"}}); !IsTabNotFoundError(err) {
		t.Errorf("UpdateRange(blank tab) error = %v, want TabNotFoundError", err)
	}
	if got := sent.snapshot(); got.calls != 0 {
		t.Errorf("made %d requests, want none — a refused span must never reach the user's spreadsheet", got.calls)
	}
}

func TestWriter_AddTabRejectsBadTitlesBeforeCallingGoogle(t *testing.T) {
	t.Parallel()
	var sent sentRequest
	w := newTestWriter(t, func(r *http.Request) (int, string) {
		sent.record(r)
		return http.StatusOK, `{}`
	})

	if err := w.AddTab(context.Background(), "FILE", "   "); !IsInvalidTabTitleError(err) {
		t.Errorf("blank title error = %v, want InvalidTabTitleError", err)
	}
	if err := w.AddTab(context.Background(), "FILE", strings.Repeat("x", 101)); !IsInvalidTabTitleError(err) {
		t.Errorf("over-long title error = %v, want InvalidTabTitleError", err)
	}
	if got := sent.snapshot(); got.calls != 0 {
		t.Errorf("made %d requests, want none: an invalid title must not reach Google", got.calls)
	}
}

func TestWriter_AddTabSendsAnAddSheetRequest(t *testing.T) {
	t.Parallel()
	var sent sentRequest
	w := newTestWriter(t, func(r *http.Request) (int, string) {
		sent.record(r)
		return http.StatusOK, `{}`
	})

	if err := w.AddTab(context.Background(), "FILE", "  Q2  "); err != nil {
		t.Fatalf("AddTab: %v", err)
	}

	got := sent.snapshot()
	if got.calls != 1 {
		t.Fatalf("AddTab made %d requests, want exactly 1", got.calls)
	}
	if want := "/v4/spreadsheets/FILE:batchUpdate"; got.path != want {
		t.Errorf("path = %q, want %q", got.path, want)
	}
	if !strings.Contains(got.body, `"addSheet"`) {
		t.Errorf("body = %q, want an addSheet request", got.body)
	}
	if !strings.Contains(got.body, `"title":"Q2"`) {
		t.Errorf("body = %q, want the trimmed title", got.body)
	}
}

func TestWriter_AddTabAcceptsTheHundredthCharacter(t *testing.T) {
	t.Parallel()
	w := newTestWriter(t, func(*http.Request) (int, string) { return http.StatusOK, `{}` })

	if err := w.AddTab(context.Background(), "FILE", strings.Repeat("x", 100)); err != nil {
		t.Fatalf("AddTab(100 chars): %v", err)
	}
}

func TestWriter_AddTabCountsRunesNotBytes(t *testing.T) {
	t.Parallel()
	w := newTestWriter(t, func(*http.Request) (int, string) { return http.StatusOK, `{}` })

	if err := w.AddTab(context.Background(), "FILE", strings.Repeat("é", 100)); err != nil {
		t.Fatalf("AddTab(100 runes, 200 bytes): %v", err)
	}
}

func TestWriter_TranslatesGoogleFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
		body   string
		check  func(error) bool
	}{
		{"403", http.StatusForbidden, `{"error":{"code":403,"message":"denied"}}`, gworkspace.IsPermissionDeniedError},
		{"404", http.StatusNotFound, `{"error":{"code":404,"message":"not found"}}`, gworkspace.IsNotFoundError},
		{"429", http.StatusTooManyRequests, `{"error":{"code":429,"message":"quota"}}`, gworkspace.IsQuotaExceededError},
		{"400 on a range is a missing tab", http.StatusBadRequest, `{"error":{"code":400,"message":"Unable to parse range"}}`, IsTabNotFoundError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newTestWriter(t, func(*http.Request) (int, string) { return tc.status, tc.body })

			if _, err := w.Append(context.Background(), "FILE", "Rates", []any{"a"}); !tc.check(err) {
				t.Errorf("Append error = %v (%T), want the matching typed error", err, err)
			}
			if err := w.UpdateRow(context.Background(), "FILE", "Rates", 1, []any{"a"}); !tc.check(err) {
				t.Errorf("UpdateRow error = %v (%T), want the matching typed error", err, err)
			}
		})
	}
}

func TestWriter_AddTabTranslatesGoogleFailures(t *testing.T) {
	t.Parallel()
	w := newTestWriter(t, func(*http.Request) (int, string) {
		return http.StatusForbidden, `{"error":{"code":403,"message":"denied"}}`
	})

	if err := w.AddTab(context.Background(), "FILE", "Q2"); !gworkspace.IsPermissionDeniedError(err) {
		t.Fatalf("error = %v, want PermissionDeniedError", err)
	}
}

func TestNewWriter_RejectsNilTokenSource(t *testing.T) {
	t.Parallel()
	if _, err := NewWriter(context.Background(), nil); err == nil {
		t.Fatal("NewWriter(nil) succeeded")
	}
}

// NOTE: the embedded *Client is method-set separation, so a write workflow that must read first uses one object.
func TestWriter_CanReadThroughTheEmbeddedClient(t *testing.T) {
	t.Parallel()
	w := newTestWriter(t, func(*http.Request) (int, string) {
		return http.StatusOK, `{"values":[["a"],["1"]]}`
	})

	tbl, err := w.Table(context.Background(), "FILE", "Rates")
	if err != nil {
		t.Fatalf("Table: %v", err)
	}
	if len(tbl.Headers) != 1 || tbl.Headers[0] != "a" {
		t.Fatalf("Headers = %q, want [a]", tbl.Headers)
	}
}

func TestWriterFactory_WrapsNewWriter(t *testing.T) {
	t.Parallel()
	var f WriterFactory = func(ctx context.Context, ts oauth2.TokenSource) (*Writer, error) {
		return NewWriter(ctx, ts)
	}

	w, err := f(context.Background(), staticTokenSource{})
	if err != nil {
		t.Fatalf("WriterFactory: %v", err)
	}
	if w == nil {
		t.Fatal("WriterFactory returned a nil Writer")
	}
}

// SECURITY: a read client silently gaining write scope would let read-path code mutate a spreadsheet.
func TestScopes_ReadOnlyAndReadWriteAreDistinct(t *testing.T) {
	t.Parallel()
	if ScopeReadOnly != "https://www.googleapis.com/auth/spreadsheets.readonly" {
		t.Errorf("ScopeReadOnly = %q, want the readonly Sheets scope", ScopeReadOnly)
	}
	if ScopeReadWrite != "https://www.googleapis.com/auth/spreadsheets" {
		t.Errorf("ScopeReadWrite = %q, want the read-write Sheets scope", ScopeReadWrite)
	}
	if ScopeReadOnly == ScopeReadWrite {
		t.Fatal("ScopeReadOnly and ScopeReadWrite are the same scope")
	}
}
