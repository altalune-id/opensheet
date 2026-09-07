package notify

import (
	"log/slog"
	"strings"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/mailer"
)

// Build dispatches each SinkConfig into a concrete ReportSink.
func Build(cfg Config, mail mailer.Mailer, log *slog.Logger) []apperror.ReportSink {
	if log == nil {
		log = slog.Default()
	}
	out := make([]apperror.ReportSink, 0, len(cfg.Sinks))
	for _, s := range cfg.Sinks {
		kind := strings.ToLower(strings.TrimSpace(s.Kind))
		switch kind {
		case "stdout":
			out = append(out, NewStdoutSink(log))
		case "slack":
			if s.WebhookURL == "" {
				log.Warn("notify: slack sink missing webhookUrl", slog.String("kind", s.Kind))
				continue
			}
			out = append(out, NewSlackSink(s.WebhookURL, log))
		case "discord":
			if s.WebhookURL == "" {
				log.Warn("notify: discord sink missing webhookUrl", slog.String("kind", s.Kind))
				continue
			}
			out = append(out, NewDiscordSink(s.WebhookURL, log))
		case "googlechat":
			if s.WebhookURL == "" {
				log.Warn("notify: googlechat sink missing webhookUrl", slog.String("kind", s.Kind))
				continue
			}
			out = append(out, NewGoogleChatSink(s.WebhookURL, log))
		case "email":
			if mail == nil {
				log.Warn("notify: email sink configured but no Mailer provided", slog.String("kind", s.Kind))
				continue
			}
			if len(s.To) == 0 {
				log.Warn("notify: email sink missing to addresses", slog.String("kind", s.Kind))
				continue
			}
			out = append(out, NewEmailSink(mail, s.To, s.From, log))
		default:
			log.Warn("notify: unknown sink kind", slog.String("kind", s.Kind))
		}
	}
	return out
}
