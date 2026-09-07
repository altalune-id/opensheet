package logger

import (
	"log/slog"
	"regexp"
	"strings"
)

var defaultRedactRe = regexp.MustCompile(`(?i)password|secret|token|cookie|authorization|api[_-]?key|bearer`)

// Redact returns a slog ReplaceAttr that masks values whose attr key matches the sensitive-fields regex.
func Redact(cfg Config) func(groups []string, a slog.Attr) slog.Attr {
	extra := compileExtras(cfg.RedactPatterns)
	return func(_ []string, a slog.Attr) slog.Attr {
		if defaultRedactRe.MatchString(a.Key) || matchAny(extra, a.Key) {
			return slog.String(a.Key, "<redacted>")
		}
		return a
	}
}

func compileExtras(patterns []string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		if strings.TrimSpace(p) == "" {
			continue
		}
		if re, err := regexp.Compile("(?i)" + p); err == nil {
			out = append(out, re)
		}
	}
	return out
}

func matchAny(res []*regexp.Regexp, s string) bool {
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}
