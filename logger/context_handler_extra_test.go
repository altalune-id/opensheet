package logger_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/template/logger"
	"altalune.id/template/reqid"
)

func TestContextHandler_WithAttrs_PreservesWrapper(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	base := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	h := logger.ContextHandler{Handler: base}

	withAttrs := h.WithAttrs([]slog.Attr{slog.String("service", "test")})
	lg := slog.New(withAttrs)
	ctx := reqid.WithContext(context.Background(), "abc123")
	lg.InfoContext(ctx, "hello")

	out := buf.String()
	assert.Contains(t, out, "service")
	assert.Contains(t, out, "abc123", "request_id must still be injected through wrapped handler")
}

func TestContextHandler_WithGroup_PreservesWrapper(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	base := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	h := logger.ContextHandler{Handler: base}

	grouped := h.WithGroup("api")
	require.NotNil(t, grouped)

	lg := slog.New(grouped)
	ctx := reqid.WithContext(context.Background(), "req-1")
	lg.InfoContext(ctx, "grouped", "user", "alice")

	out := buf.String()
	assert.NotEmpty(t, out)
	assert.True(t, strings.Contains(out, "req-1") || strings.Contains(out, "request_id"))
}

func TestRedact_ExtraPatterns(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	h := slog.NewJSONHandler(buf, &slog.HandlerOptions{
		ReplaceAttr: logger.Redact(logger.Config{RedactPatterns: []string{"account_number", ""}}),
	})
	lg := slog.New(h)
	lg.Info("charge", "account_number", "12345", "safe", "ok")

	out := buf.String()
	assert.Contains(t, out, "<redacted>")
	assert.Contains(t, out, "safe")
	assert.NotContains(t, out, "12345")
}

func TestRedact_InvalidRegexIgnored(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	h := slog.NewJSONHandler(buf, &slog.HandlerOptions{
		ReplaceAttr: logger.Redact(logger.Config{RedactPatterns: []string{"["}}),
	})
	lg := slog.New(h)
	lg.Info("hi", "field", "value")
	assert.Contains(t, buf.String(), "value")
}
