package middleware_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/web/middleware"
)

// stubReporter counts calls and returns a fixed AppError.
type stubReporter struct{ calls int }

func (s *stubReporter) Unexpected(_ context.Context, message string, _ error, _ ...any) *apperror.AppError {
	s.calls++
	return apperror.New(apperror.CodeUnexpectedError, message, codes.Internal, nil)
}

func TestRecover_TurnsPanicInto500(t *testing.T) {
	t.Parallel()
	rep := &stubReporter{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpl := middleware.LogError{Log: log}

	panicking := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("boom")
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	middleware.Recover(rep.Unexpected, tmpl)(panicking).ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status=%d, want 500", rr.Code)
	}
	if rep.calls != 1 {
		t.Errorf("reporter calls=%d, want 1", rep.calls)
	}
}

func TestRecover_PassesThroughOnHappyPath(t *testing.T) {
	t.Parallel()
	rep := &stubReporter{}
	tmpl := middleware.LogError{}

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	middleware.Recover(rep.Unexpected, tmpl)(next).ServeHTTP(rr, req)

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

func TestErrorTemplate_HTMXFragment(t *testing.T) {
	t.Parallel()
	tmpl := middleware.TemplateErrorPage{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("HX-Request", "true")

	err := apperror.New(apperror.CodeUnexpectedError, "kaboom", codes.Internal, nil)
	if renderErr := tmpl.RenderError(rr, req, err); renderErr != nil {
		t.Fatalf("RenderError: %v", renderErr)
	}
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status=%d, want 500", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `class="alt-error"`) {
		t.Errorf("body missing alt-error marker: %q", body)
	}
	if !strings.Contains(body, "kaboom") {
		t.Errorf("body missing message: %q", body)
	}
	if strings.Contains(body, "<html") || strings.Contains(body, "<!doctype") {
		t.Errorf("HTMX fragment should not include full page markup: %q", body)
	}
}

func TestErrorTemplate_FullHTML(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	tmpl := middleware.LogError{Log: log}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)

	err := apperror.New(apperror.CodeUnexpectedError, "kaboom", codes.Internal, nil)
	if renderErr := tmpl.RenderError(rr, req, err); renderErr != nil {
		t.Fatalf("RenderError: %v", renderErr)
	}
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status=%d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "kaboom") {
		t.Errorf("body missing message: %q", rr.Body.String())
	}
	if !strings.Contains(buf.String(), "web.error") {
		t.Errorf("log missing web.error entry: %q", buf.String())
	}
}
