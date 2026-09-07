package apperror

import (
	"context"
	"log/slog"
)

// ReportSink receives fan-out incident notifications from Reporter.
type ReportSink interface {
	Report(ctx context.Context, incident *Incident)
}

// IncidentCounter records a tick per unexpected incident, keyed by code.
type IncidentCounter interface {
	Inc(ctx context.Context, code string)
}

// ContextMetaFunc extracts serializable metadata from ctx for the wire envelope.
type ContextMetaFunc func(ctx context.Context) map[string]string

// Incident carries the details of an unexpected error to every ReportSink.
type Incident struct {
	Message   string
	Code      string
	Cause     error
	AppErr    *AppError
	Attrs     []slog.Attr
	RequestID string
	TraceID   string
}
