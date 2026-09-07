package apperror

import (
	"context"

	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"

	apperrorv1 "altalune.id/template/gen/go/apperror/v1"
	"altalune.id/template/reqid"
)

// AttachContext returns a copy of e whose ErrorDetail(s) carry request_id and trace_id from ctx.
func AttachContext(ctx context.Context, e *AppError) *AppError {
	if e == nil {
		return nil
	}
	reqID := reqid.FromContext(ctx)
	var traceID string
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		traceID = sc.TraceID().String()
	}
	if reqID == "" && traceID == "" {
		return e
	}

	cp := *e
	cp.details = make([]proto.Message, len(e.details))
	for i, d := range e.details {
		if ed, ok := d.(*apperrorv1.ErrorDetail); ok {
			cpDet, _ := proto.Clone(ed).(*apperrorv1.ErrorDetail)
			if cpDet.RequestId == "" {
				cpDet.RequestId = reqID
			}
			if cpDet.TraceId == "" {
				cpDet.TraceId = traceID
			}
			cp.details[i] = cpDet
		} else {
			cp.details[i] = d
		}
	}
	return &cp
}
