package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"altalune.id/opensheet/internal/platform/config"
)

// NOTE: precedence is --token, then --token-file, then OPENSHEET_TOKEN; an empty result falls through to the profile.
func resolveToken(cmd *cobra.Command) (string, error) {
	root := cmd.Root()
	if f := root.PersistentFlags().Lookup("token"); f != nil && f.Changed {
		return strings.TrimSpace(f.Value.String()), nil
	}
	if f := root.PersistentFlags().Lookup("token-file"); f != nil && f.Changed {
		b, err := os.ReadFile(f.Value.String()) //nolint:gosec // operator-supplied path, not tainted input.
		if err != nil {
			return "", fmt.Errorf("cli: read token file: %w", err)
		}
		tok := strings.TrimSpace(string(b))
		if tok == "" {
			return "", fmt.Errorf("cli: token file %q is empty", f.Value.String())
		}
		return tok, nil
	}
	return strings.TrimSpace(os.Getenv(config.EnvVar("token"))), nil
}
