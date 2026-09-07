package logger_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"altalune.id/template/logger"
)

func TestRedact_MasksSensitiveKeys(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		ReplaceAttr: logger.Redact(logger.Config{}),
	})
	slog.New(h).InfoContext(context.Background(), "login",
		"email", "u@example.com",
		"password", "secret-pw",
		"authorization", "Bearer x")

	var got map[string]any
	_ = json.Unmarshal(buf.Bytes(), &got)
	if got["password"] != "<redacted>" {
		t.Errorf("password = %v, want <redacted>", got["password"])
	}
	if got["authorization"] != "<redacted>" {
		t.Errorf("authorization = %v, want <redacted>", got["authorization"])
	}
	if got["email"] != "u@example.com" {
		t.Errorf("email = %v, must not be redacted", got["email"])
	}
}
