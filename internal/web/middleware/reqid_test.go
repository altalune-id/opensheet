package middleware_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"altalune.id/opensheet/internal/web/middleware"
	"altalune.id/opensheet/logger"
	"altalune.id/opensheet/reqid"
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

// NOTE: reusing the listener's BaseContext id would make every response in a deployment share one id.
func TestRequestIDDoesNotInheritTheBaseContextID(t *testing.T) {
	base := reqid.WithContext(t.Context(), "process-wide-id")

	h := middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	seen := map[string]bool{}
	for range 3 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil).WithContext(base))
		got := rec.Header().Get(reqid.Header)
		if got == "process-wide-id" {
			t.Fatalf("response reused the base context id %q", got)
		}
		if seen[got] {
			t.Fatalf("request id %q was reused across requests", got)
		}
		seen[got] = true
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil).WithContext(base)
	req.Header.Set(reqid.Header, "caller-supplied")
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get(reqid.Header); got != "caller-supplied" {
		t.Fatalf("inbound id = %q, want %q", got, "caller-supplied")
	}
}

func TestRequestID_SanitizesInbound(t *testing.T) {
	t.Parallel()
	const uuidV7 = "0192a3f1-c7c1-7c1d-b1d1-abcdef012345"
	for _, tt := range []struct {
		name       string
		inbound    string
		sets       bool
		propagated bool
	}{
		{name: "uuid v7 is propagated", inbound: uuidV7, sets: true, propagated: true},
		{name: "absent mints a fresh id", sets: false},
		{name: "empty mints a fresh id", inbound: "", sets: true},
		{name: "oversized is replaced", inbound: strings.Repeat("A", reqid.MaxLength+1), sets: true},
		{name: "control characters are replaced", inbound: "abc\x00\x1bdef", sets: true},
		{name: "newline injection is replaced", inbound: "abc\n{\"msg\":\"forged\"}", sets: true},
		{name: "whitespace is replaced", inbound: "abc def", sets: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			log := slog.New(logger.ContextHandler{Handler: slog.NewJSONHandler(&buf, nil)})

			var inCtx string
			next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				inCtx = reqid.FromContext(r.Context())
			})
			h := middleware.RequestID(middleware.RequestLog(log)(next))

			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			if tt.sets {
				req.Header.Set(reqid.Header, tt.inbound)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			echoed := rec.Header().Get(reqid.Header)
			if tt.propagated {
				if echoed != tt.inbound {
					t.Errorf("echoed = %q, want the inbound %q", echoed, tt.inbound)
				}
			}
			if !tt.propagated {
				if echoed == tt.inbound {
					t.Fatalf("echoed the unsafe inbound value %q verbatim", tt.inbound)
				}
				if !uuidV7Pattern.MatchString(echoed) {
					t.Fatalf("echoed = %q, want a freshly minted UUIDv7", echoed)
				}
			}
			if inCtx != echoed {
				t.Errorf("ctx id = %q, echoed = %q; they must agree", inCtx, echoed)
			}

			var line map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &line); err != nil {
				t.Fatalf("log line is not one JSON object: %v (%q)", err, buf.String())
			}
			if got, _ := line["request_id"].(string); got != echoed {
				t.Errorf("logged request_id = %q, want %q", got, echoed)
			}
			if tt.sets && !tt.propagated && strings.Contains(buf.String(), "forged") {
				t.Errorf("unsafe inbound value reached the log: %q", buf.String())
			}
		})
	}
}

var uuidV7Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
