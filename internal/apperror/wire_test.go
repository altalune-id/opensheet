package apperror_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/template/gen/go/apperror/v1"
	"altalune.id/template/internal/apperror"
	"altalune.id/template/reqid"
)

func TestAttachContext_NilError(t *testing.T) {
	if got := apperror.AttachContext(context.Background(), nil); got != nil {
		t.Errorf("AttachContext(nil) = %v, want nil", got)
	}
}

func TestAttachContext_PopulatesErrorDetail(t *testing.T) {
	sc := fixedSpanContext(t)
	ctx := trace.ContextWithSpanContext(context.Background(), sc)
	ctx = reqid.WithContext(ctx, "req-99")

	e := apperror.New("test.code", "msg", codes.Internal,
		&apperrorv1.ErrorDetail{Code: "test.code"})

	got := apperror.AttachContext(ctx, e)

	det := firstErrorDetail(t, got)
	if det.GetRequestId() != "req-99" {
		t.Errorf("RequestId = %q, want req-99", det.GetRequestId())
	}
	if det.GetTraceId() != sc.TraceID().String() {
		t.Errorf("TraceId = %q, want %q", det.GetTraceId(), sc.TraceID().String())
	}

	origDet := firstErrorDetail(t, e)
	if origDet.GetRequestId() != "" || origDet.GetTraceId() != "" {
		t.Error("AttachContext must not mutate the source AppError's details")
	}
}

func TestAttachContext_NoOpWhenCtxCarriesNeither(t *testing.T) {
	e := apperror.New("test.code", "msg", codes.Internal,
		&apperrorv1.ErrorDetail{Code: "test.code"})

	got := apperror.AttachContext(context.Background(), e)

	if got != e {
		t.Error("AttachContext with bare ctx should return receiver unchanged")
	}
}

func TestAttachContext_PreservesExistingIDs(t *testing.T) {
	sc := fixedSpanContext(t)
	ctx := trace.ContextWithSpanContext(context.Background(), sc)
	ctx = reqid.WithContext(ctx, "outer-req")

	e := apperror.New("test.code", "msg", codes.Internal,
		&apperrorv1.ErrorDetail{
			Code:      "test.code",
			RequestId: "inner-req",
			TraceId:   "inner-trace",
		})

	got := apperror.AttachContext(ctx, e)

	det := firstErrorDetail(t, got)
	if det.GetRequestId() != "inner-req" {
		t.Errorf("RequestId = %q, want inner-req (should not be overwritten)", det.GetRequestId())
	}
	if det.GetTraceId() != "inner-trace" {
		t.Errorf("TraceId = %q, want inner-trace (should not be overwritten)", det.GetTraceId())
	}
}

func TestAttachContext_PreservesNonErrorDetailProtos(t *testing.T) {
	ctx := reqid.WithContext(context.Background(), "req-1")
	upstream := &apperrorv1.UpstreamErrorDetail{Code: "u.code"}
	ed := &apperrorv1.ErrorDetail{Code: "test.code"}
	e := apperror.New("test.code", "msg", codes.Internal, ed, upstream)

	got := apperror.AttachContext(ctx, e)

	details := got.Details()
	if len(details) != 2 {
		t.Fatalf("Details length = %d, want 2", len(details))
	}
	if up, ok := details[1].(*apperrorv1.UpstreamErrorDetail); !ok || up.GetCode() != "u.code" {
		t.Errorf("Details[1] = %+v, want UpstreamErrorDetail{code=u.code}", details[1])
	}
}

func TestAttachContext_OnlyRequestIDPresent(t *testing.T) {
	ctx := reqid.WithContext(context.Background(), "just-req")
	e := apperror.New("test.code", "msg", codes.Internal,
		&apperrorv1.ErrorDetail{Code: "test.code"})

	got := apperror.AttachContext(ctx, e)

	det := firstErrorDetail(t, got)
	if det.GetRequestId() != "just-req" {
		t.Errorf("RequestId = %q, want just-req", det.GetRequestId())
	}
	if det.GetTraceId() != "" {
		t.Errorf("TraceId = %q, want empty", det.GetTraceId())
	}
}
