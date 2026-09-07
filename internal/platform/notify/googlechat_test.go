package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"altalune.id/template/internal/apperror"
)

func TestGoogleChatSink_Report_PostsPayload(t *testing.T) {
	var (
		mu   sync.Mutex
		body []byte
		done = make(chan struct{})
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		body = b
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		close(done)
	}))
	defer srv.Close()

	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	s := NewGoogleChatSink(srv.URL, log)
	t.Cleanup(func() { _ = s.Close() })

	s.Report(context.Background(), &apperror.Incident{
		Code:      "altempl.unexpected",
		Message:   "kaput",
		Cause:     errors.New("underlying"),
		RequestID: "req-77",
		TraceID:   "trace-77",
	})

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for webhook post")
	}
	mu.Lock()
	got := string(body)
	mu.Unlock()

	for _, want := range []string{"req-77", "trace-77", "altempl.unexpected", "kaput", "underlying"} {
		if !strings.Contains(got, want) {
			t.Errorf("body missing %q; got %s", want, got)
		}
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if _, ok := payload["cards"]; !ok {
		t.Errorf("googlechat payload missing cards key: %s", got)
	}
}

func TestGoogleChatSink_Close_MakesReportNoOp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	s := NewGoogleChatSink(srv.URL, log)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s.Report(context.Background(), &apperror.Incident{Code: "x", Message: "y"})
	if got := s.droppedCount(); got != 0 {
		t.Errorf("after Close, Report should be a no-op; got dropped=%d", got)
	}
}
