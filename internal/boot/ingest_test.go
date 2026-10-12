package boot_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func serveIngest(t *testing.T, f *edgeFixture, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	f.srv.Web.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader("{}")))
	return rec
}

func ingestPaths() []string {
	return []string{
		"/hooks/",
		"/hooks/someprovider",
		"/hooks/someprovider/events",
		"/hooks/someprovider/events/nested",
	}
}

// TestIngestIsMountedAndFailsClosed proves /hooks/ routes to S4 and, with no provider registered, refuses every delivery.
func TestIngestIsMountedAndFailsClosed(t *testing.T) {
	f := newEdgeFixture(t)

	for _, path := range ingestPaths() {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
			t.Run(method+" "+path, func(t *testing.T) {
				rec := serveIngest(t, f, method, path)
				body := rec.Body.String()

				require.Equal(t, http.StatusNotFound, rec.Code,
					"ingest must refuse a delivery no verifier claims; body=%s", body)
				require.NotContains(t, body, "<html",
					"R6: an HTML error page reached a machine surface")

				var envelope struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				}
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope),
					"R6: the ingest error body is not the declared envelope: %s", body)
				require.Equal(t, "not_found", envelope.Code, "body=%s", body)
			})
		}
	}
}

// TestIngestIsNotTheConsoleChain enforces R5: no CSP header or session cookie on an ingest response.
func TestIngestIsNotTheConsoleChain(t *testing.T) {
	f := newEdgeFixture(t)

	console := serveIngest(t, f, http.MethodGet, "/login")
	require.NotEmpty(t, console.Header().Get("Content-Security-Policy"),
		"the console chain must set CSP, or the assertions below prove nothing")

	for _, path := range ingestPaths() {
		t.Run(path, func(t *testing.T) {
			rec := serveIngest(t, f, http.MethodPost, path)

			require.Empty(t, rec.Header().Get("Content-Security-Policy"),
				"R5: ingest ran the console chain — CSP is an SSR concern, and a machine surface renders no HTML")
			require.Empty(t, rec.Header().Values("Set-Cookie"),
				"R5: ingest touched the session — a machine surface issues no cookies")
			require.True(t, strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json"),
				"R6: ingest Content-Type = %q, want application/json", rec.Header().Get("Content-Type"))
		})
	}

	missing := serveIngest(t, f, http.MethodGet, "/nosuchconsolepage")
	require.NotContains(t, missing.Header().Get("Content-Type"), "application/json",
		"R5/R6 are per surface: the console must not share the ingest error shape")
}
