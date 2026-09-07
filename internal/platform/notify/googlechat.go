package notify

import (
	"context"
	"encoding/json"
	"log/slog"

	"altalune.id/template/internal/apperror"
)

// GoogleChatSink posts incident notifications to a Google Chat webhook.
type GoogleChatSink struct{ *webhookSink }

// NewGoogleChatSink builds a GoogleChatSink writing to webhookURL.
func NewGoogleChatSink(webhookURL string, log *slog.Logger) *GoogleChatSink {
	return &GoogleChatSink{webhookSink: newWebhookSink("googlechat", webhookURL, log)}
}

// Report enqueues an incident for delivery; non-blocking, drops on overflow.
func (s *GoogleChatSink) Report(ctx context.Context, inc *apperror.Incident) {
	if inc == nil || s.url == "" {
		return
	}
	payload, err := json.Marshal(buildGoogleChatPayload(inc))
	if err != nil {
		s.log.ErrorContext(ctx, "notify: googlechat marshal", slog.Any("error", err))
		return
	}
	s.enqueue(ctx, payload)
}

func buildGoogleChatPayload(inc *apperror.Incident) map[string]any {
	widgets := []map[string]any{
		{"keyValue": map[string]any{"topLabel": "Code", "content": inc.Code}},
		{"keyValue": map[string]any{"topLabel": "Message", "content": inc.Message}},
	}
	if inc.RequestID != "" {
		widgets = append(widgets, map[string]any{"keyValue": map[string]any{"topLabel": "Request ID", "content": inc.RequestID}})
	}
	if inc.TraceID != "" {
		widgets = append(widgets, map[string]any{"keyValue": map[string]any{"topLabel": "Trace ID", "content": inc.TraceID}})
	}
	if inc.Cause != nil {
		widgets = append(widgets, map[string]any{"keyValue": map[string]any{"topLabel": "Cause", "content": inc.Cause.Error()}})
	}
	return map[string]any{
		"cards": []map[string]any{
			{
				"header": map[string]any{
					"title":    "Unexpected incident",
					"subtitle": inc.Code,
				},
				"sections": []map[string]any{
					{"widgets": widgets},
				},
			},
		},
	}
}
