package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/boot"
	"altalune.id/template/internal/cli/render"
	"altalune.id/template/scheduler"
)

func newSchedulerCmd(bootServer ServerBootFn) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "scheduler",
		Short:   "Inspect and run periodic jobs",
		GroupID: "runtime",
	}
	cmd.AddCommand(
		newSchedulerListCmd(bootServer),
		newSchedulerRunCmd(bootServer),
	)
	return cmd
}

func newSchedulerListCmd(bootServer ServerBootFn) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every registered job",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			runner, closeFn, err := schedulerRunner(cmd, bootServer)
			if err != nil {
				return err
			}
			defer closeFn()

			jobs := runner.Jobs()
			if render.Detect(cmd) != render.FormatText {
				out := make([]map[string]any, 0, len(jobs))
				for _, j := range jobs {
					out = append(out, map[string]any{
						"name":      j.Name,
						"scope":     string(j.Scope),
						"schedule":  j.Schedule.String(),
						"timeout":   j.Timeout.String(),
						"singleton": j.Singleton,
					})
				}
				return render.JSON(cmd.OutOrStdout(), out)
			}

			rows := make([][]string, 0, len(jobs))
			for _, j := range jobs {
				rows = append(rows, []string{
					j.Name, string(j.Scope), j.Schedule.String(),
					j.Timeout.String(), fmt.Sprintf("%t", j.Singleton),
				})
			}
			return render.Table(cmd.OutOrStdout(),
				[]string{"NAME", "SCOPE", "SCHEDULE", "TIMEOUT", "SINGLETON"}, rows)
		},
	}
}

func newSchedulerRunCmd(bootServer ServerBootFn) *cobra.Command {
	return &cobra.Command{
		Use:   "run <job>",
		Short: "Run one job immediately, bypassing its schedule",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runner, closeFn, err := schedulerRunner(cmd, bootServer)
			if err != nil {
				return err
			}
			defer closeFn()

			if err := runner.RunOnce(cmd.Context(), args[0]); err != nil {
				return schedulerAppError(err)
			}
			cmd.Printf("altempl: job %q completed\n", args[0])
			return nil
		},
	}
}

func schedulerRunner(cmd *cobra.Command, bootServer ServerBootFn) (runner *scheduler.Runner, closeFn func(), err error) {
	cfg, err := withCfg(cmd)
	if err != nil {
		return nil, nil, err
	}
	s, err := bootServer(cmd.Context(), cfg, boot.WithScheduler(true))
	if err != nil {
		return nil, nil, err
	}
	if s.Scheduler == nil {
		_ = s.Close()
		return nil, nil, apperror.New("scheduler.disabled",
			"the scheduler is disabled; set scheduler.enabled=true", codes.FailedPrecondition)
	}
	return s.Scheduler, func() { _ = s.Close() }, nil
}

// schedulerAppError maps scheduler's typed errors onto the CLI exit contract; scheduler/ cannot import apperror.
func schedulerAppError(err error) error {
	switch {
	case scheduler.IsUnknownJobError(err):
		return apperror.New("scheduler.unknown_job", err.Error(), codes.NotFound)
	case scheduler.IsBusyError(err), scheduler.IsNotLeaderError(err):
		return apperror.New("scheduler.conflict", err.Error(), codes.AlreadyExists)
	case scheduler.IsDrainingError(err):
		return apperror.New("scheduler.draining", err.Error(), codes.FailedPrecondition)
	default:
		return err
	}
}
