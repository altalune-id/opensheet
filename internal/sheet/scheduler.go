package sheet

import (
	"context"
	"log/slog"
	"time"

	"altalune.id/opensheet/scheduler"
)

const (
	sweepEvery   = time.Hour
	sweepJitter  = 5 * time.Minute
	sweepTimeout = 2 * time.Minute
)

// sweepJobName keys this job in logs, metrics, the leader lock, the CLI, and scheduler.jobs config overrides.
const sweepJobName = "sheet-write-attempt-sweep"

// Scheduler adapts *Service to scheduler.Provider.
type Scheduler struct {
	svc *Service
	log *slog.Logger
}

// NewScheduler binds svc to the expired-write-attempt sweep job.
func NewScheduler(svc *Service, log *slog.Logger) *Scheduler {
	return &Scheduler{svc: svc, log: log.With("module", "sheet")}
}

// SchedulerJobs implements scheduler.Provider.
func (a *Scheduler) SchedulerJobs() []scheduler.Job {
	return []scheduler.Job{{
		Name:      sweepJobName,
		Scope:     scheduler.ScopeTenant,
		Schedule:  scheduler.MustEveryInterval(sweepEvery, sweepJitter),
		Timeout:   sweepTimeout,
		Singleton: true,
		Run: func(ctx context.Context) error {
			n, err := a.svc.SweepWriteAttempts(ctx)
			if err != nil {
				return err
			}
			if n > 0 {
				a.log.LogAttrs(ctx, slog.LevelInfo, "sheet.write_attempt_sweep", slog.Int("deleted", n))
			}
			return nil
		},
	}}
}
