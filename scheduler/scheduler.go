package scheduler

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"altalune.id/template/reqid"
)

const defaultShutdownGrace = 30 * time.Second

// Scope selects how the Runner invokes a Job.
type Scope string

const (
	// ScopeSystem invokes Run once per tick with no tenant bound to ctx.
	ScopeSystem Scope = "system"
	// ScopeTenant invokes Run once per tenant per tick, each with a tenant-bound ctx.
	ScopeTenant Scope = "tenant"
)

// Status is the outcome of one Job invocation.
type Status string

const (
	// StatusSuccess means Run returned nil.
	StatusSuccess Status = "success"
	// StatusError means Run returned a non-nil error.
	StatusError Status = "error"
	// StatusOverlap means the tick was dropped because a prior run was still in flight.
	StatusOverlap Status = "overlap"
	// StatusNotLeader means another process held the Job's lock.
	StatusNotLeader Status = "not_leader"
	// StatusPanic means Run panicked and the Runner recovered it.
	StatusPanic Status = "panic"
)

// Job is one periodic unit of work. Construct with keyed fields only.
type Job struct {
	Name     string
	Scope    Scope
	Schedule Schedule
	// NOTE: Timeout caps one invocation of Run — per tenant under ScopeTenant, not per tick. Zero disables it.
	Timeout   time.Duration
	Singleton bool
	Run       func(ctx context.Context) error
}

// Options configures a Runner. Construct with keyed fields only.
type Options struct {
	Logger        *slog.Logger
	Reporter      ErrorReporter
	Meter         metric.Meter
	Tenants       Tenants
	Locker        Locker
	ShutdownGrace time.Duration
	Now           func() time.Time
}

// Runner owns one goroutine per registered Job plus the metric handles.
type Runner struct {
	log     *slog.Logger
	report  ErrorReporter
	tenants Tenants
	locker  Locker
	grace   time.Duration
	now     func() time.Time

	mu   sync.RWMutex
	jobs map[string]*jobEntry

	runs       metric.Int64Counter
	duration   metric.Float64Histogram
	lastOK     metric.Int64Gauge
	tenantRuns metric.Int64Counter

	started  atomic.Bool
	drainMu  sync.Mutex
	draining bool
	wg       sync.WaitGroup
}

type jobEntry struct {
	job      Job
	inFlight atomic.Bool
}

// New constructs a Runner from opts. Logger is required; Meter may be nil, which disables metrics.
func New(opts Options) (*Runner, error) {
	if opts.Logger == nil {
		return nil, errors.New("scheduler: Options.Logger is required")
	}
	r := &Runner{
		log:     opts.Logger,
		report:  opts.Reporter,
		tenants: opts.Tenants,
		locker:  opts.Locker,
		grace:   cmp.Or(opts.ShutdownGrace, defaultShutdownGrace),
		now:     opts.Now,
		jobs:    make(map[string]*jobEntry),
	}
	if r.now == nil {
		r.now = time.Now
	}
	if opts.Meter != nil {
		if err := r.initMetrics(opts.Meter); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (r *Runner) initMetrics(m metric.Meter) error {
	var err error
	if r.runs, err = m.Int64Counter("scheduler.job_runs_total",
		metric.WithDescription("Scheduler job invocations by job, scope and outcome.")); err != nil {
		return fmt.Errorf("scheduler: job_runs_total: %w", err)
	}
	if r.duration, err = m.Float64Histogram("scheduler.job_duration_seconds",
		metric.WithDescription("Wall-clock duration of a scheduler job run."),
		metric.WithUnit("s")); err != nil {
		return fmt.Errorf("scheduler: job_duration_seconds: %w", err)
	}
	if r.lastOK, err = m.Int64Gauge("scheduler.job_last_success_timestamp_seconds",
		metric.WithDescription("Unix seconds of the most recent successful run."),
		metric.WithUnit("s")); err != nil {
		return fmt.Errorf("scheduler: job_last_success_timestamp_seconds: %w", err)
	}
	if r.tenantRuns, err = m.Int64Counter("scheduler.tenant_runs_total",
		metric.WithDescription("Per-tenant invocations of ScopeTenant jobs by outcome.")); err != nil {
		return fmt.Errorf("scheduler: tenant_runs_total: %w", err)
	}
	return nil
}

// Register adds j to the set Run will start.
func (r *Runner) Register(j Job) error {
	if j.Name == "" {
		return errors.New("scheduler.Register: Job.Name is required")
	}
	if j.Scope != ScopeSystem && j.Scope != ScopeTenant {
		return fmt.Errorf("scheduler.Register(%q): unknown Job.Scope %q", j.Name, j.Scope)
	}
	if j.Schedule == nil {
		return fmt.Errorf("scheduler.Register(%q): Job.Schedule is required", j.Name)
	}
	if j.Run == nil {
		return fmt.Errorf("scheduler.Register(%q): Job.Run is required", j.Name)
	}
	if j.Timeout < 0 {
		return fmt.Errorf("scheduler.Register(%q): Job.Timeout must be non-negative, got %s", j.Name, j.Timeout)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.jobs[j.Name]; exists {
		return &DuplicateJobError{Name: j.Name}
	}
	r.jobs[j.Name] = &jobEntry{job: j}
	return nil
}

// Jobs returns a copy of every registered Job, sorted by name.
func (r *Runner) Jobs() []Job {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Job, 0, len(r.jobs))
	for _, e := range r.jobs {
		out = append(out, e.job)
	}
	slices.SortFunc(out, func(a, b Job) int { return cmp.Compare(a.Name, b.Name) })
	return out
}

// Name implements worker.Worker.
func (r *Runner) Name() string { return "scheduler" }

// Run starts one goroutine per Job, blocks until ctx is canceled, then drains within ShutdownGrace.
func (r *Runner) Run(ctx context.Context) error {
	if !r.started.CompareAndSwap(false, true) {
		return errors.New("scheduler: Run already called on this Runner")
	}

	r.mu.RLock()
	entries := make([]*jobEntry, 0, len(r.jobs))
	for _, e := range r.jobs {
		entries = append(entries, e)
	}
	r.mu.RUnlock()

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.job.Scope == ScopeTenant && r.tenants == nil {
			return fmt.Errorf("scheduler: job %q has ScopeTenant but Options.Tenants is nil", e.job.Name)
		}
		names = append(names, e.job.Name)
	}
	slices.Sort(names)
	r.log.InfoContext(ctx, "scheduler.started", "jobs", len(entries), "job_names", names)

	var loops sync.WaitGroup
	for _, e := range entries {
		loops.Go(func() { r.loop(ctx, e) })
	}
	loops.Wait()

	drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.grace)
	defer cancel()
	return r.Shutdown(drainCtx)
}

// Shutdown refuses new runs and blocks until in-flight runs finish or ctx expires.
func (r *Runner) Shutdown(ctx context.Context) error {
	r.drainMu.Lock()
	r.draining = true
	r.drainMu.Unlock()

	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		r.log.InfoContext(ctx, "scheduler.drained")
		return nil
	case <-ctx.Done():
		r.log.WarnContext(ctx, "scheduler.drain_timeout")
		return fmt.Errorf("scheduler.Shutdown: %w", ctx.Err())
	}
}

// RunOnce invokes the named Job now, bypassing its Schedule but honouring Timeout, single-flight and Singleton.
func (r *Runner) RunOnce(ctx context.Context, name string) error {
	r.mu.RLock()
	e, ok := r.jobs[name]
	r.mu.RUnlock()
	if !ok {
		return &UnknownJobError{Name: name}
	}
	return r.runEntry(ctx, e, true)
}

func (r *Runner) loop(ctx context.Context, e *jobEntry) {
	for {
		delay := max(time.Until(e.job.Schedule.Next(r.now())), 0)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			_ = r.runEntry(ctx, e, false)
		}
	}
}

// NOTE: reserving under drainMu closes the WaitGroup Add-during-Wait race with Shutdown.
func (r *Runner) reserve() bool {
	r.drainMu.Lock()
	defer r.drainMu.Unlock()
	if r.draining {
		return false
	}
	r.wg.Add(1)
	return true
}

func (r *Runner) runEntry(ctx context.Context, e *jobEntry, manual bool) error {
	if !r.reserve() {
		if manual {
			return &DrainingError{}
		}
		return nil
	}
	defer r.wg.Done()

	if !e.inFlight.CompareAndSwap(false, true) {
		r.log.WarnContext(ctx, "scheduler.job_overlap", "job", e.job.Name)
		r.recordRun(ctx, e.job, StatusOverlap, 0)
		if manual {
			return &BusyError{Name: e.job.Name}
		}
		return nil
	}
	defer e.inFlight.Store(false)

	return r.invoke(ctx, e, manual)
}

func (r *Runner) invoke(ctx context.Context, e *jobEntry, manual bool) error {
	if e.job.Singleton && r.locker != nil {
		release, acquired, err := r.locker.TryLock(ctx, e.job.Name)
		if err != nil {
			r.logRun(ctx, e.job, StatusError, 0, fmt.Errorf("scheduler: acquire lock: %w", err), nil)
			r.recordRun(ctx, e.job, StatusError, 0)
			return err
		}
		if !acquired {
			r.log.DebugContext(ctx, "scheduler.job_not_leader", "job", e.job.Name)
			r.recordRun(ctx, e.job, StatusNotLeader, 0)
			if manual {
				return &NotLeaderError{Name: e.job.Name}
			}
			return nil
		}
		defer release()
	}

	ctx, _ = reqid.Ensure(ctx)
	start := r.now()

	var (
		err   error
		extra []slog.Attr
	)
	switch e.job.Scope {
	case ScopeTenant:
		extra, err = r.fanOut(ctx, e.job)
	default:
		err = r.callGuarded(ctx, e.job, e.job.Run)
	}

	elapsed := r.now().Sub(start)
	status := statusFor(err)
	r.logRun(ctx, e.job, status, elapsed, err, extra)
	r.recordRun(ctx, e.job, status, elapsed)
	if err == nil {
		r.recordLastOK(ctx, e.job.Name)
	}
	return err
}

func (r *Runner) fanOut(ctx context.Context, j Job) ([]slog.Attr, error) {
	var (
		total  int
		failed []error
	)
	enumErr := r.tenants.Each(ctx, func(tctx context.Context, tenantID string) error {
		total++
		tctx = reqid.WithContext(tctx, reqid.New())
		err := r.callGuarded(tctx, j, j.Run)
		r.recordTenantRun(tctx, j.Name, statusFor(err))
		if err != nil {
			failed = append(failed, fmt.Errorf("tenant %s: %w", tenantID, err))
		}
		return nil
	})

	attrs := []slog.Attr{
		slog.Int("tenants_total", total),
		slog.Int("tenants_failed", len(failed)),
	}
	if enumErr != nil {
		return attrs, fmt.Errorf("scheduler: enumerate tenants: %w", enumErr)
	}
	return attrs, errors.Join(failed...)
}

// NOTE: recovers panics so one domain bug cannot take the process down with the HTTP listener.
func (r *Runner) callGuarded(ctx context.Context, j Job, fn func(context.Context) error) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = &PanicError{Job: j.Name, Value: rec, Stack: string(debug.Stack())}
		}
	}()
	return r.callOnce(ctx, j, fn)
}

func statusFor(err error) Status {
	if err == nil {
		return StatusSuccess
	}
	if IsPanicError(err) {
		return StatusPanic
	}
	return StatusError
}

func (r *Runner) recordTenantRun(ctx context.Context, jobName string, status Status) {
	if r.tenantRuns == nil {
		return
	}
	r.tenantRuns.Add(ctx, 1, metric.WithAttributes(
		attribute.String("job", jobName),
		attribute.String("status", string(status)),
	))
}

func (r *Runner) callOnce(ctx context.Context, j Job, fn func(context.Context) error) error {
	if j.Timeout <= 0 {
		return fn(ctx)
	}
	runCtx, cancel := context.WithTimeout(ctx, j.Timeout)
	defer cancel()
	return fn(runCtx)
}

func (r *Runner) logRun(ctx context.Context, j Job, status Status, elapsed time.Duration, err error, extra []slog.Attr) {
	attrs := append([]slog.Attr{
		slog.String("job", j.Name),
		slog.String("scope", string(j.Scope)),
		slog.String("status", string(status)),
		slog.Int64("duration_ms", elapsed.Milliseconds()),
	}, extra...)
	if err == nil {
		r.log.LogAttrs(ctx, slog.LevelInfo, "scheduler.job", attrs...)
		return
	}
	r.log.LogAttrs(ctx, slog.LevelError, "scheduler.job", append(attrs, slog.Any("error", err))...)
	if r.report != nil {
		r.report.Report(ctx, "scheduler.job: "+j.Name, err, slog.String("job", j.Name))
	}
}

func (r *Runner) recordRun(ctx context.Context, j Job, status Status, elapsed time.Duration) {
	if r.runs == nil {
		return
	}
	r.runs.Add(ctx, 1, metric.WithAttributes(
		attribute.String("job", j.Name),
		attribute.String("scope", string(j.Scope)),
		attribute.String("status", string(status)),
	))
	if status == StatusOverlap || status == StatusNotLeader {
		return
	}
	r.duration.Record(ctx, elapsed.Seconds(), metric.WithAttributes(
		attribute.String("job", j.Name),
		attribute.String("scope", string(j.Scope)),
	))
}

func (r *Runner) recordLastOK(ctx context.Context, name string) {
	if r.lastOK == nil {
		return
	}
	r.lastOK.Record(ctx, r.now().Unix(), metric.WithAttributes(attribute.String("job", name)))
}
