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

func TestSlackSink_Report_PostsPayload(t *testing.T) {
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
	s := NewSlackSink(srv.URL, log)
	t.Cleanup(func() { _ = s.Close() })

	s.Report(context.Background(), &apperror.Incident{
		Code:      "altempl.unexpected",
		Message:   "boom",
		Cause:     errors.New("underlying"),
		RequestID: "req-42",
		TraceID:   "trace-42",
	})

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for webhook post")
	}

	mu.Lock()
	got := string(body)
	mu.Unlock()
	for _, want := range []string{"req-42", "trace-42", "altempl.unexpected", "boom", "underlying"} {
		if !strings.Contains(got, want) {
			t.Errorf("body missing %q; got %s", want, got)
		}
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if _, ok := payload["blocks"]; !ok {
		t.Errorf("slack payload missing blocks key: %s", got)
	}
}

func TestSlackSink_Report_Overflow_DropsAndDoesNotBlock(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	s := NewSlackSink(srv.URL, log)
	t.Cleanup(func() {
		close(release)
		_ = s.Close()
		srv.Close()
	})

	inc := &apperror.Incident{Code: "altempl.unexpected", Message: "x"}
	done := make(chan struct{})
	go func() {
		for i := 0; i < webhookQueueCap*3; i++ {
			s.Report(context.Background(), inc)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Report calls blocked; queue is not non-blocking")
	}
	if s.droppedCount() == 0 {
		t.Errorf("expected drops > 0, got 0")
	}
}

func TestSlackSink_Close_MakesReportNoOp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	s := NewSlackSink(srv.URL, log)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	s.Report(context.Background(), &apperror.Incident{Code: "x", Message: "y"})
	if got := s.droppedCount(); got != 0 {
		t.Errorf("after Close, Report should be a no-op with no drops; got dropped=%d", got)
	}
}

func TestSlackSink_Report_NilIncidentIsNoOp(t *testing.T) {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	s := NewSlackSink("http://example.invalid", log)
	t.Cleanup(func() { _ = s.Close() })
	s.Report(context.Background(), nil)
}

func TestSlackSink_Report_EmptyURLIsNoOp(t *testing.T) {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	s := NewSlackSink("", log)
	t.Cleanup(func() { _ = s.Close() })
	s.Report(context.Background(), &apperror.Incident{Code: "x", Message: "y"})
}
