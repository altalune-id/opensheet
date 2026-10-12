package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"altalune.id/opensheet/internal/web/middleware"
)

func TestRecoverJSON_TurnsPanicIntoJSON500(t *testing.T) {
	t.Parallel()
	rep := &stubReporter{}

	panicking := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { panic("boom") })
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/a/projects/b/posts", nil)
	middleware.RecoverJSON(rep.Unexpected)(panicking).ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status=%d, want 500", rr.Code)
	}
	if rep.calls != 1 {
		t.Errorf("reporter calls=%d, want 1", rep.calls)
	}
	if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("Content-Type=%q, want JSON", got)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"code":"internal"`) {
		t.Errorf("body=%q, want the JSON error envelope", body)
	}
	if strings.Contains(body, "boom") {
		t.Errorf("body leaked the panic value: %q", body)
	}
	if strings.Contains(body, "<") {
		t.Errorf("a machine surface must not render HTML: %q", body)
	}
}

func TestRecoverJSON_PassesThroughOnHappyPath(t *testing.T) {
	t.Parallel()
	rep := &stubReporter{}

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	middleware.RecoverJSON(rep.Unexpected)(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status=%d", rr.Code)
	}
	if rep.calls != 0 {
		t.Errorf("reporter should not fire on happy path, calls=%d", rep.calls)
	}
	if got := rr.Body.String(); got != "ok" {
		t.Errorf("body=%q", got)
	}
}
