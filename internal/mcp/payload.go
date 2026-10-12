package mcp

import (
	"context"

	"go.opentelemetry.io/otel/trace"

	rootmcp "altalune.id/opensheet/mcp"
	"altalune.id/opensheet/reqid"
)

// AttachContext returns a copy of p carrying the request and trace ids ctx holds, leaving any id already set alone.
func AttachContext(ctx context.Context, p rootmcp.ErrorPayload) rootmcp.ErrorPayload {
	if p.RequestID == "" {
		p.RequestID = reqid.FromContext(ctx)
	}
	if p.TraceID != "" {
		return p
	}
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		p.TraceID = sc.TraceID().String()
	}
	return p
}
