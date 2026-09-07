// Package render dispatches CLI output to text tables, JSON, or NDJSON writers.
package render

import (
	"os"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// Format names one of the three canonical CLI output modes.
type Format string

const (
	FormatText   Format = "text"
	FormatJSON   Format = "json"
	FormatNDJSON Format = "ndjson"
)

// EnvVar is the environment variable read when --output is unset.
const EnvVar = "ALT_OUTPUT"

// Detect picks the output format: --output flag > ALT_OUTPUT env > TTY heuristic; unknown falls back to text.
func Detect(cmd *cobra.Command) Format {
	if raw := lookupFlag(cmd, "output"); raw != "" {
		return normalize(raw)
	}
	if raw := os.Getenv(EnvVar); raw != "" {
		return normalize(raw)
	}
	if isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd()) {
		return FormatText
	}
	return FormatJSON
}

func normalize(s string) Format {
	switch Format(strings.ToLower(strings.TrimSpace(s))) {
	case FormatJSON:
		return FormatJSON
	case FormatNDJSON:
		return FormatNDJSON
	default:
		return FormatText
	}
}

func lookupFlag(cmd *cobra.Command, name string) string {
	if cmd == nil {
		return ""
	}
	if f := cmd.Flag(name); f != nil && f.Changed {
		return f.Value.String()
	}
	root := cmd.Root()
	if root == nil {
		return ""
	}
	if f := root.PersistentFlags().Lookup(name); f != nil && f.Changed {
		return f.Value.String()
	}
	return ""
}
