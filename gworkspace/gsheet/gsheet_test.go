package gsheet

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"altalune.id/opensheet/gworkspace"
)

type staticTokenSource struct{}

func (staticTokenSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: "dummy", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)}, nil
}

func newTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	c, err := New(context.Background(), staticTokenSource{}, gworkspace.WithBaseURL(baseURL))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func fakeSheets(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, body := range routes {
		b := body
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(b))
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func fakeStatus(t *testing.T, status int, body string, header map[string]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		for k, v := range header {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestRows_KeysByHeaderRow(t *testing.T) {
	srv := fakeSheets(t, map[string]string{
		"/v4/spreadsheets/FILE/values/": `{"values":[["name","qty"],["apple","3"],["pear","5"]]}`,
	})
	c := newTestClient(t, srv.URL)

	rows, warnings, err := c.Rows(context.Background(), "FILE", "Sheet1")
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	want := []Row{{"name": "apple", "qty": "3"}, {"name": "pear", "qty": "5"}}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d", len(rows), len(want))
	}
	for i := range want {
		for k, v := range want[i] {
			if rows[i][k] != v {
				t.Errorf("rows[%d][%q] = %q, want %q", i, k, rows[i][k], v)
			}
		}
	}
}

func TestRows_BlankAndDuplicateHeaders(t *testing.T) {
	srv := fakeSheets(t, map[string]string{
		"/v4/spreadsheets/FILE/values/": `{"values":[["name","","name"],["a","b","c"]]}`,
	})
	c := newTestClient(t, srv.URL)

	rows, warnings, err := c.Rows(context.Background(), "FILE", "Sheet1")
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	got := rows[0]
	if got["name"] != "a" || got["col_2"] != "b" || got["name_2"] != "c" {
		t.Fatalf("row = %#v, want name=a col_2=b name_2=c", got)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v, want 2 (one blank, one duplicate)", warnings)
	}
}

func TestRows_EmptySheetReturnsNoRowsNotError(t *testing.T) {
	srv := fakeSheets(t, map[string]string{
		"/v4/spreadsheets/FILE/values/": `{}`,
	})
	c := newTestClient(t, srv.URL)

	rows, _, err := c.Rows(context.Background(), "FILE", "Sheet1")
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("got %d rows, want 0", len(rows))
	}
}

func TestRows_HeaderOnlySheetReturnsNoRows(t *testing.T) {
	srv := fakeSheets(t, map[string]string{
		"/v4/spreadsheets/FILE/values/": `{"values":[["name","qty"]]}`,
	})
	c := newTestClient(t, srv.URL)

	rows, warnings, err := c.Rows(context.Background(), "FILE", "Sheet1")
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(rows) != 0 || len(warnings) != 0 {
		t.Fatalf("rows = %v, warnings = %v; want empty", rows, warnings)
	}
}

func TestRows_CoercesCellTypesAndShortRows(t *testing.T) {
	srv := fakeSheets(t, map[string]string{
		"/v4/spreadsheets/FILE/values/": `{"values":[["a","b","c"],[1.5,true,null],["only"]]}`,
	})
	c := newTestClient(t, srv.URL)

	rows, _, err := c.Rows(context.Background(), "FILE", "Sheet1")
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0]["a"] != "1.5" || rows[0]["b"] != "true" || rows[0]["c"] != "" {
		t.Errorf("rows[0] = %#v", rows[0])
	}
	if rows[1]["a"] != "only" || rows[1]["b"] != "" {
		t.Errorf("rows[1] = %#v", rows[1])
	}
}

func TestRows_RejectsBlankTab(t *testing.T) {
	srv := fakeSheets(t, map[string]string{"/v4/spreadsheets/FILE/values/": `{}`})
	c := newTestClient(t, srv.URL)

	if _, _, err := c.Rows(context.Background(), "FILE", "  "); !IsTabNotFoundError(err) {
		t.Fatalf("error = %v, want TabNotFoundError", err)
	}
}

func TestTabs_AndFirstTab(t *testing.T) {
	srv := fakeSheets(t, map[string]string{
		"/v4/spreadsheets/FILE": `{"properties":{"title":"Prices"},"sheets":[{"properties":{"title":"Q1"}},{"properties":{"title":"Q2"}}]}`,
	})
	c := newTestClient(t, srv.URL)

	tabs, err := c.Tabs(context.Background(), "FILE")
	if err != nil {
		t.Fatalf("Tabs: %v", err)
	}
	if len(tabs) != 2 || tabs[0] != "Q1" || tabs[1] != "Q2" {
		t.Fatalf("Tabs = %v, want [Q1 Q2]", tabs)
	}
	first, err := c.FirstTab(context.Background(), "FILE")
	if err != nil || first != "Q1" {
		t.Fatalf("FirstTab = %q, %v; want Q1", first, err)
	}
	title, err := c.Title(context.Background(), "FILE")
	if err != nil || title != "Prices" {
		t.Fatalf("Title = %q, %v; want Prices", title, err)
	}
}

func TestFirstTab_NoTabsIsTabNotFound(t *testing.T) {
	srv := fakeSheets(t, map[string]string{
		"/v4/spreadsheets/FILE": `{"properties":{"title":"Empty"},"sheets":[]}`,
	})
	c := newTestClient(t, srv.URL)

	if _, err := c.FirstTab(context.Background(), "FILE"); !IsTabNotFoundError(err) {
		t.Fatalf("error = %v, want TabNotFoundError", err)
	}
}

func TestMeta_TranslatesFailures(t *testing.T) {
	srv := fakeStatus(t, http.StatusForbidden, `{"error":{"code":403,"message":"denied"}}`, nil)
	c := newTestClient(t, srv.URL)

	if _, err := c.Tabs(context.Background(), "FILE"); !gworkspace.IsPermissionDeniedError(err) {
		t.Errorf("Tabs error = %v, want PermissionDeniedError", err)
	}
	if _, err := c.Title(context.Background(), "FILE"); !gworkspace.IsPermissionDeniedError(err) {
		t.Errorf("Title error = %v, want PermissionDeniedError", err)
	}
	if _, err := c.FirstTab(context.Background(), "FILE"); !gworkspace.IsPermissionDeniedError(err) {
		t.Errorf("FirstTab error = %v, want PermissionDeniedError", err)
	}
}

func TestErrorTranslation(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		check  func(error) bool
	}{
		{"404", http.StatusNotFound, `{"error":{"code":404,"message":"not found"}}`, gworkspace.IsNotFoundError},
		{"403", http.StatusForbidden, `{"error":{"code":403,"message":"denied"}}`, gworkspace.IsPermissionDeniedError},
		{"429", http.StatusTooManyRequests, `{"error":{"code":429,"message":"quota"}}`, gworkspace.IsQuotaExceededError},
		{"503", http.StatusServiceUnavailable, `{"error":{"code":503,"message":"down"}}`, gworkspace.IsUnavailableError},
		{"401", http.StatusUnauthorized, `{"error":{"code":401,"message":"invalid_grant"}}`, gworkspace.IsAuthExpiredError},
		{"400", http.StatusBadRequest, `{"error":{"code":400,"message":"Unable to parse range"}}`, IsTabNotFoundError},
		{"500", http.StatusInternalServerError, `{"error":{"code":500,"message":"boom"}}`, gworkspace.IsUnavailableError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := fakeStatus(t, tc.status, tc.body, nil)
			c := newTestClient(t, srv.URL)

			_, _, err := c.Rows(context.Background(), "FILE", "Sheet1")
			if !tc.check(err) {
				t.Fatalf("error = %v (%T), want the matching typed error", err, err)
			}
		})
	}
}

func TestRows_TransportFailureIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	c := newTestClient(t, srv.URL)
	srv.Close()

	if _, _, err := c.Rows(context.Background(), "FILE", "Sheet1"); !gworkspace.IsUnavailableError(err) {
		t.Fatalf("error = %v, want UnavailableError", err)
	}
}

func TestQuotaExceeded_CarriesRetryAfter(t *testing.T) {
	srv := fakeStatus(t, http.StatusTooManyRequests,
		`{"error":{"code":429,"message":"quota"}}`, map[string]string{"Retry-After": "30"})
	c := newTestClient(t, srv.URL)

	_, _, err := c.Rows(context.Background(), "FILE", "Sheet1")
	quota, ok := errors.AsType[*gworkspace.QuotaExceededError](err)
	if !ok {
		t.Fatalf("error = %v, want QuotaExceededError", err)
	}
	if quota.RetryAfter != 30*time.Second {
		t.Fatalf("RetryAfter = %v, want 30s", quota.RetryAfter)
	}
}

// SECURITY: a tab named A'!A1:Z must not be able to break out of the A1 range expression.
func TestQuoteRange_EscapesEmbeddedSingleQuotes(t *testing.T) {
	tests := map[string]string{
		"Sheet1":    "'Sheet1'",
		"A'!A1:Z":   "'A''!A1:Z'",
		"it's":      "'it''s'",
		"'":         "''''",
		"Q1 Report": "'Q1 Report'",
	}
	for in, want := range tests {
		if got := quoteRange(in); got != want {
			t.Errorf("quoteRange(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRows_SendsQuotedRangeOnTheWire(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"values":[["a"],["1"]]}`))
	}))
	t.Cleanup(srv.Close)
	c := newTestClient(t, srv.URL)

	if _, _, err := c.Rows(context.Background(), "FILE", `A'!A1:Z`); err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if !strings.HasSuffix(gotPath, `/values/'A''!A1:Z'`) {
		t.Fatalf("request path = %q, want it to end with the escaped range", gotPath)
	}
}

func TestNew_RejectsNilTokenSource(t *testing.T) {
	if _, err := New(context.Background(), nil); err == nil {
		t.Fatal("New(nil) succeeded")
	}
}

func TestNormalizeHeaders(t *testing.T) {
	names, warnings := normalizeHeaders([]string{" name ", "", "name", "name", ""})
	want := []string{"name", "col_2", "name_2", "name_3", "col_5"}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("names[%d] = %q, want %q", i, names[i], want[i])
		}
	}
	if len(warnings) != 4 {
		t.Fatalf("warnings = %v, want 4", warnings)
	}
}

func TestMeta_TolerateMissingProperties(t *testing.T) {
	srv := fakeSheets(t, map[string]string{
		"/v4/spreadsheets/FILE": `{"sheets":[{},{"properties":{"title":"Q2"}}]}`,
	})
	c := newTestClient(t, srv.URL)

	title, err := c.Title(context.Background(), "FILE")
	if err != nil || title != "" {
		t.Fatalf("Title = %q, %v; want empty", title, err)
	}
	tabs, err := c.Tabs(context.Background(), "FILE")
	if err != nil {
		t.Fatalf("Tabs: %v", err)
	}
	if len(tabs) != 1 || tabs[0] != "Q2" {
		t.Fatalf("Tabs = %v, want [Q2]", tabs)
	}
}

func TestCellString_FallsBackToFmt(t *testing.T) {
	if got := cellString([]any{1}); got != "[1]" {
		t.Fatalf("cellString = %q, want %q", got, "[1]")
	}
}

func TestWithTimeout_BoundsTheCall(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"values":[["a"],["b"]]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c, err := New(context.Background(), staticTokenSource{},
		gworkspace.WithBaseURL(srv.URL), gworkspace.WithTimeout(20*time.Millisecond))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, _, err := c.Rows(context.Background(), "FILE", "Sheet1"); err == nil {
		t.Fatal("Rows succeeded despite a 20ms timeout against a 300ms server")
	}
}

func TestRows_DropsCellsBeyondTheHeader(t *testing.T) {
	srv := fakeSheets(t, map[string]string{
		"/v4/spreadsheets/FILE/values/": `{"values":[["a"],["1","2","3"]]}`,
	})
	c := newTestClient(t, srv.URL)

	rows, _, err := c.Rows(context.Background(), "FILE", "Sheet1")
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(rows) != 1 || len(rows[0]) != 1 || rows[0]["a"] != "1" {
		t.Fatalf("rows = %#v, want one row keyed only by the single header", rows)
	}
}

func valuesClient(t *testing.T, body string) *Client {
	t.Helper()
	srv := fakeSheets(t, map[string]string{"/v4/spreadsheets/FILE/values/": body})
	return newTestClient(t, srv.URL)
}

func TestClient_TableReturnsRawHeadersAndOrderedRows(t *testing.T) {
	t.Parallel()
	c := valuesClient(t, `{"values":[[" ID ","Name","id"],["1","ada"],["2","bob","x"]]}`)

	got, err := c.Table(context.Background(), "FILE", "Rates")
	if err != nil {
		t.Fatalf("Table: %v", err)
	}
	wantHeaders := []string{" ID ", "Name", "id"}
	if !slices.Equal(got.Headers, wantHeaders) {
		t.Errorf("Headers = %q, want %q (raw: normalizeHeaders would rename the second id and trim nothing)", got.Headers, wantHeaders)
	}
	wantRows := [][]string{{"1", "ada"}, {"2", "bob", "x"}}
	if !slices.EqualFunc(got.Rows, wantRows, slices.Equal[[]string]) {
		t.Errorf("Rows = %q, want %q (each row keeps its own width: no padding, no truncation)", got.Rows, wantRows)
	}
}

func TestClient_TableRejectsAnEmptyTab(t *testing.T) {
	t.Parallel()
	c := valuesClient(t, `{"values":[]}`)

	if _, err := c.Table(context.Background(), "FILE", "  "); !IsTabNotFoundError(err) {
		t.Fatalf("error = %v, want TabNotFoundError", err)
	}
}

func TestClient_TableEmptySheetHasNilHeaders(t *testing.T) {
	t.Parallel()
	c := valuesClient(t, `{"values":[]}`)

	got, err := c.Table(context.Background(), "FILE", "Rates")
	if err != nil {
		t.Fatalf("Table: %v", err)
	}
	if got.Headers != nil || got.Rows != nil {
		t.Fatalf("Table = %#v, want the zero Table for an empty sheet", got)
	}
}

func TestClient_TableHeaderOnlySheetHasNonNilEmptyHeaders(t *testing.T) {
	t.Parallel()
	c := valuesClient(t, `{"values":[[]]}`)

	got, err := c.Table(context.Background(), "FILE", "Rates")
	if err != nil {
		t.Fatalf("Table: %v", err)
	}
	if got.Headers == nil {
		t.Fatal("Headers = nil for a present-but-empty header row; nil is reserved for the empty sheet")
	}
	if len(got.Headers) != 0 {
		t.Fatalf("Headers = %q, want empty", got.Headers)
	}
}

func TestClient_RowsBehaviourIsUnchangedByTheTableRefactor(t *testing.T) {
	t.Parallel()
	t.Run("empty sheet returns nil rows", func(t *testing.T) {
		t.Parallel()
		c := valuesClient(t, `{"values":[]}`)
		rows, warns, err := c.Rows(context.Background(), "FILE", "Rates")
		if err != nil {
			t.Fatalf("Rows: %v", err)
		}
		if rows != nil || warns != nil {
			t.Fatalf("rows = %#v, warnings = %#v; want nil, nil", rows, warns)
		}
	})
	t.Run("header only returns non-nil empty", func(t *testing.T) {
		t.Parallel()
		c := valuesClient(t, `{"values":[["a","b"]]}`)
		rows, _, err := c.Rows(context.Background(), "FILE", "Rates")
		if err != nil {
			t.Fatalf("Rows: %v", err)
		}
		if rows == nil {
			t.Fatal("rows = nil for a header-only sheet, want a non-nil empty slice")
		}
		if len(rows) != 0 {
			t.Fatalf("rows = %#v, want empty", rows)
		}
	})
	t.Run("a present but empty header row returns non-nil empty", func(t *testing.T) {
		t.Parallel()
		c := valuesClient(t, `{"values":[[]]}`)
		rows, _, err := c.Rows(context.Background(), "FILE", "Rates")
		if err != nil {
			t.Fatalf("Rows: %v", err)
		}
		if rows == nil {
			t.Fatal("rows = nil; only an absent header row may answer nil, so the empty-sheet test must discriminate on nil headers, not on their length")
		}
		if len(rows) != 0 {
			t.Fatalf("rows = %#v, want empty", rows)
		}
	})
	t.Run("short row omits trailing keys", func(t *testing.T) {
		t.Parallel()
		c := valuesClient(t, `{"values":[["a","b"],["1"]]}`)
		rows, _, err := c.Rows(context.Background(), "FILE", "Rates")
		if err != nil {
			t.Fatalf("Rows: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("got %d rows, want 1", len(rows))
		}
		if _, present := rows[0]["b"]; present {
			t.Errorf("rows[0] = %#v; a missing cell must be an absent key, not an empty string", rows[0])
		}
	})
	t.Run("extra cells are dropped", func(t *testing.T) {
		t.Parallel()
		c := valuesClient(t, `{"values":[["a"],["1","2"]]}`)
		rows, _, err := c.Rows(context.Background(), "FILE", "Rates")
		if err != nil {
			t.Fatalf("Rows: %v", err)
		}
		if len(rows) != 1 || len(rows[0]) != 1 {
			t.Fatalf("rows = %#v, want one row keyed only by the single header", rows)
		}
	})
}

func TestClient_RowsMakesOneValuesCallPerRead(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"values":[["a","b"],["1","2"]]}`))
	}))
	t.Cleanup(srv.Close)
	c := newTestClient(t, srv.URL)

	if _, _, err := c.Rows(context.Background(), "FILE", "Rates"); err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("Rows made %d requests, want exactly 1", got)
	}
	calls.Store(0)
	if _, err := c.Table(context.Background(), "FILE", "Rates"); err != nil {
		t.Fatalf("Table: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("Table made %d requests, want exactly 1", got)
	}
}
