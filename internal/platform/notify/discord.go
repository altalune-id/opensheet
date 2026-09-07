package notify

import (
	"context"
	"encoding/json"
	"log/slog"

	"altalune.id/template/internal/apperror"
)

// DiscordSink posts incident notifications to a Discord webhook.
type DiscordSink struct{ *webhookSink }

// NewDiscordSink builds a DiscordSink writing to webhookURL.
func NewDiscordSink(webhookURL string, log *slog.Logger) *DiscordSink {
	return &DiscordSink{webhookSink: newWebhookSink("discord", webhookURL, log)}
}

// Report enqueues an incident for delivery; non-blocking, drops on overflow.
func (s *DiscordSink) Report(ctx context.Context, inc *apperror.Incident) {
	if inc == nil || s.url == "" {
		return
	}
	payload, err := json.Marshal(buildDiscordPayload(inc))
	if err != nil {
		s.log.ErrorContext(ctx, "notify: discord marshal", slog.Any("error", err))
		return
	}
	s.enqueue(ctx, payload)
}

func buildDiscordPayload(inc *apperror.Incident) map[string]any {
	fields := []map[string]any{
		{"name": "Code", "value": inc.Code, "inline": true},
	}
	if inc.RequestID != "" {
		fields = append(fields, map[string]any{"name": "Request ID", "value": inc.RequestID, "inline": true})
	}
	if inc.TraceID != "" {
		fields = append(fields, map[string]any{"name": "Trace ID", "value": inc.TraceID, "inline": true})
	}
	if inc.Cause != nil {
		fields = append(fields, map[string]any{"name": "Cause", "value": inc.Cause.Error()})
	}
	return map[string]any{
		"content": "Unexpected incident: " + inc.Message,
		"embeds": []map[string]any{
			{
				"title":       "Unexpected incident",
				"description": inc.Message,
				"fields":      fields,
			},
		},
	}
}
