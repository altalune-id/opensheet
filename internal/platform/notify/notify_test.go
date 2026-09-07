package notify

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"altalune.id/template/mailer"
)

type noopMailer struct{}

func (noopMailer) Send(_ context.Context, _ mailer.Message) error { return nil }

func TestBuild_EmptyConfigReturnsEmptySlice(t *testing.T) {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	sinks := Build(Config{}, nil, log)
	if len(sinks) != 0 {
		t.Errorf("expected empty slice, got %d sinks", len(sinks))
	}
	if sinks == nil {
		t.Error("expected non-nil empty slice, got nil")
	}
}

func TestBuild_DispatchesByKind(t *testing.T) {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := Config{
		Sinks: []SinkConfig{
			{Kind: "stdout"},
			{Kind: "Slack", WebhookURL: "http://slack.example"},
			{Kind: "DISCORD", WebhookURL: "http://discord.example"},
			{Kind: "googlechat", WebhookURL: "http://gchat.example"},
			{Kind: "email", To: []string{"ops@example.com"}, From: "alerts@example.com"},
		},
	}
	sinks := Build(cfg, noopMailer{}, log)
	t.Cleanup(func() {
		for _, s := range sinks {
			if c, ok := s.(io.Closer); ok {
				_ = c.Close()
			}
		}
	})

	if len(sinks) != 5 {
		t.Fatalf("want 5 sinks, got %d", len(sinks))
	}
	wantTypes := []string{"*notify.StdoutSink", "*notify.SlackSink", "*notify.DiscordSink", "*notify.GoogleChatSink", "*notify.EmailSink"}
	for i, s := range sinks {
		typeName := typeString(s)
		if typeName != wantTypes[i] {
			t.Errorf("sink[%d] type = %s, want %s", i, typeName, wantTypes[i])
		}
	}
}

func TestBuild_UnknownKindLogsWarning(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	sinks := Build(Config{Sinks: []SinkConfig{{Kind: "pigeon"}}}, nil, log)
	if len(sinks) != 0 {
		t.Errorf("unknown kind must not produce a sink; got %d", len(sinks))
	}
	if !strings.Contains(buf.String(), "unknown sink kind") {
		t.Errorf("expected warning log, got %s", buf.String())
	}
	if !strings.Contains(buf.String(), "pigeon") {
		t.Errorf("expected kind name in log, got %s", buf.String())
	}
}

func TestBuild_EmailWithoutMailerSkipped(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	sinks := Build(Config{Sinks: []SinkConfig{{Kind: "email", To: []string{"ops@example.com"}}}}, nil, log)
	if len(sinks) != 0 {
		t.Errorf("email without mailer must be skipped; got %d", len(sinks))
	}
	if !strings.Contains(buf.String(), "no Mailer") {
		t.Errorf("expected mailer-missing warning, got %s", buf.String())
	}
}

func TestBuild_SlackWithoutURLSkipped(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	sinks := Build(Config{Sinks: []SinkConfig{{Kind: "slack"}}}, nil, log)
	if len(sinks) != 0 {
		t.Errorf("slack without url must be skipped; got %d", len(sinks))
	}
	if !strings.Contains(buf.String(), "missing webhookUrl") {
		t.Errorf("expected missing-url warning, got %s", buf.String())
	}
}

func TestBuild_EmailWithoutRecipientsSkipped(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	sinks := Build(Config{Sinks: []SinkConfig{{Kind: "email"}}}, noopMailer{}, log)
	if len(sinks) != 0 {
		t.Errorf("email without recipients must be skipped; got %d", len(sinks))
	}
	if !strings.Contains(buf.String(), "to addresses") {
		t.Errorf("expected missing-to warning, got %s", buf.String())
	}
}

func typeString(v any) string {
	switch v.(type) {
	case *StdoutSink:
		return "*notify.StdoutSink"
	case *SlackSink:
		return "*notify.SlackSink"
	case *DiscordSink:
		return "*notify.DiscordSink"
	case *GoogleChatSink:
		return "*notify.GoogleChatSink"
	case *EmailSink:
		return "*notify.EmailSink"
	default:
		return "unknown"
	}
}
