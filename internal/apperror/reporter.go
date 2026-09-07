package apperror

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/template/gen/go/apperror/v1"
	"altalune.id/template/reqid"
)

// UnexpectedFunc is the function-type dependency domain services accept.
type UnexpectedFunc = func(ctx context.Context, message string, cause error, attrs ...any) *AppError

// Reporter turns unexpected errors into AppError envelopes and fans out incidents.
type Reporter struct {
	log          *slog.Logger
	isProduction bool
	contextMeta  ContextMetaFunc
	counter      IncidentCounter
	sinks        []ReportSink
}

// ReporterOption configures a Reporter at construction.
type ReporterOption func(*Reporter)

// WithContextMeta installs a function that adds ctx-derived key/values into the wire envelope.
func WithContextMeta(fn ContextMetaFunc) ReporterOption {
	return func(r *Reporter) { r.contextMeta = fn }
}

// WithSinks appends ReportSinks that receive fan-out incident notifications.
func WithSinks(s ...ReportSink) ReporterOption {
	return func(r *Reporter) { r.sinks = append(r.sinks, s...) }
}

// WithIncidentCounter attaches a counter ticked once per Unexpected call.
func WithIncidentCounter(c IncidentCounter) ReporterOption {
	return func(r *Reporter) { r.counter = c }
}

// NewReporter builds a Reporter with the given logger, prod flag, and options.
func NewReporter(log *slog.Logger, isProduction bool, opts ...ReporterOption) *Reporter {
	r := &Reporter{log: log, isProduction: isProduction}
	for _, o := range opts {
		o(r)
	}
	return r
}

var _ UnexpectedFunc = (*Reporter)(nil).Unexpected

// Unexpected logs cause at ERROR, ticks the counter, fans out to sinks, and returns a CodeUnexpectedError AppError.
func (r *Reporter) Unexpected(ctx context.Context, message string, cause error, attrs ...any) *AppError {
	reqID := reqid.FromContext(ctx)
	var traceID string
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		traceID = sc.TraceID().String()
	}

	logAttrs := append([]any{slog.Any("error", cause)}, attrs...)
	r.log.ErrorContext(ctx, message, logAttrs...)

	if r.counter != nil {
		r.counter.Inc(ctx, CodeUnexpectedError)
	}

	wireMsg := "An unexpected error occurred"
	meta := r.collectMeta(ctx)
	if !r.isProduction && cause != nil {
		wireMsg = wireMsg + ": " + cause.Error()
		meta["underlying_error"] = cause.Error()
	}
	out := New(CodeUnexpectedError, wireMsg, codes.Internal,
		&apperrorv1.ErrorDetail{
			Code:      CodeUnexpectedError,
			Meta:      meta,
			RequestId: reqID,
			TraceId:   traceID,
		}).WithCause(cause)

	if inner, ok := AsAppError(cause); ok && inner.Upstream() != nil {
		out = out.WithUpstream(inner.Upstream())
	}

	r.fanOut(ctx, message, cause, out, attrs, reqID, traceID)
	return out
}

func (r *Reporter) collectMeta(ctx context.Context) map[string]string {
	m := map[string]string{}
	if r.contextMeta == nil {
		return m
	}
	for k, v := range r.contextMeta(ctx) {
		m[k] = v
	}
	return m
}

func (r *Reporter) fanOut(ctx context.Context, msg string, cause error, out *AppError, attrs []any, reqID, traceID string) {
	if len(r.sinks) == 0 {
		return
	}
	slogAttrs := toSlogAttrs(attrs)
	inc := &Incident{
		Message:   msg,
		Code:      out.code,
		Cause:     cause,
		AppErr:    out,
		Attrs:     slogAttrs,
		RequestID: reqID,
		TraceID:   traceID,
	}
	for _, s := range r.sinks {
		s.Report(ctx, inc)
	}
}

func toSlogAttrs(a []any) []slog.Attr {
	out := make([]slog.Attr, 0, len(a)/2)
	for i := 0; i+1 < len(a); i += 2 {
		key, _ := a[i].(string)
		if key == "" {
			continue
		}
		out = append(out, slog.Any(key, a[i+1]))
	}
	return out
}
