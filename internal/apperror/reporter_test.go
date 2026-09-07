package apperror_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"

	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/template/gen/go/apperror/v1"
	"altalune.id/template/internal/apperror"
	"altalune.id/template/reqid"
)

type fakeCounter struct{ n atomic.Int64 }

func (c *fakeCounter) Inc(_ context.Context, _ string) { c.n.Add(1) }

type recordingSink struct {
	mu        sync.Mutex
	incidents []*apperror.Incident
}

func (s *recordingSink) Report(_ context.Context, inc *apperror.Incident) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.incidents = append(s.incidents, inc)
}

func (s *recordingSink) snapshot() []*apperror.Incident {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*apperror.Incident, len(s.incidents))
	copy(out, s.incidents)
	return out
}

func newTestLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func fixedSpanContext(t *testing.T) trace.SpanContext {
	t.Helper()
	traceID, err := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	if err != nil {
		t.Fatalf("trace id: %v", err)
	}
	spanID, err := trace.SpanIDFromHex("0102030405060708")
	if err != nil {
		t.Fatalf("span id: %v", err)
	}
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
}

func firstErrorDetail(t *testing.T, e *apperror.AppError) *apperrorv1.ErrorDetail {
	t.Helper()
	for _, d := range e.Details() {
		if ed, ok := d.(*apperrorv1.ErrorDetail); ok {
			return ed
		}
	}
	t.Fatal("no *apperrorv1.ErrorDetail in AppError.Details()")
	return nil
}

func TestReporter_Unexpected_ReturnsAppErrorWithCode(t *testing.T) {
	var buf bytes.Buffer
	r := apperror.NewReporter(newTestLogger(&buf), false)
	cause := errors.New("boom")

	got := r.Unexpected(context.Background(), "op failed", cause)

	if got == nil {
		t.Fatal("Unexpected returned nil")
	}
	if got.Code() != apperror.CodeUnexpectedError {
		t.Errorf("Code() = %q, want %q", got.Code(), apperror.CodeUnexpectedError)
	}
	if got.GRPCCode() != codes.Internal {
		t.Errorf("GRPCCode() = %v, want Internal", got.GRPCCode())
	}
	if !errors.Is(got, cause) {
		t.Error("errors.Is must reach the wrapped cause")
	}
}

func TestReporter_Unexpected_LogsAtError(t *testing.T) {
	var buf bytes.Buffer
	r := apperror.NewReporter(newTestLogger(&buf), false)
	cause := errors.New("db lost")

	_ = r.Unexpected(context.Background(), "load user", cause)

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal log: %v", err)
	}
	if got["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR", got["level"])
	}
	if got["msg"] != "load user" {
		t.Errorf("msg = %v, want load user", got["msg"])
	}
	if got["error"] != "db lost" {
		t.Errorf("error attr = %v, want db lost", got["error"])
	}
}

func TestReporter_Unexpected_TicksCounterOnce(t *testing.T) {
	ctr := &fakeCounter{}
	r := apperror.NewReporter(newTestLogger(io.Discard), false, apperror.WithIncidentCounter(ctr))

	_ = r.Unexpected(context.Background(), "x", errors.New("y"))

	if got := ctr.n.Load(); got != 1 {
		t.Errorf("counter ticked %d times, want 1", got)
	}
}

func TestReporter_Unexpected_FansOutToSinks(t *testing.T) {
	sink1, sink2 := &recordingSink{}, &recordingSink{}
	r := apperror.NewReporter(newTestLogger(io.Discard), false,
		apperror.WithSinks(sink1, sink2))
	ctx := reqid.WithContext(context.Background(), "req-abc")
	cause := errors.New("bad thing")

	out := r.Unexpected(ctx, "op x", cause, "key", "val")

	for i, s := range []*recordingSink{sink1, sink2} {
		got := s.snapshot()
		if len(got) != 1 {
			t.Fatalf("sink %d received %d incidents, want 1", i, len(got))
		}
		inc := got[0]
		if inc.Message != "op x" {
			t.Errorf("sink %d Message = %q", i, inc.Message)
		}
		if inc.Code != apperror.CodeUnexpectedError {
			t.Errorf("sink %d Code = %q", i, inc.Code)
		}
		if !errors.Is(inc.Cause, cause) {
			t.Errorf("sink %d Cause = %v", i, inc.Cause)
		}
		if inc.AppErr != out {
			t.Errorf("sink %d AppErr pointer mismatch", i)
		}
		if inc.RequestID != "req-abc" {
			t.Errorf("sink %d RequestID = %q, want req-abc", i, inc.RequestID)
		}
		if len(inc.Attrs) != 1 || inc.Attrs[0].Key != "key" {
			t.Errorf("sink %d Attrs = %+v", i, inc.Attrs)
		}
	}
}

func TestReporter_Unexpected_PopulatesRequestAndTraceID(t *testing.T) {
	r := apperror.NewReporter(newTestLogger(io.Discard), false)

	sc := fixedSpanContext(t)
	ctx := trace.ContextWithSpanContext(context.Background(), sc)
	ctx = reqid.WithContext(ctx, "req-42")

	out := r.Unexpected(ctx, "boom", errors.New("cause"))

	det := firstErrorDetail(t, out)
	if det.GetRequestId() != "req-42" {
		t.Errorf("ErrorDetail.RequestId = %q, want req-42", det.GetRequestId())
	}
	if det.GetTraceId() != sc.TraceID().String() {
		t.Errorf("ErrorDetail.TraceId = %q, want %q", det.GetTraceId(), sc.TraceID().String())
	}
}

func TestReporter_Unexpected_ProductionRedactsMessage(t *testing.T) {
	r := apperror.NewReporter(newTestLogger(io.Discard), true)
	cause := errors.New("secret: password=hunter2")

	out := r.Unexpected(context.Background(), "op", cause)

	if got := out.Message(); got != "An unexpected error occurred" {
		t.Errorf("prod Message() = %q, want redacted", got)
	}
	det := firstErrorDetail(t, out)
	if _, has := det.GetMeta()["underlying_error"]; has {
		t.Error("prod mode must not leak underlying_error into meta")
	}
}

func TestReporter_Unexpected_NonProductionExposesCause(t *testing.T) {
	r := apperror.NewReporter(newTestLogger(io.Discard), false)
	cause := errors.New("driver: connection refused")

	out := r.Unexpected(context.Background(), "op", cause)

	want := "An unexpected error occurred: driver: connection refused"
	if got := out.Message(); got != want {
		t.Errorf("dev Message() = %q, want %q", got, want)
	}
	det := firstErrorDetail(t, out)
	if got := det.GetMeta()["underlying_error"]; got != "driver: connection refused" {
		t.Errorf("dev meta[underlying_error] = %q", got)
	}
}

func TestReporter_Unexpected_ContextMetaMerged(t *testing.T) {
	meta := func(_ context.Context) map[string]string {
		return map[string]string{"tenant": "acme", "user": "u1"}
	}
	r := apperror.NewReporter(newTestLogger(io.Discard), true, apperror.WithContextMeta(meta))

	out := r.Unexpected(context.Background(), "op", errors.New("x"))

	det := firstErrorDetail(t, out)
	if det.GetMeta()["tenant"] != "acme" || det.GetMeta()["user"] != "u1" {
		t.Errorf("meta = %+v, want tenant=acme user=u1", det.GetMeta())
	}
}

func TestReporter_Unexpected_PreservesUpstream(t *testing.T) {
	upstream := &apperrorv1.UpstreamErrorDetail{
		Service: "altalune-auth",
		Source:  "connect",
		Code:    "token.expired",
	}
	inner := apperror.New("test.inner", "inner", codes.Internal).WithUpstream(upstream)

	r := apperror.NewReporter(newTestLogger(io.Discard), false)
	out := r.Unexpected(context.Background(), "outer op", inner)

	if got := out.Upstream(); got == nil || got.GetCode() != "token.expired" {
		t.Errorf("Upstream() = %+v, want propagated from inner", got)
	}
}

func TestReporter_UnexpectedFuncBinding(t *testing.T) {
	r := apperror.NewReporter(newTestLogger(io.Discard), false)
	//nolint:staticcheck // intentional: explicit type asserts the alias binding.
	var f apperror.UnexpectedFunc = r.Unexpected
	_ = f
}
