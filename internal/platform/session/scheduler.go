package session

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
const sweepJobName = "session-sweep"

// Scheduler adapts a Store to scheduler.Provider.
type Scheduler struct {
	store Store
	log   *slog.Logger
}

// NewScheduler binds store to the expired-session sweep job.
func NewScheduler(store Store, log *slog.Logger) *Scheduler {
	return &Scheduler{store: store, log: log.With("module", "session")}
}

// SchedulerJobs implements scheduler.Provider.
func (a *Scheduler) SchedulerJobs() []scheduler.Job {
	return []scheduler.Job{{
		Name:      sweepJobName,
		Scope:     scheduler.ScopeSystem,
		Schedule:  scheduler.MustEveryInterval(sweepEvery, sweepJitter),
		Timeout:   sweepTimeout,
		Singleton: true,
		Run: func(ctx context.Context) error {
			n, err := a.store.DeleteExpired(ctx)
			if err != nil {
				return err
			}
			if n > 0 {
				a.log.LogAttrs(ctx, slog.LevelInfo, "session.sweep", slog.Int("deleted", n))
			}
			return nil
		},
	}}
}
