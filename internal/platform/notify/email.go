package notify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/mailer"
)

const (
	emailQueueCap    = 128
	emailSendTimeout = 15 * time.Second
)

// EmailSink delivers incident notifications by email via mailer.Mailer.
type EmailSink struct {
	mail      mailer.Mailer
	to        []string
	from      string
	log       *slog.Logger
	q         chan emailPayload
	wg        sync.WaitGroup
	mu        sync.RWMutex
	closed    bool
	closeOnce sync.Once
	dropped   atomic.Uint64
}

type emailPayload struct {
	subject string
	body    string
}

// NewEmailSink builds an EmailSink using mail as the transport.
func NewEmailSink(mail mailer.Mailer, to []string, from string, log *slog.Logger) *EmailSink {
	if log == nil {
		log = slog.Default()
	}
	s := &EmailSink{
		mail: mail,
		to:   append([]string(nil), to...),
		from: from,
		log:  log,
		q:    make(chan emailPayload, emailQueueCap),
	}
	s.wg.Add(1)
	go s.run()
	return s
}

// Report enqueues an incident for delivery; non-blocking, drops on overflow.
func (s *EmailSink) Report(ctx context.Context, inc *apperror.Incident) {
	if inc == nil || s.mail == nil || len(s.to) == 0 {
		return
	}
	pl := emailPayload{
		subject: fmt.Sprintf("[incident] %s", inc.Code),
		body:    formatEmailBody(inc),
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return
	}
	select {
	case s.q <- pl:
	default:
		n := s.dropped.Add(1)
		s.log.WarnContext(ctx, "notify: email queue full, dropping",
			slog.String("sink", "email"),
			slog.Uint64("dropped_total", n),
		)
	}
}

func (s *EmailSink) run() {
	defer s.wg.Done()
	for p := range s.q {
		ctx, cancel := context.WithTimeout(context.Background(), emailSendTimeout)
		for _, addr := range s.to {
			msg := mailer.Message{
				To:       addr,
				From:     s.from,
				Subject:  p.subject,
				TextBody: p.body,
			}
			if err := s.mail.Send(ctx, msg); err != nil {
				s.log.Error("notify: email send",
					slog.String("sink", "email"),
					slog.String("to", addr),
					slog.Any("error", err))
			}
		}
		cancel()
	}
}

func (s *EmailSink) droppedCount() uint64 { return s.dropped.Load() }

// Close closes the queue and waits for the consumer goroutine to drain.
func (s *EmailSink) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		close(s.q)
		s.mu.Unlock()
		s.wg.Wait()
	})
	return nil
}

func formatEmailBody(inc *apperror.Incident) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Code:       %s\n", inc.Code)
	fmt.Fprintf(&b, "Message:    %s\n", inc.Message)
	if inc.RequestID != "" {
		fmt.Fprintf(&b, "Request ID: %s\n", inc.RequestID)
	}
	if inc.TraceID != "" {
		fmt.Fprintf(&b, "Trace ID:   %s\n", inc.TraceID)
	}
	if inc.Cause != nil {
		fmt.Fprintf(&b, "Cause:      %s\n", inc.Cause.Error())
	}
	return b.String()
}
