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

func TestDiscordSink_Report_PostsPayload(t *testing.T) {
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
	s := NewDiscordSink(srv.URL, log)
	t.Cleanup(func() { _ = s.Close() })

	s.Report(context.Background(), &apperror.Incident{
		Code:      "altempl.unexpected",
		Message:   "boom",
		Cause:     errors.New("underlying"),
		RequestID: "req-99",
		TraceID:   "trace-99",
	})

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for webhook post")
	}
	mu.Lock()
	got := string(body)
	mu.Unlock()

	for _, want := range []string{"req-99", "trace-99", "altempl.unexpected", "boom", "underlying"} {
		if !strings.Contains(got, want) {
			t.Errorf("body missing %q; got %s", want, got)
		}
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if _, ok := payload["embeds"]; !ok {
		t.Errorf("discord payload missing embeds key: %s", got)
	}
}

func TestDiscordSink_Close_MakesReportNoOp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	s := NewDiscordSink(srv.URL, log)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s.Report(context.Background(), &apperror.Incident{Code: "x", Message: "y"})
	if got := s.droppedCount(); got != 0 {
		t.Errorf("after Close, Report should be a no-op; got dropped=%d", got)
	}
}

func TestDiscordSink_Report_EmptyURLIsNoOp(t *testing.T) {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	s := NewDiscordSink("", log)
	t.Cleanup(func() { _ = s.Close() })
	s.Report(context.Background(), &apperror.Incident{Code: "x", Message: "y"})
}
