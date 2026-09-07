package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	"altalune.id/template/internal/apperror"
)

func TestStdoutSink_Report_WritesJSON(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	s := newStdoutSink(log, &buf)

	s.Report(context.Background(), &apperror.Incident{
		Code:      "altempl.unexpected",
		Message:   "op failed",
		Cause:     errors.New("boom"),
		RequestID: "req-1",
		TraceID:   "trace-1",
	})

	var got stdoutRecord
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v (raw=%q)", err, buf.String())
	}
	if got.Level != "ERROR" {
		t.Errorf("Level = %q, want ERROR", got.Level)
	}
	if got.Code != "altempl.unexpected" {
		t.Errorf("Code = %q", got.Code)
	}
	if got.Message != "op failed" {
		t.Errorf("Message = %q", got.Message)
	}
	if got.Cause != "boom" {
		t.Errorf("Cause = %q", got.Cause)
	}
	if got.RequestID != "req-1" {
		t.Errorf("RequestID = %q", got.RequestID)
	}
	if got.TraceID != "trace-1" {
		t.Errorf("TraceID = %q", got.TraceID)
	}
}

func TestStdoutSink_Report_NilIncidentIsNoOp(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	s := newStdoutSink(log, &buf)

	s.Report(context.Background(), nil)

	if buf.Len() != 0 {
		t.Errorf("nil incident wrote %q, want empty", buf.String())
	}
}

func TestStdoutSink_NewUsesStderrByDefault(t *testing.T) {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	s := NewStdoutSink(log)
	if s.w == nil {
		t.Fatal("writer must default to stderr")
	}
}
