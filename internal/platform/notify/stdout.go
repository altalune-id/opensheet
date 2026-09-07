// Package notify provides apperror.ReportSink adapters for incident fan-out.
package notify

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"sync"

	"altalune.id/template/internal/apperror"
)

// StdoutSink writes JSON incident records to stderr.
type StdoutSink struct {
	log *slog.Logger
	mu  sync.Mutex
	w   io.Writer
}

// NewStdoutSink builds a StdoutSink writing to os.Stderr.
func NewStdoutSink(log *slog.Logger) *StdoutSink {
	return newStdoutSink(log, os.Stderr)
}

func newStdoutSink(log *slog.Logger, w io.Writer) *StdoutSink {
	if log == nil {
		log = slog.Default()
	}
	return &StdoutSink{log: log, w: w}
}

// Report writes a single-line JSON encoding of inc.
func (s *StdoutSink) Report(ctx context.Context, inc *apperror.Incident) {
	if inc == nil {
		return
	}
	rec := stdoutRecord{
		Level:     "ERROR",
		Kind:      "incident",
		Code:      inc.Code,
		Message:   inc.Message,
		RequestID: inc.RequestID,
		TraceID:   inc.TraceID,
	}
	if inc.Cause != nil {
		rec.Cause = inc.Cause.Error()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := json.NewEncoder(s.w).Encode(rec); err != nil {
		s.log.ErrorContext(ctx, "notify: stdout encode", slog.Any("error", err))
	}
}

type stdoutRecord struct {
	Level     string `json:"level"`
	Kind      string `json:"kind"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Cause     string `json:"cause,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	TraceID   string `json:"trace_id,omitempty"`
}
