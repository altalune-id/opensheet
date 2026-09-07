package logger

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"

	"altalune.id/template/reqid"
)

// ContextHandler wraps a slog.Handler and injects request_id, trace_id, and span_id from ctx into every record.
type ContextHandler struct{ slog.Handler }

// Handle attaches request_id and trace/span ids from ctx, then delegates.
func (h ContextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := reqid.FromContext(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, r)
}

// WithAttrs preserves the wrapper.
func (h ContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return ContextHandler{Handler: h.Handler.WithAttrs(attrs)}
}

// WithGroup preserves the wrapper.
func (h ContextHandler) WithGroup(name string) slog.Handler {
	return ContextHandler{Handler: h.Handler.WithGroup(name)}
}
