package logger

import (
	"fmt"
	"log/slog"
	"strings"
)

// Config configures the platform slog logger.
type Config struct {
	Level          string   `yaml:"level"          mapstructure:"level"`
	Format         string   `yaml:"format"         mapstructure:"format"`
	AddSource      bool     `yaml:"addSource"      mapstructure:"addSource"`
	RedactPatterns []string `yaml:"redactPatterns" mapstructure:"redactPatterns" awareness:"bootstrap"`
}

// Validate reports whether Config carries recognized level and format values.
func (c *Config) Validate() error {
	switch strings.ToLower(c.Level) {
	case "", "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("logger: unknown level %q", c.Level)
	}
	switch strings.ToLower(c.Format) {
	case "", "json", "text":
	default:
		return fmt.Errorf("logger: unknown format %q", c.Format)
	}
	return nil
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
