package middleware_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"altalune.id/template/internal/web/middleware"
)

func TestRequestLog_CapturesFields(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("brew"))
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/things/42", nil)
	req.RemoteAddr = "10.0.0.1:1234"

	middleware.RequestLog(log)(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusTeapot {
		t.Fatalf("status=%d", rr.Code)
	}
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("unmarshal log line: %v (raw=%s)", err, buf.String())
	}
	if line["method"] != http.MethodPost {
		t.Errorf("method=%v", line["method"])
	}
	if line["path"] != "/things/42" {
		t.Errorf("path=%v", line["path"])
	}
	if line["status"] != float64(http.StatusTeapot) {
		t.Errorf("status=%v", line["status"])
	}
	if line["remote_addr"] != "10.0.0.1:1234" {
		t.Errorf("remote_addr=%v", line["remote_addr"])
	}
	if _, ok := line["duration_ms"]; !ok {
		t.Errorf("expected duration_ms field")
	}
}

func TestRequestLog_DefaultsStatusOK(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	middleware.RequestLog(log)(next).ServeHTTP(rr, req)

	var line map[string]any
	_ = json.Unmarshal(buf.Bytes(), &line)
	if line["status"] != float64(http.StatusOK) {
		t.Errorf("status=%v, want 200", line["status"])
	}
}
