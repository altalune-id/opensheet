package notify

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/mailer"
)

type fakeMailer struct {
	mu       sync.Mutex
	sent     []sentEmail
	done     chan struct{}
	blockCh  chan struct{}
	errOnce  error
	sentOnce bool
}

type sentEmail struct {
	to      string
	from    string
	subject string
	body    string
}

func newFakeMailer() *fakeMailer {
	return &fakeMailer{done: make(chan struct{}, 16)}
}

func (m *fakeMailer) Send(ctx context.Context, msg mailer.Message) error {
	if m.blockCh != nil {
		<-m.blockCh
	}
	m.mu.Lock()
	m.sent = append(m.sent, sentEmail{to: msg.To, from: msg.From, subject: msg.Subject, body: msg.TextBody})
	var err error
	if !m.sentOnce && m.errOnce != nil {
		err = m.errOnce
		m.sentOnce = true
	}
	m.mu.Unlock()
	select {
	case m.done <- struct{}{}:
	default:
	}
	_ = ctx
	return err
}

func (m *fakeMailer) snapshot() []sentEmail {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]sentEmail, len(m.sent))
	copy(out, m.sent)
	return out
}

func TestEmailSink_Report_SendsWithCorrectBody(t *testing.T) {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	m := newFakeMailer()
	s := NewEmailSink(m, []string{"ops@example.com"}, "alerts@example.com", log)
	t.Cleanup(func() { _ = s.Close() })

	s.Report(context.Background(), &apperror.Incident{
		Code:      "altempl.unexpected",
		Message:   "boom",
		Cause:     errors.New("underlying"),
		RequestID: "req-e1",
		TraceID:   "trace-e1",
	})
	select {
	case <-m.done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for send")
	}

	got := m.snapshot()
	if len(got) != 1 {
		t.Fatalf("want 1 send, got %d", len(got))
	}
	if got[0].to != "ops@example.com" {
		t.Errorf("to = %q", got[0].to)
	}
	if got[0].from != "alerts@example.com" {
		t.Errorf("from = %q", got[0].from)
	}
	if !strings.Contains(got[0].subject, "altempl.unexpected") {
		t.Errorf("subject = %q", got[0].subject)
	}
	for _, want := range []string{"req-e1", "trace-e1", "altempl.unexpected", "boom", "underlying"} {
		if !strings.Contains(got[0].body, want) {
			t.Errorf("body missing %q; got %s", want, got[0].body)
		}
	}
}

func TestEmailSink_Report_NoRecipientsIsNoOp(t *testing.T) {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	m := newFakeMailer()
	s := NewEmailSink(m, nil, "alerts@example.com", log)
	t.Cleanup(func() { _ = s.Close() })

	s.Report(context.Background(), &apperror.Incident{Code: "x", Message: "y"})
	time.Sleep(50 * time.Millisecond)
	if got := m.snapshot(); len(got) != 0 {
		t.Errorf("expected zero sends, got %d", len(got))
	}
}

func TestEmailSink_Overflow_DropsAndDoesNotBlock(t *testing.T) {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	m := newFakeMailer()
	m.blockCh = make(chan struct{})
	s := NewEmailSink(m, []string{"ops@example.com"}, "alerts@example.com", log)
	t.Cleanup(func() {
		close(m.blockCh)
		_ = s.Close()
	})

	inc := &apperror.Incident{Code: "x", Message: "y"}
	fillDone := make(chan struct{})
	go func() {
		for i := 0; i < emailQueueCap*3; i++ {
			s.Report(context.Background(), inc)
		}
		close(fillDone)
	}()
	select {
	case <-fillDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Report blocked; queue not non-blocking")
	}
	if s.droppedCount() == 0 {
		t.Errorf("expected drops > 0, got 0")
	}
}

func TestEmailSink_Close_MakesReportNoOp(t *testing.T) {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	m := newFakeMailer()
	s := NewEmailSink(m, []string{"ops@example.com"}, "alerts@example.com", log)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s.Report(context.Background(), &apperror.Incident{Code: "x", Message: "y"})
	if got := s.droppedCount(); got != 0 {
		t.Errorf("after Close, Report should be a no-op; got dropped=%d", got)
	}
}

func TestEmailSink_MailerError_IsLoggedNotFatal(t *testing.T) {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	m := newFakeMailer()
	m.errOnce = errors.New("smtp down")
	s := NewEmailSink(m, []string{"ops@example.com"}, "alerts@example.com", log)
	t.Cleanup(func() { _ = s.Close() })

	s.Report(context.Background(), &apperror.Incident{Code: "x", Message: "y"})
	select {
	case <-m.done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for send")
	}
	s.Report(context.Background(), &apperror.Incident{Code: "x", Message: "y2"})
	select {
	case <-m.done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for second send after error")
	}
	if got := len(m.snapshot()); got != 2 {
		t.Errorf("expected 2 sends across error and success, got %d", got)
	}
}
