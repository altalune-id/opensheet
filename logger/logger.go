package logger

import (
	"log/slog"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
)

// New builds a *slog.Logger honoring cfg, wrapped with ContextHandler for auto-injected request_id / trace_id / span_id.
func New(cfg Config) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level:       parseLevel(cfg.Level),
		AddSource:   cfg.AddSource,
		ReplaceAttr: Redact(cfg),
	}
	var base slog.Handler
	if strings.EqualFold(cfg.Format, "text") && isatty.IsTerminal(os.Stderr.Fd()) {
		base = slog.NewTextHandler(os.Stderr, opts)
	} else {
		base = slog.NewJSONHandler(os.Stderr, opts)
	}
	return slog.New(ContextHandler{Handler: base})
}
