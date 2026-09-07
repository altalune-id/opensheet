package notify

import (
	"context"
	"encoding/json"
	"log/slog"

	"altalune.id/template/internal/apperror"
)

// SlackSink posts incident notifications to a Slack incoming webhook.
type SlackSink struct{ *webhookSink }

// NewSlackSink builds a SlackSink writing to webhookURL.
func NewSlackSink(webhookURL string, log *slog.Logger) *SlackSink {
	return &SlackSink{webhookSink: newWebhookSink("slack", webhookURL, log)}
}

// Report enqueues an incident for delivery; non-blocking, drops on overflow.
func (s *SlackSink) Report(ctx context.Context, inc *apperror.Incident) {
	if inc == nil || s.url == "" {
		return
	}
	payload, err := json.Marshal(buildSlackPayload(inc))
	if err != nil {
		s.log.ErrorContext(ctx, "notify: slack marshal", slog.Any("error", err))
		return
	}
	s.enqueue(ctx, payload)
}

func buildSlackPayload(inc *apperror.Incident) map[string]any {
	fields := []map[string]any{
		{"type": "mrkdwn", "text": "*Code*\n" + inc.Code},
	}
	if inc.RequestID != "" {
		fields = append(fields, map[string]any{"type": "mrkdwn", "text": "*Request ID*\n" + inc.RequestID})
	}
	if inc.TraceID != "" {
		fields = append(fields, map[string]any{"type": "mrkdwn", "text": "*Trace ID*\n" + inc.TraceID})
	}
	blocks := []map[string]any{
		{
			"type": "header",
			"text": map[string]any{"type": "plain_text", "text": "Unexpected incident"},
		},
		{
			"type": "section",
			"text": map[string]any{"type": "mrkdwn", "text": "*Message:* " + inc.Message},
		},
		{
			"type":   "section",
			"fields": fields,
		},
	}
	if inc.Cause != nil {
		blocks = append(blocks, map[string]any{
			"type": "section",
			"text": map[string]any{"type": "mrkdwn", "text": "*Cause:*\n```" + inc.Cause.Error() + "```"},
		})
	}
	return map[string]any{"blocks": blocks}
}
