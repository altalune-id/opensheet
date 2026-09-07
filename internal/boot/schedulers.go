package boot

import (
	"context"
	"fmt"
	"log/slog"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/platform"
	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/db"
	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/internal/todo"
	"altalune.id/template/scheduler"
)

// schedulerDomains names each slot in schedulerProviders, in order.
//
//nolint:gochecknoglobals // Immutable wiring manifest; not runtime state.
var schedulerDomains = []string{"todo"}

func schedulerProviders(s *Services, loc scheduler.LocationFunc, log *slog.Logger) []scheduler.Provider {
	return []scheduler.Provider{
		todo.NewScheduler(s.Todos, log, loc),
	}
}

func assertSchedulerWiring(ps []scheduler.Provider) error {
	if len(ps) != len(schedulerDomains) {
		return fmt.Errorf("scheduler wiring: %d providers for %d domains %v",
			len(ps), len(schedulerDomains), schedulerDomains)
	}
	missing := make([]string, 0, len(ps))
	for i, p := range ps {
		if p == nil {
			missing = append(missing, schedulerDomains[i])
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("scheduler wiring: domains missing a provider: %v", missing)
	}
	return nil
}

func buildScheduler(
	cfg *config.Config,
	k *platform.Kernel,
	s *Services,
	log *slog.Logger,
) (*scheduler.Runner, error) {
	loc, err := cfg.Scheduler.Locations()
	if err != nil {
		return nil, err
	}

	providers := schedulerProviders(s, loc, log)
	if wErr := assertSchedulerWiring(providers); wErr != nil {
		return nil, wErr
	}

	runner, err := scheduler.New(scheduler.Options{
		Logger:        log,
		Reporter:      reporterAdapter{report: k.Reporter.Unexpected, log: log},
		Meter:         k.Meter,
		Tenants:       tenant.NewEnumerator(tenant.NewOrgReader(k.Pool, cfg.DB.Driver, cfg.DB.Schema, cfg.DB.TablePrefix), log),
		Locker:        db.NewLocker(cfg.DB, k.Pool, log),
		ShutdownGrace: cfg.Scheduler.ShutdownGrace,
	})
	if err != nil {
		return nil, fmt.Errorf("boot: scheduler: %w", err)
	}

	wallClock := map[string]bool{}
	for _, p := range providers {
		for _, j := range p.SchedulerJobs() {
			if rErr := runner.Register(j); rErr != nil {
				return nil, fmt.Errorf("boot: register job %q: %w", j.Name, rErr)
			}
			wallClock[j.Name] = scheduler.UsesWallClock(j.Schedule)
		}
	}
	warnUnusedTimezoneOverrides(cfg, wallClock, log)
	return runner, nil
}

func warnUnusedTimezoneOverrides(cfg *config.Config, wallClock map[string]bool, log *slog.Logger) {
	for name, jc := range cfg.Scheduler.Jobs {
		if jc.Timezone == "" {
			continue
		}
		isWallClock, registered := wallClock[name]
		if !registered {
			log.Warn("boot: scheduler.jobs timezone override names an unknown job",
				slog.String("job", name), slog.String("timezone", jc.Timezone))
			continue
		}
		if !isWallClock {
			log.Warn("boot: scheduler.jobs timezone override ignored - the job uses an interval schedule",
				slog.String("job", name), slog.String("timezone", jc.Timezone))
		}
	}
}

var _ scheduler.ErrorReporter = reporterAdapter{}

type reporterAdapter struct {
	report apperror.UnexpectedFunc
	log    *slog.Logger
}

func (a reporterAdapter) Report(ctx context.Context, message string, cause error, attrs ...any) {
	if alreadyReported(cause) {
		a.log.ErrorContext(ctx, message, append([]any{slog.Any("error", cause)}, attrs...)...)
		return
	}
	_ = a.report(ctx, message, cause, attrs...)
}

func alreadyReported(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := apperror.AsAppError(err); ok {
		return true
	}
	//nolint:errorlint // single-hop check: AsAppError already walked this node's %w chain.
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return false
	}
	causes := joined.Unwrap()
	if len(causes) == 0 {
		return false
	}
	for _, c := range causes {
		if !alreadyReported(c) {
			return false
		}
	}
	return true
}
