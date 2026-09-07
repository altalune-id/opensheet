package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"altalune.id/template/internal/web/middleware"
	"altalune.id/template/reqid"
)

func TestRequestID_MintsOneWhenMissing(t *testing.T) {
	t.Parallel()
	var seen string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = reqid.FromContext(r.Context())
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	middleware.RequestID(next).ServeHTTP(rr, req)

	if seen == "" {
		t.Error("expected ctx to carry a fresh request id")
	}
	got := rr.Header().Get(reqid.Header)
	if got == "" || got != seen {
		t.Errorf("response header=%q ctx=%q", got, seen)
	}
}

func TestRequestID_PropagatesInbound(t *testing.T) {
	t.Parallel()
	const inbound = "0192a3f1-c7c1-7c1d-b1d1-abcdef012345"
	var seen string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = reqid.FromContext(r.Context())
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(reqid.Header, inbound)
	middleware.RequestID(next).ServeHTTP(rr, req)

	if seen != inbound {
		t.Errorf("ctx id=%q, want %q", seen, inbound)
	}
	if got := rr.Header().Get(reqid.Header); got != inbound {
		t.Errorf("response header=%q, want %q", got, inbound)
	}
}
