package cli

import (
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// NOTE: precedence is --url > OPENSHEET_URL > the sole saved profile > http.baseURL from config.
func resolveURL(cmd *cobra.Command) string {
	if v := explicitURL(cmd); v != "" {
		return v
	}
	cfg := configFromCtx(cmd.Context())
	if cfg == nil {
		return ""
	}
	if v := savedProfileURL(cfg.Session.Path); v != "" {
		return v
	}
	return cfg.HTTP.BaseURL
}

func explicitURL(cmd *cobra.Command) string {
	if f := cmd.Root().PersistentFlags().Lookup("url"); f != nil && f.Changed {
		return strings.TrimSpace(f.Value.String())
	}
	return strings.TrimSpace(os.Getenv("OPENSHEET_URL"))
}

func savedProfileURL(path string) string {
	if path == "" {
		return ""
	}
	sf, err := loadSessionFile(path)
	if err != nil || sf == nil || len(sf.Profiles) != 1 {
		return ""
	}
	for u := range sf.Profiles {
		return u
	}
	return ""
}

func registerURLFlagCompletion(root *cobra.Command) {
	_ = root.RegisterFlagCompletionFunc("url", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	})
}
