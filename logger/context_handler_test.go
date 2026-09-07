package logger_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"altalune.id/template/logger"
	"altalune.id/template/reqid"
)

func TestContextHandler_AttachesRequestID(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewJSONHandler(&buf, nil)
	log := slog.New(logger.ContextHandler{Handler: base})

	ctx := reqid.WithContext(context.Background(), "test-req-id")
	log.InfoContext(ctx, "hello")

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["request_id"] != "test-req-id" {
		t.Errorf("request_id = %v, want test-req-id", got["request_id"])
	}
}

func TestContextHandler_OmitsWhenAbsent(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(logger.ContextHandler{Handler: slog.NewJSONHandler(&buf, nil)})
	log.InfoContext(context.Background(), "no ids")
	var got map[string]any
	_ = json.Unmarshal(buf.Bytes(), &got)
	if _, has := got["request_id"]; has {
		t.Error("request_id must not appear when ctx carries none")
	}
}
