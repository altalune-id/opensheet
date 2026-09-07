package todo

import (
	"context"
	"log/slog"
	"time"

	"altalune.id/template/scheduler"
)

// sweepCron fires at 00:00, 06:00, 12:00 and 18:00 in the configured location.
const sweepCron = "0 */6 * * *"

// sweepJobName keys this job in logs, metrics, the leader lock, the CLI, and scheduler.jobs config overrides.
const sweepJobName = "todo-autocomplete-stale"

// Scheduler adapts *Service to scheduler.Provider.
type Scheduler struct {
	svc *Service
	log *slog.Logger
	loc scheduler.LocationFunc
}

// NewScheduler binds svc to the stale-todo sweep job, resolving its zone through loc.
func NewScheduler(svc *Service, log *slog.Logger, loc scheduler.LocationFunc) *Scheduler {
	if loc == nil {
		loc = scheduler.FixedLocation(time.UTC)
	}
	return &Scheduler{svc: svc, log: log.With("module", "todo"), loc: loc}
}

// SchedulerJobs implements scheduler.Provider.
func (a *Scheduler) SchedulerJobs() []scheduler.Job {
	return []scheduler.Job{{
		Name:      sweepJobName,
		Scope:     scheduler.ScopeTenant,
		Schedule:  scheduler.MustCronExpr(sweepCron, a.loc(sweepJobName)),
		Timeout:   2 * time.Minute,
		Singleton: true,
		Run: func(ctx context.Context) error {
			n, err := a.svc.AutoCompleteStale(ctx, StaleAfter)
			if err != nil {
				return err
			}
			if n > 0 {
				a.log.LogAttrs(ctx, slog.LevelInfo, "todo.autocomplete_stale",
					slog.Int("swept", n),
					slog.Duration("stale_after", StaleAfter),
				)
			}
			return nil
		},
	}}
}
