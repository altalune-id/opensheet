package cli

import (
	"github.com/spf13/cobra"

	"altalune.id/template/internal/cli/render"
	"altalune.id/template/version"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Print altempl version info",
		GroupID: "meta",
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := version.Get()
			switch render.Detect(cmd) {
			case render.FormatJSON, render.FormatNDJSON:
				return render.JSON(cmd.OutOrStdout(), map[string]string{
					"version":   info.Version,
					"commit":    info.Commit,
					"buildTime": info.BuildTime,
				})
			default:
				cmd.Println(version.String())
				return nil
			}
		},
	}
}
