package cli

import (
	"github.com/spf13/cobra"

	"altalune.id/template/internal/boot"
)

func newServeCmd(bootServer ServerBootFn) *cobra.Command {
	var noScheduler, schedulerOnly bool

	cmd := &cobra.Command{
		Use:     "serve",
		Short:   "Run the altempl HTTP server (web UI + Connect API + workers)",
		GroupID: "runtime",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := withCfg(cmd)
			if err != nil {
				return err
			}
			s, err := bootServer(cmd.Context(), cfg,
				boot.WithScheduler(!noScheduler),
				boot.WithSchedulerOnly(schedulerOnly),
			)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()

			cmd.Printf("altempl: listening on %s (basePath=%q, mode=%s, scheduler=%t)\n",
				s.Cfg.HTTP.Addr, s.Cfg.HTTP.BasePath, s.Cfg.Mode, s.Scheduler != nil)
			return s.Run(cmd.Context())
		},
	}

	cmd.Flags().BoolVar(&noScheduler, "no-scheduler", false, "do not start the periodic-job runner")
	cmd.Flags().BoolVar(&schedulerOnly, "scheduler-only", false, "run only the scheduler and a health endpoint")
	cmd.MarkFlagsMutuallyExclusive("no-scheduler", "scheduler-only")

	return cmd
}
