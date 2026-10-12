package queue

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"altalune.id/opensheet/internal/platform/tenant"
)

func TestTenantHeaders_RoundTrip(t *testing.T) {
	tests := []struct {
		name string
		tc   tenant.Context
	}{
		{"org only", tenant.Context{OrgID: uuid.New()}},
		{"org, project and user", tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New(), UserID: uuid.New()}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := nats.Header{}
			setTenant(h, tt.tc)

			got, ok, err := tenantFrom(h)
			require.NoError(t, err)
			require.True(t, ok)
			assert.Equal(t, tt.tc, got)
		})
	}
}

func TestSetTenant_SkipsZeroIDs(t *testing.T) {
	h := nats.Header{}
	setTenant(h, tenant.Context{OrgID: uuid.New()})

	assert.NotEmpty(t, h.Get(headerOrgID))
	assert.Empty(t, h.Values(headerProjectID))
	assert.Empty(t, h.Values(headerUserID))
}

func TestTenantFrom_NoHeaderIsUnscoped(t *testing.T) {
	_, ok, err := tenantFrom(nats.Header{})
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestTenantFrom_MalformedHeader(t *testing.T) {
	tests := []struct {
		name   string
		header string
	}{
		{"org", headerOrgID},
		{"project", headerProjectID},
		{"user", headerUserID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := nats.Header{}
			setTenant(h, tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New(), UserID: uuid.New()})
			h.Set(tt.header, "not-a-uuid")

			_, _, err := tenantFrom(h)
			require.Error(t, err)
		})
	}
}

func TestTenantFrom_ProjectOrUserWithoutOrg(t *testing.T) {
	for _, key := range []string{headerProjectID, headerUserID} {
		t.Run(key, func(t *testing.T) {
			h := nats.Header{}
			h.Set(key, uuid.NewString())

			_, ok, err := tenantFrom(h)
			require.Error(t, err)
			assert.False(t, ok)
		})
	}
}

func TestTraceHeaders_RoundTrip(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	ctx, span := tp.Tracer("test").Start(t.Context(), "parent")
	defer span.End()

	h := nats.Header{}
	injectTrace(ctx, h)
	require.NotEmpty(t, h.Get("traceparent"))

	got := trace.SpanContextFromContext(extractTrace(context.Background(), h))
	assert.Equal(t, span.SpanContext().TraceID(), got.TraceID())
	assert.Equal(t, span.SpanContext().SpanID(), got.SpanID())
}

func TestHeaderNames(t *testing.T) {
	tests := []struct {
		got  string
		want string
	}{
		{headerMsgID, "Nats-Msg-Id"},
		{headerJob, "Opensheet-Job"},
		{headerJobVersion, "Opensheet-Job-Version"},
		{headerBroadcast, "Opensheet-Broadcast"},
		{headerBroadcastVersion, "Opensheet-Broadcast-Version"},
		{headerCreatedAt, "Opensheet-Created-At"},
		{headerOrgID, "Opensheet-Org-Id"},
		{headerProjectID, "Opensheet-Project-Id"},
		{headerUserID, "Opensheet-User-Id"},
		{headerDlqReason, "Opensheet-Dlq-Reason"},
		{headerDlqError, "Opensheet-Dlq-Error"},
		{headerDlqAttempts, "Opensheet-Dlq-Attempts"},
		{headerDlqStreamSeq, "Opensheet-Dlq-Stream-Seq"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.got)
		})
	}
}
