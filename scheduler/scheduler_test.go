package scheduler_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"altalune.id/template/reqid"
	"altalune.id/template/scheduler"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newTestRunner(t *testing.T, mutate func(*scheduler.Options)) *scheduler.Runner {
	t.Helper()
	opts := scheduler.Options{Logger: testLogger()}
	if mutate != nil {
		mutate(&opts)
	}
	r, err := scheduler.New(opts)
	require.NoError(t, err)
	return r
}

func TestNew_RequiresLogger(t *testing.T) {
	_, err := scheduler.New(scheduler.Options{})
	require.Error(t, err)
}

func TestRegister_Validation(t *testing.T) {
	ok := func() scheduler.Job {
		return scheduler.Job{
			Name:     "j",
			Scope:    scheduler.ScopeSystem,
			Schedule: scheduler.MustEveryInterval(time.Minute, 0),
			Run:      func(context.Context) error { return nil },
		}
	}
	tests := []struct {
		name    string
		mutate  func(*scheduler.Job)
		wantErr bool
	}{
		{"valid", func(*scheduler.Job) {}, false},
		{"empty name", func(j *scheduler.Job) { j.Name = "" }, true},
		{"unknown scope", func(j *scheduler.Job) { j.Scope = "nope" }, true},
		{"nil schedule", func(j *scheduler.Job) { j.Schedule = nil }, true},
		{"nil run", func(j *scheduler.Job) { j.Run = nil }, true},
		{"negative timeout", func(j *scheduler.Job) { j.Timeout = -time.Second }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newTestRunner(t, nil)
			j := ok()
			tt.mutate(&j)
			err := r.Register(j)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestRegister_RejectsDuplicateName(t *testing.T) {
	r := newTestRunner(t, nil)
	j := scheduler.Job{
		Name:     "dup",
		Scope:    scheduler.ScopeSystem,
		Schedule: scheduler.MustEveryInterval(time.Minute, 0),
		Run:      func(context.Context) error { return nil },
	}
	require.NoError(t, r.Register(j))
	err := r.Register(j)
	require.True(t, scheduler.IsDuplicateJobError(err), "got %v", err)
}

func TestJobs_SortedByName(t *testing.T) {
	r := newTestRunner(t, nil)
	for _, n := range []string{"c", "a", "b"} {
		require.NoError(t, r.Register(scheduler.Job{
			Name:     n,
			Scope:    scheduler.ScopeSystem,
			Schedule: scheduler.MustEveryInterval(time.Minute, 0),
			Run:      func(context.Context) error { return nil },
		}))
	}
	got := make([]string, 0, 3)
	for _, j := range r.Jobs() {
		got = append(got, j.Name)
	}
	require.Equal(t, []string{"a", "b", "c"}, got)
}

func TestRunner_Name(t *testing.T) {
	require.Equal(t, "scheduler", newTestRunner(t, nil).Name())
}

func TestRun_FiresOnSchedule(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		r := newTestRunner(t, nil)
		require.NoError(t, r.Register(scheduler.Job{
			Name:     "tick",
			Scope:    scheduler.ScopeSystem,
			Schedule: scheduler.MustEveryInterval(time.Minute, 0),
			Run:      func(context.Context) error { calls.Add(1); return nil },
		}))

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- r.Run(ctx) }()

		time.Sleep(3*time.Minute + time.Second)
		require.Equal(t, int64(3), calls.Load())

		cancel()
		require.NoError(t, <-done)
	})
}

func TestRun_NoJobsBlocksUntilCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newTestRunner(t, nil)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- r.Run(ctx) }()
		time.Sleep(time.Minute)
		cancel()
		require.NoError(t, <-done)
	})
}

func TestRun_TwiceIsAnError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newTestRunner(t, nil)
		ctx, cancel := context.WithCancel(t.Context())
		go func() { _ = r.Run(ctx) }()
		time.Sleep(time.Second)
		require.Error(t, r.Run(ctx))
		cancel()
	})
}

func TestRun_OverlappingTickIsDropped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var started atomic.Int64
		release := make(chan struct{})
		r := newTestRunner(t, nil)
		require.NoError(t, r.Register(scheduler.Job{
			Name:     "slow",
			Scope:    scheduler.ScopeSystem,
			Schedule: scheduler.MustEveryInterval(time.Minute, 0),
			Run: func(context.Context) error {
				started.Add(1)
				<-release
				return nil
			},
		}))

		ctx, cancel := context.WithCancel(t.Context())
		go func() { _ = r.Run(ctx) }()

		time.Sleep(3*time.Minute + time.Second)
		require.Equal(t, int64(1), started.Load(), "later ticks must be dropped while one is in flight")

		close(release)
		cancel()
	})
}

func TestRun_TimeoutCancelsJobContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gotErr := make(chan error, 1)
		r := newTestRunner(t, nil)
		require.NoError(t, r.Register(scheduler.Job{
			Name:     "slow",
			Scope:    scheduler.ScopeSystem,
			Schedule: scheduler.MustEveryInterval(time.Minute, 0),
			Timeout:  10 * time.Second,
			Run: func(ctx context.Context) error {
				<-ctx.Done()
				gotErr <- ctx.Err()
				return ctx.Err()
			},
		}))

		ctx, cancel := context.WithCancel(t.Context())
		go func() { _ = r.Run(ctx) }()
		time.Sleep(2 * time.Minute)
		require.ErrorIs(t, <-gotErr, context.DeadlineExceeded)
		cancel()
	})
}

func TestShutdown_WaitsForInFlight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		finished := make(chan struct{})
		r := newTestRunner(t, nil)
		require.NoError(t, r.Register(scheduler.Job{
			Name:     "slow",
			Scope:    scheduler.ScopeSystem,
			Schedule: scheduler.MustEveryInterval(time.Minute, 0),
			Run: func(context.Context) error {
				<-release
				close(finished)
				return nil
			},
		}))

		ctx, cancel := context.WithCancel(t.Context())
		go func() { _ = r.Run(ctx) }()
		time.Sleep(time.Minute + time.Second)

		drained := make(chan error, 1)
		go func() { drained <- r.Shutdown(context.Background()) }()

		time.Sleep(time.Second)
		select {
		case <-drained:
			t.Fatal("Shutdown returned before the in-flight run finished")
		default:
		}

		close(release)
		<-finished
		require.NoError(t, <-drained)
		cancel()
	})
}

func TestShutdown_TimesOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		defer close(release)
		r := newTestRunner(t, nil)
		require.NoError(t, r.Register(scheduler.Job{
			Name:     "stuck",
			Scope:    scheduler.ScopeSystem,
			Schedule: scheduler.MustEveryInterval(time.Minute, 0),
			Run:      func(context.Context) error { <-release; return nil },
		}))

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() { _ = r.Run(ctx) }()
		time.Sleep(time.Minute + time.Second)

		sctx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer scancel()
		require.Error(t, r.Shutdown(sctx))
	})
}

func TestRun_JobErrorGoesToReporter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rep := &fakeReporter{}
		boom := errors.New("boom")
		r := newTestRunner(t, func(o *scheduler.Options) { o.Reporter = rep })
		require.NoError(t, r.Register(scheduler.Job{
			Name:     "bad",
			Scope:    scheduler.ScopeSystem,
			Schedule: scheduler.MustEveryInterval(time.Minute, 0),
			Run:      func(context.Context) error { return boom },
		}))

		ctx, cancel := context.WithCancel(t.Context())
		go func() { _ = r.Run(ctx) }()
		time.Sleep(time.Minute + time.Second)
		cancel()

		require.Equal(t, int64(1), rep.calls.Load())
	})
}

type fakeReporter struct {
	calls atomic.Int64
	last  atomic.Value
}

func (f *fakeReporter) Report(_ context.Context, message string, cause error, _ ...any) {
	f.calls.Add(1)
	f.last.Store(message + ": " + cause.Error())
}

type logRecord struct {
	msg   string
	reqID string
	attrs map[string]string
}

type logCapture struct {
	mu   sync.Mutex
	recs []logRecord
}

func (c *logCapture) Enabled(context.Context, slog.Level) bool { return true }

func (c *logCapture) Handle(ctx context.Context, r slog.Record) error {
	rec := logRecord{msg: r.Message, reqID: reqid.FromContext(ctx), attrs: map[string]string{}}
	r.Attrs(func(a slog.Attr) bool {
		rec.attrs[a.Key] = a.Value.String()
		return true
	})
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recs = append(c.recs, rec)
	return nil
}

func (c *logCapture) WithAttrs([]slog.Attr) slog.Handler { return c }

func (c *logCapture) WithGroup(string) slog.Handler { return c }

func (c *logCapture) last(t *testing.T, msg string) logRecord {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.recs) - 1; i >= 0; i-- {
		if c.recs[i].msg == msg {
			return c.recs[i]
		}
	}
	t.Fatalf("no %q log record captured", msg)
	return logRecord{}
}

func capturingLogger() (*slog.Logger, *logCapture) {
	c := &logCapture{}
	return slog.New(c), c
}

type fakeTenants struct {
	ids     []string
	err     error
	seenIDs []string
}

func (f *fakeTenants) Each(ctx context.Context, fn func(context.Context, string) error) error {
	if f.err != nil {
		return f.err
	}
	for _, id := range f.ids {
		f.seenIDs = append(f.seenIDs, id)
		if err := fn(context.WithValue(ctx, tenantKey{}, id), id); err != nil {
			return err
		}
	}
	return nil
}

type tenantKey struct{}

type fakeLocker struct {
	acquire  bool
	err      error
	released atomic.Int64
}

func (f *fakeLocker) TryLock(context.Context, string) (func(), bool, error) {
	if f.err != nil {
		return nil, false, f.err
	}
	if !f.acquire {
		return nil, false, nil
	}
	return func() { f.released.Add(1) }, true, nil
}

func TestRun_TenantScope_InvokesOncePerTenantInOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tn := &fakeTenants{ids: []string{"a", "b", "c"}}
		var got []string
		var mu sync.Mutex

		r := newTestRunner(t, func(o *scheduler.Options) { o.Tenants = tn })
		require.NoError(t, r.Register(scheduler.Job{
			Name:     "sweep",
			Scope:    scheduler.ScopeTenant,
			Schedule: scheduler.MustEveryInterval(time.Minute, 0),
			Run: func(ctx context.Context) error {
				mu.Lock()
				got = append(got, ctx.Value(tenantKey{}).(string))
				mu.Unlock()
				return nil
			},
		}))

		ctx, cancel := context.WithCancel(t.Context())
		go func() { _ = r.Run(ctx) }()
		time.Sleep(time.Minute + time.Second)
		cancel()

		mu.Lock()
		defer mu.Unlock()
		require.Equal(t, []string{"a", "b", "c"}, got, "fan-out must be serial and in enumeration order")
	})
}

func TestRun_TenantScope_OneFailureDoesNotAbortTheRest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tn := &fakeTenants{ids: []string{"a", "bad", "c"}}
		var invoked atomic.Int64
		rep := &fakeReporter{}

		r := newTestRunner(t, func(o *scheduler.Options) {
			o.Tenants = tn
			o.Reporter = rep
		})
		require.NoError(t, r.Register(scheduler.Job{
			Name:     "sweep",
			Scope:    scheduler.ScopeTenant,
			Schedule: scheduler.MustEveryInterval(time.Minute, 0),
			Run: func(ctx context.Context) error {
				invoked.Add(1)
				if ctx.Value(tenantKey{}).(string) == "bad" {
					return errors.New("tenant blew up")
				}
				return nil
			},
		}))

		ctx, cancel := context.WithCancel(t.Context())
		go func() { _ = r.Run(ctx) }()
		time.Sleep(time.Minute + time.Second)
		cancel()

		require.Equal(t, int64(3), invoked.Load(), "every tenant must be attempted")
		require.Equal(t, int64(1), rep.calls.Load(), "the joined failure is reported once for the job")
	})
}

func TestRun_TenantScope_EnumerationErrorFailsTheJob(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rep := &fakeReporter{}
		r := newTestRunner(t, func(o *scheduler.Options) {
			o.Tenants = &fakeTenants{err: errors.New("db down")}
			o.Reporter = rep
		})
		require.NoError(t, r.Register(scheduler.Job{
			Name:     "sweep",
			Scope:    scheduler.ScopeTenant,
			Schedule: scheduler.MustEveryInterval(time.Minute, 0),
			Run:      func(context.Context) error { return nil },
		}))

		ctx, cancel := context.WithCancel(t.Context())
		go func() { _ = r.Run(ctx) }()
		time.Sleep(time.Minute + time.Second)
		cancel()

		require.Equal(t, int64(1), rep.calls.Load())
	})
}

func TestRun_TenantScope_TimeoutIsPerTenant(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tn := &fakeTenants{ids: []string{"a", "b", "c"}}
		var invoked atomic.Int64
		var mu sync.Mutex
		var expiries []time.Time

		r := newTestRunner(t, func(o *scheduler.Options) { o.Tenants = tn })
		require.NoError(t, r.Register(scheduler.Job{
			Name:     "sweep",
			Scope:    scheduler.ScopeTenant,
			Schedule: scheduler.MustEveryInterval(time.Hour, 0),
			Timeout:  10 * time.Second,
			Run: func(ctx context.Context) error {
				invoked.Add(1)
				<-ctx.Done()
				mu.Lock()
				expiries = append(expiries, time.Now())
				mu.Unlock()
				return ctx.Err()
			},
		}))

		ctx, cancel := context.WithCancel(t.Context())
		go func() { _ = r.Run(ctx) }()
		time.Sleep(time.Hour + time.Minute)
		cancel()

		require.Equal(t, int64(3), invoked.Load(),
			"a per-tenant deadline must not starve the tail of the enumeration")

		mu.Lock()
		defer mu.Unlock()
		require.Len(t, expiries, 3)
		require.Equal(t, 10*time.Second, expiries[1].Sub(expiries[0]),
			"each tenant gets its own Timeout window; a whole-tick budget would expire all three at once")
		require.Equal(t, 10*time.Second, expiries[2].Sub(expiries[1]),
			"each tenant gets its own Timeout window; a whole-tick budget would expire all three at once")
	})
}

func TestRun_TenantScope_PanicIsContainedPerTenant(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tn := &fakeTenants{ids: []string{"a", "panicky", "c"}}
		var invoked atomic.Int64
		rep := &fakeReporter{}
		log, logs := capturingLogger()

		r := newTestRunner(t, func(o *scheduler.Options) {
			o.Tenants = tn
			o.Reporter = rep
			o.Logger = log
		})
		require.NoError(t, r.Register(scheduler.Job{
			Name:     "sweep",
			Scope:    scheduler.ScopeTenant,
			Schedule: scheduler.MustEveryInterval(time.Minute, 0),
			Run: func(ctx context.Context) error {
				invoked.Add(1)
				if ctx.Value(tenantKey{}).(string) == "panicky" {
					panic("tenant boom")
				}
				return nil
			},
		}))

		ctx, cancel := context.WithCancel(t.Context())
		go func() { _ = r.Run(ctx) }()
		time.Sleep(time.Minute + time.Second)
		cancel()

		require.Equal(t, int64(3), invoked.Load(), "a tenant panic must not abort the fan-out")
		require.Equal(t, int64(1), rep.calls.Load())
		summary := logs.last(t, "scheduler.job")
		require.Equal(t, "panic", summary.attrs["status"],
			"a panic joined into the fan-out error must still classify as StatusPanic")
		require.Equal(t, "1", summary.attrs["tenants_failed"])
	})
}

func TestRun_TenantScope_MissingTenantsIsABootError(t *testing.T) {
	r := newTestRunner(t, nil)
	require.NoError(t, r.Register(scheduler.Job{
		Name:     "sweep",
		Scope:    scheduler.ScopeTenant,
		Schedule: scheduler.MustEveryInterval(time.Minute, 0),
		Run:      func(context.Context) error { return nil },
	}))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	require.Error(t, r.Run(ctx))
}

func TestRun_SingletonSkippedWhenLockNotAcquired(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var invoked atomic.Int64
		lk := &fakeLocker{acquire: false}
		r := newTestRunner(t, func(o *scheduler.Options) { o.Locker = lk })
		require.NoError(t, r.Register(scheduler.Job{
			Name:      "single",
			Scope:     scheduler.ScopeSystem,
			Singleton: true,
			Schedule:  scheduler.MustEveryInterval(time.Minute, 0),
			Run:       func(context.Context) error { invoked.Add(1); return nil },
		}))

		ctx, cancel := context.WithCancel(t.Context())
		go func() { _ = r.Run(ctx) }()
		time.Sleep(2*time.Minute + time.Second)
		cancel()

		require.Equal(t, int64(0), invoked.Load())
	})
}

func TestRun_SingletonReleasesLock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lk := &fakeLocker{acquire: true}
		r := newTestRunner(t, func(o *scheduler.Options) { o.Locker = lk })
		require.NoError(t, r.Register(scheduler.Job{
			Name:      "single",
			Scope:     scheduler.ScopeSystem,
			Singleton: true,
			Schedule:  scheduler.MustEveryInterval(time.Minute, 0),
			Run:       func(context.Context) error { return nil },
		}))

		ctx, cancel := context.WithCancel(t.Context())
		go func() { _ = r.Run(ctx) }()
		time.Sleep(2*time.Minute + time.Second)
		cancel()

		require.Equal(t, int64(2), lk.released.Load(), "every acquired lock must be released")
	})
}

func TestRun_NonSingletonIgnoresLocker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var invoked atomic.Int64
		lk := &fakeLocker{acquire: false}
		r := newTestRunner(t, func(o *scheduler.Options) { o.Locker = lk })
		require.NoError(t, r.Register(scheduler.Job{
			Name:     "shared",
			Scope:    scheduler.ScopeSystem,
			Schedule: scheduler.MustEveryInterval(time.Minute, 0),
			Run:      func(context.Context) error { invoked.Add(1); return nil },
		}))

		ctx, cancel := context.WithCancel(t.Context())
		go func() { _ = r.Run(ctx) }()
		time.Sleep(time.Minute + time.Second)
		cancel()

		require.Equal(t, int64(1), invoked.Load())
	})
}

func TestRun_PanicIsContainedAndReported(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rep := &fakeReporter{}
		var ticks atomic.Int64
		log, logs := capturingLogger()
		r := newTestRunner(t, func(o *scheduler.Options) {
			o.Reporter = rep
			o.Logger = log
		})
		require.NoError(t, r.Register(scheduler.Job{
			Name:     "panicky",
			Scope:    scheduler.ScopeSystem,
			Schedule: scheduler.MustEveryInterval(time.Minute, 0),
			Run:      func(context.Context) error { ticks.Add(1); panic("boom") },
		}))

		ctx, cancel := context.WithCancel(t.Context())
		go func() { _ = r.Run(ctx) }()
		time.Sleep(2*time.Minute + time.Second)
		cancel()

		require.Equal(t, int64(2), ticks.Load(), "the loop must survive a panic")
		require.Equal(t, int64(2), rep.calls.Load())
		require.Equal(t, "panic", logs.last(t, "scheduler.job").attrs["status"])
	})
}

func TestRunOnce(t *testing.T) {
	t.Run("unknown job", func(t *testing.T) {
		r := newTestRunner(t, nil)
		err := r.RunOnce(t.Context(), "nope")
		require.True(t, scheduler.IsUnknownJobError(err), "got %v", err)
	})

	t.Run("invokes the job", func(t *testing.T) {
		var invoked atomic.Int64
		r := newTestRunner(t, nil)
		require.NoError(t, r.Register(scheduler.Job{
			Name:     "manual",
			Scope:    scheduler.ScopeSystem,
			Schedule: scheduler.MustEveryInterval(time.Hour, 0),
			Run:      func(context.Context) error { invoked.Add(1); return nil },
		}))
		require.NoError(t, r.RunOnce(t.Context(), "manual"))
		require.Equal(t, int64(1), invoked.Load())
	})

	t.Run("not leader", func(t *testing.T) {
		r := newTestRunner(t, func(o *scheduler.Options) { o.Locker = &fakeLocker{acquire: false} })
		require.NoError(t, r.Register(scheduler.Job{
			Name:      "manual",
			Scope:     scheduler.ScopeSystem,
			Singleton: true,
			Schedule:  scheduler.MustEveryInterval(time.Hour, 0),
			Run:       func(context.Context) error { return nil },
		}))
		err := r.RunOnce(t.Context(), "manual")
		require.True(t, scheduler.IsNotLeaderError(err), "got %v", err)
	})

	t.Run("draining", func(t *testing.T) {
		r := newTestRunner(t, nil)
		require.NoError(t, r.Register(scheduler.Job{
			Name:     "manual",
			Scope:    scheduler.ScopeSystem,
			Schedule: scheduler.MustEveryInterval(time.Hour, 0),
			Run:      func(context.Context) error { return nil },
		}))
		require.NoError(t, r.Shutdown(t.Context()))
		err := r.RunOnce(t.Context(), "manual")
		require.True(t, scheduler.IsDrainingError(err), "got %v", err)
	})
}

func TestRun_TenantScope_RequestIDIsPerTenant(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tn := &fakeTenants{ids: []string{"a", "b"}}
		var mu sync.Mutex
		ids := map[string]string{}

		r := newTestRunner(t, func(o *scheduler.Options) { o.Tenants = tn })
		require.NoError(t, r.Register(scheduler.Job{
			Name:     "sweep",
			Scope:    scheduler.ScopeTenant,
			Schedule: scheduler.MustEveryInterval(time.Minute, 0),
			Run: func(ctx context.Context) error {
				mu.Lock()
				ids[ctx.Value(tenantKey{}).(string)] = reqid.FromContext(ctx)
				mu.Unlock()
				return nil
			},
		}))

		ctx, cancel := context.WithCancel(t.Context())
		go func() { _ = r.Run(ctx) }()
		time.Sleep(time.Minute + time.Second)
		cancel()

		mu.Lock()
		defer mu.Unlock()
		require.Len(t, ids, 2)
		require.NotEmpty(t, ids["a"])
		require.NotEmpty(t, ids["b"])
		require.NotEqual(t, ids["a"], ids["b"],
			"each tenant's sweep must be independently traceable")
	})
}

func TestRun_SystemScope_RequestIDIsMinted(t *testing.T) {
	var got atomic.Value
	r := newTestRunner(t, nil)
	require.NoError(t, r.Register(scheduler.Job{
		Name:     "manual",
		Scope:    scheduler.ScopeSystem,
		Schedule: scheduler.MustEveryInterval(time.Hour, 0),
		Run: func(ctx context.Context) error {
			got.Store(reqid.FromContext(ctx))
			return nil
		},
	}))
	require.NoError(t, r.RunOnce(t.Context(), "manual"))
	require.NotEmpty(t, got.Load())
}

func TestRun_SystemScope_SummaryShareTheInvocationRequestID(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var inner atomic.Value
		log, logs := capturingLogger()
		r := newTestRunner(t, func(o *scheduler.Options) { o.Logger = log })
		require.NoError(t, r.Register(scheduler.Job{
			Name:     "tick",
			Scope:    scheduler.ScopeSystem,
			Schedule: scheduler.MustEveryInterval(time.Minute, 0),
			Run: func(ctx context.Context) error {
				inner.Store(reqid.FromContext(ctx))
				return nil
			},
		}))

		ctx, cancel := context.WithCancel(t.Context())
		go func() { _ = r.Run(ctx) }()
		time.Sleep(time.Minute + time.Second)
		cancel()

		id, _ := inner.Load().(string)
		require.NotEmpty(t, id)
		require.Equal(t, id, logs.last(t, "scheduler.job").reqID,
			"the summary line must carry the same request_id as the invocation it summarises")
	})
}

func TestRun_TenantScope_SummaryIDDiffersFromEveryTenantID(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var tenantIDs []string
		log, logs := capturingLogger()
		r := newTestRunner(t, func(o *scheduler.Options) {
			o.Tenants = &fakeTenants{ids: []string{"a", "b"}}
			o.Logger = log
		})
		require.NoError(t, r.Register(scheduler.Job{
			Name:     "sweep",
			Scope:    scheduler.ScopeTenant,
			Schedule: scheduler.MustEveryInterval(time.Minute, 0),
			Run: func(ctx context.Context) error {
				mu.Lock()
				tenantIDs = append(tenantIDs, reqid.FromContext(ctx))
				mu.Unlock()
				return nil
			},
		}))

		ctx, cancel := context.WithCancel(t.Context())
		go func() { _ = r.Run(ctx) }()
		time.Sleep(time.Minute + time.Second)
		cancel()

		tickID := logs.last(t, "scheduler.job").reqID
		require.NotEmpty(t, tickID, "the tick summary must carry its own request_id")

		mu.Lock()
		defer mu.Unlock()
		require.Len(t, tenantIDs, 2)
		for _, id := range tenantIDs {
			require.NotEmpty(t, id)
			require.NotEqual(t, tickID, id, "the tick ID must be distinct from every per-tenant ID")
		}
	})
}

func TestRunOnce_PanicIsTyped(t *testing.T) {
	r := newTestRunner(t, nil)
	require.NoError(t, r.Register(scheduler.Job{
		Name:     "panicky",
		Scope:    scheduler.ScopeSystem,
		Schedule: scheduler.MustEveryInterval(time.Hour, 0),
		Run:      func(context.Context) error { panic("boom") },
	}))
	err := r.RunOnce(t.Context(), "panicky")
	require.True(t, scheduler.IsPanicError(err), "got %v", err)
}
