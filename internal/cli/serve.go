package cli

import (
	"github.com/spf13/cobra"

	"altalune.id/opensheet/internal/boot"
)

func newServeCmd(bootServer ServerBootFn) *cobra.Command {
	var noScheduler, schedulerOnly, noConsumer, consumerOnly bool

	cmd := &cobra.Command{
		Use:     "serve",
		Short:   "Run the opensheet HTTP server (web UI + Connect API + workers)",
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
				boot.WithConsumer(!noConsumer),
				boot.WithConsumerOnly(consumerOnly),
			)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()

			cmd.Printf("opensheet: listening on %s (basePath=%q, mode=%s, scheduler=%t, consumer=%t)\n",
				s.Cfg.HTTP.Addr, s.Cfg.HTTP.BasePath, s.Cfg.Mode, s.Scheduler != nil, s.Consumer != nil)
			return s.Run(cmd.Context())
		},
	}

	cmd.Flags().BoolVar(&noScheduler, "no-scheduler", false, "do not start the periodic-job runner")
	cmd.Flags().BoolVar(&schedulerOnly, "scheduler-only", false, "run only the scheduler and a health endpoint")
	cmd.Flags().BoolVar(&noConsumer, "no-consumer", false, "do not start the queue job consumer")
	cmd.Flags().BoolVar(&consumerOnly, "consumer-only", false, "run only the queue consumer and a health endpoint")
	cmd.MarkFlagsMutuallyExclusive("no-scheduler", "scheduler-only")
	cmd.MarkFlagsMutuallyExclusive("no-consumer", "consumer-only")
	cmd.MarkFlagsMutuallyExclusive("scheduler-only", "consumer-only")

	return cmd
}
