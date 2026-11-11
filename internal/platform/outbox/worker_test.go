package outbox_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/outbox"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
)

// NOTE: 0.9 * sum of Backoff's base durations over attempts 2..8 (30s+5m+30m+2h+5h+10h+10h).
const minTotalBackoff = 89397 * time.Second

func workerScope() tenant.Context {
	return tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New(), UserID: uuid.New()}
}

type singleTenant struct{ tc tenant.Context }

func (s singleTenant) Each(ctx context.Context, fn func(context.Context, string) error) error {
	return fn(tenant.Into(ctx, s.tc), s.tc.OrgID.String())
}

type recordingDeliverer struct {
	mu    sync.Mutex
	calls map[uuid.UUID]int
	first time.Time
	last  time.Time
	err   error
}

func newRecordingDeliverer(err error) *recordingDeliverer {
	return &recordingDeliverer{calls: map[uuid.UUID]int{}, err: err}
}

func (d *recordingDeliverer) Deliver(_ context.Context, e outbox.Entry) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls[e.ID]++
	now := time.Now()
	if d.first.IsZero() {
		d.first = now
	}
	d.last = now
	return d.err
}

func (d *recordingDeliverer) count(id uuid.UUID) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls[id]
}

func (d *recordingDeliverer) span() time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.last.Sub(d.first)
}

type blockingDeliverer struct {
	entered chan struct{}
	release chan struct{}
}

func newBlockingDeliverer() *blockingDeliverer {
	return &blockingDeliverer{entered: make(chan struct{}, 1), release: make(chan struct{})}
}

func (d *blockingDeliverer) Deliver(_ context.Context, _ outbox.Entry) error {
	select {
	case d.entered <- struct{}{}:
	default:
	}
	<-d.release
	return errors.New("transport closed mid-flight")
}

func runWorker(t *testing.T, w *outbox.Worker) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	return cancel, done
}

func TestWorkerRetriesWithBackoffThenSettlesTerminally(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tc := workerScope()
		store := fakes.NewOutbox()
		e := entry(tc, uuid.New())
		require.NoError(t, store.Enqueue(tenant.Into(t.Context(), tc), e))

		d := newRecordingDeliverer(errors.New("endpoint refused"))
		w := outbox.NewWorker(store, d, singleTenant{tc: tc}, slog.New(slog.DiscardHandler),
			outbox.WorkerOpts{Tick: time.Minute, Batch: 10, Concurrency: 2})

		cancel, done := runWorker(t, w)
		time.Sleep(34 * time.Hour)
		cancel()
		require.NoError(t, <-done)

		assert.Equal(t, outbox.MaxAttempts, d.count(e.ID),
			"a permanently failing entry did not stop at the attempt cap")
		assert.GreaterOrEqual(t, d.span(), minTotalBackoff,
			"retries did not back off; the worker is spinning on the failing entry")

		got, ok := store.Entry(e.ID)
		require.True(t, ok)
		assert.Equal(t, outbox.StatusFailed, got.Status, "the entry never reached a terminal state")
		assert.Equal(t, "endpoint refused", got.LastError)
	})
}

// SECURITY/correctness: two replicas delivering one entry twice is the failure the claim exists to prevent.
func TestTwoWorkersNeverDeliverAnEntryTwice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tc := workerScope()
		store := fakes.NewOutbox()
		ctx := tenant.Into(t.Context(), tc)

		ids := make([]uuid.UUID, 0, 20)
		for range 20 {
			e := entry(tc, uuid.New())
			require.NoError(t, store.Enqueue(ctx, e))
			ids = append(ids, e.ID)
		}

		d := newRecordingDeliverer(nil)
		opts := outbox.WorkerOpts{Tick: time.Second, Batch: 5, Concurrency: 4}
		tenants := singleTenant{tc: tc}
		log := slog.New(slog.DiscardHandler)

		cancelA, doneA := runWorker(t, outbox.NewWorker(store, d, tenants, log, opts))
		cancelB, doneB := runWorker(t, outbox.NewWorker(store, d, tenants, log, opts))

		time.Sleep(3 * outbox.ClaimLease)
		cancelA()
		cancelB()
		require.NoError(t, <-doneA)
		require.NoError(t, <-doneB)

		for _, id := range ids {
			assert.Equal(t, 1, d.count(id), "entry %s was delivered more than once", id)
			got, ok := store.Entry(id)
			require.True(t, ok)
			assert.Equal(t, outbox.StatusDelivered, got.Status, "entry %s", id)
		}
	})
}

func TestWorkerSettlesInFlightWorkBeforeShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tc := workerScope()
		store := fakes.NewOutbox()
		e := entry(tc, uuid.New())
		require.NoError(t, store.Enqueue(tenant.Into(t.Context(), tc), e))

		d := newBlockingDeliverer()
		w := outbox.NewWorker(store, d, singleTenant{tc: tc}, slog.New(slog.DiscardHandler),
			outbox.WorkerOpts{Tick: time.Second, Batch: 10, Concurrency: 1})

		cancel, done := runWorker(t, w)
		<-d.entered
		cancel()

		select {
		case <-done:
			t.Fatal("Run returned while a claimed entry was still in flight")
		case <-time.After(time.Minute):
		}

		close(d.release)
		require.NoError(t, <-done)

		got, ok := store.Entry(e.ID)
		require.True(t, ok)
		assert.Equal(t, 1, got.Attempt)
		assert.Equal(t, outbox.StatusPending, got.Status)
		assert.Equal(t, "transport closed mid-flight", got.LastError,
			"the in-flight delivery was dropped at shutdown without recording its outcome")
	})
}

type firstBlocksDeliverer struct {
	mu      sync.Mutex
	calls   []uuid.UUID
	entered chan struct{}
	release chan struct{}
}

func (d *firstBlocksDeliverer) Deliver(_ context.Context, e outbox.Entry) error {
	d.mu.Lock()
	d.calls = append(d.calls, e.ID)
	first := len(d.calls) == 1
	d.mu.Unlock()
	if first {
		close(d.entered)
		<-d.release
	}
	return errors.New("transport closed mid-flight")
}

func (d *firstBlocksDeliverer) delivered() []uuid.UUID {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.calls)
}

func TestWorkerLeavesUnstartedClaimsToTheLeaseOnCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tc := workerScope()
		store := fakes.NewOutbox()
		ids := make([]uuid.UUID, 0, 3)
		for range 3 {
			e := entry(tc, uuid.New())
			require.NoError(t, store.Enqueue(tenant.Into(t.Context(), tc), e))
			ids = append(ids, e.ID)
		}

		d := &firstBlocksDeliverer{entered: make(chan struct{}), release: make(chan struct{})}
		w := outbox.NewWorker(store, d, singleTenant{tc: tc}, slog.New(slog.DiscardHandler),
			outbox.WorkerOpts{Tick: time.Second, Batch: 10, Concurrency: 1})

		start := time.Now()
		cancel, done := runWorker(t, w)
		<-d.entered
		cancel()
		close(d.release)
		require.NoError(t, <-done)

		delivered := d.delivered()
		require.Len(t, delivered, 1, "an entry not yet started must not be delivered after cancel")
		for _, id := range ids {
			got, ok := store.Entry(id)
			require.True(t, ok)
			assert.Equal(t, 1, got.Attempt, "entry %s", id)
			assert.Equal(t, outbox.StatusPending, got.Status, "entry %s", id)
			if id == delivered[0] {
				assert.Equal(t, "transport closed mid-flight", got.LastError, "the in-flight entry still settles")
				continue
			}
			assert.Empty(t, got.LastError, "entry %s: no Fail may be recorded for an unstarted claim", id)
			assert.False(t, got.NextAttemptAt.After(start.Add(time.Second+outbox.ClaimLease)),
				"entry %s: the claim lease, not a backoff, must release it", id)
		}
	})
}

func TestWorkerStopsOnContextCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tc := workerScope()
		w := outbox.NewWorker(fakes.NewOutbox(), newRecordingDeliverer(nil), singleTenant{tc: tc},
			slog.New(slog.DiscardHandler), outbox.WorkerOpts{Tick: time.Second})

		cancel, done := runWorker(t, w)
		cancel()

		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(time.Minute):
			t.Fatal("Run did not return after cancel")
		}
	})
}

func TestWorkerWithoutDelivererIsIdle(t *testing.T) {
	tc := workerScope()
	store := fakes.NewOutbox()
	e := entry(tc, uuid.New())
	require.NoError(t, store.Enqueue(tenant.Into(t.Context(), tc), e))

	w := outbox.NewWorker(store, nil, singleTenant{tc: tc}, slog.New(slog.DiscardHandler),
		outbox.WorkerOpts{Tick: time.Millisecond})
	require.NoError(t, w.Run(t.Context()))

	got, ok := store.Entry(e.ID)
	require.True(t, ok)
	assert.Equal(t, 0, got.Attempt, "an unconfigured worker claimed an entry it cannot deliver")
}

func TestWorkerNameIsStable(t *testing.T) {
	w := outbox.NewWorker(fakes.NewOutbox(), newRecordingDeliverer(nil), singleTenant{}, nil, outbox.WorkerOpts{})
	assert.Equal(t, outbox.WorkerName, w.Name())
	assert.Equal(t, "outbox.dispatch", w.Name())
}

type countingTenants struct {
	singleTenant
	mu    sync.Mutex
	calls int
}

func (c *countingTenants) Each(ctx context.Context, fn func(context.Context, string) error) error {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return c.singleTenant.Each(ctx, fn)
}

func (c *countingTenants) sweeps() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

type failFirstDeliverer struct {
	*recordingDeliverer
}

func (d *failFirstDeliverer) Deliver(ctx context.Context, e outbox.Entry) error {
	_ = d.recordingDeliverer.Deliver(ctx, e)
	if d.count(e.ID) == 1 {
		return errors.New("endpoint refused")
	}
	return nil
}

func quietOpts() outbox.WorkerOpts {
	return outbox.WorkerOpts{Tick: 5 * time.Second, IdlePoll: 10 * time.Minute, Batch: 10, Concurrency: 2}
}

func TestWorkerWakeDeliversWithoutWaitingForTheIdlePoll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tc := workerScope()
		raw := fakes.NewOutbox()
		d := newRecordingDeliverer(nil)
		opts := quietOpts()
		opts.Waker = outbox.NewWaker()
		w := outbox.NewWorker(raw, d, singleTenant{tc: tc}, slog.New(slog.DiscardHandler), opts)
		store := outbox.WakeOnEnqueue(raw, opts.Waker)

		cancel, done := runWorker(t, w)
		synctest.Wait()

		e := entry(tc, uuid.New())
		require.NoError(t, fakes.UnitOfWork(tenant.Into(t.Context(), tc), func(ctx context.Context) error {
			require.NoError(t, store.Enqueue(ctx, e))
			time.Sleep(time.Second)
			assert.Equal(t, 0, d.count(e.ID), "the worker woke before the enqueue committed")
			return nil
		}))
		time.Sleep(time.Second)

		assert.Equal(t, 1, d.count(e.ID), "a committed enqueue did not wake the worker before the idle poll")
		cancel()
		require.NoError(t, <-done)
	})
}

func TestWorkerSendsNoQueryWhileIdle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tenants := &countingTenants{singleTenant: singleTenant{tc: workerScope()}}
		w := outbox.NewWorker(fakes.NewOutbox(), newRecordingDeliverer(nil), tenants,
			slog.New(slog.DiscardHandler), quietOpts())

		cancel, done := runWorker(t, w)
		time.Sleep(10*time.Minute - time.Second)
		assert.Equal(t, 1, tenants.sweeps(), "only the startup sweep may run before the idle poll")

		time.Sleep(2 * time.Second)
		assert.Equal(t, 2, tenants.sweeps(), "the idle poll did not sweep")
		cancel()
		require.NoError(t, <-done)
	})
}

func TestWorkerRetriesAFailedEntryBeforeTheIdlePoll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tc := workerScope()
		store := fakes.NewOutbox()
		e := entry(tc, uuid.New())
		require.NoError(t, store.Enqueue(tenant.Into(t.Context(), tc), e))
		d := &failFirstDeliverer{recordingDeliverer: newRecordingDeliverer(nil)}
		w := outbox.NewWorker(store, d, singleTenant{tc: tc}, slog.New(slog.DiscardHandler), quietOpts())

		cancel, done := runWorker(t, w)
		time.Sleep(backoffCeiling(2) + time.Second)

		assert.Equal(t, 2, d.count(e.ID), "the retry waited for the idle poll instead of its backoff")
		got, ok := store.Entry(e.ID)
		require.True(t, ok)
		assert.Equal(t, outbox.StatusDelivered, got.Status)
		cancel()
		require.NoError(t, <-done)
	})
}

func TestWorkerResweepsAfterAFullBatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tc := workerScope()
		store := fakes.NewOutbox()
		ids := make([]uuid.UUID, 3)
		for i := range ids {
			e := entry(tc, uuid.New())
			require.NoError(t, store.Enqueue(tenant.Into(t.Context(), tc), e))
			ids[i] = e.ID
		}
		d := newRecordingDeliverer(nil)
		opts := quietOpts()
		opts.Batch = 2
		w := outbox.NewWorker(store, d, singleTenant{tc: tc}, slog.New(slog.DiscardHandler), opts)

		cancel, done := runWorker(t, w)
		time.Sleep(opts.Tick + time.Second)

		for _, id := range ids {
			assert.Equal(t, 1, d.count(id), "entry %s waited for the idle poll behind a full batch", id)
		}
		cancel()
		require.NoError(t, <-done)
	})
}

func backoffCeiling(attempt int) time.Duration {
	base := outbox.BackoffBase(attempt)
	return base + base/10
}

type flakyStore struct {
	outbox.Store
	mu                                  sync.Mutex
	claimFails, succeedFails, failFails int
}

func (s *flakyStore) take(n *int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if *n == 0 {
		return false
	}
	*n--
	return true
}

func (s *flakyStore) ClaimDue(ctx context.Context, now time.Time, limit int) ([]outbox.Entry, error) {
	if s.take(&s.claimFails) {
		return nil, errors.New("claim refused")
	}
	return s.Store.ClaimDue(ctx, now, limit)
}

func (s *flakyStore) Succeed(ctx context.Context, e outbox.Entry, at time.Time) error {
	if s.take(&s.succeedFails) {
		return errors.New("settle refused")
	}
	return s.Store.Succeed(ctx, e, at)
}

func (s *flakyStore) Fail(ctx context.Context, e outbox.Entry, retryAt time.Time, cause string) error {
	if s.take(&s.failFails) {
		return errors.New("settle refused")
	}
	return s.Store.Fail(ctx, e, retryAt, cause)
}

type flakyTenants struct {
	singleTenant
	mu    sync.Mutex
	fails int
}

func (f *flakyTenants) Each(ctx context.Context, fn func(context.Context, string) error) error {
	f.mu.Lock()
	fail := f.fails > 0
	if fail {
		f.fails--
	}
	f.mu.Unlock()
	if fail {
		return errors.New("enumerate refused")
	}
	return f.singleTenant.Each(ctx, fn)
}

func TestWorkerRetriesAtTheTickAfterAnError(t *testing.T) {
	cases := []struct {
		name    string
		store   func(outbox.Store) outbox.Store
		tenants func(tenant.Context) outbox.Tenants
	}{
		{
			name:    "claim error",
			store:   func(s outbox.Store) outbox.Store { return &flakyStore{Store: s, claimFails: 1} },
			tenants: func(tc tenant.Context) outbox.Tenants { return singleTenant{tc: tc} },
		},
		{
			name:  "tenant enumeration error",
			store: func(s outbox.Store) outbox.Store { return s },
			tenants: func(tc tenant.Context) outbox.Tenants {
				return &flakyTenants{singleTenant: singleTenant{tc: tc}, fails: 1}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				tc := workerScope()
				base := fakes.NewOutbox()
				e := entry(tc, uuid.New())
				require.NoError(t, base.Enqueue(tenant.Into(t.Context(), tc), e))

				d := newRecordingDeliverer(nil)
				w := outbox.NewWorker(c.store(base), d, c.tenants(tc), slog.New(slog.DiscardHandler),
					outbox.WorkerOpts{Tick: 5 * time.Second, IdlePoll: time.Hour})

				cancel, done := runWorker(t, w)
				time.Sleep(6 * time.Second)
				cancel()
				require.NoError(t, <-done)

				assert.Equal(t, 1, d.count(e.ID), "an error left the sweep for the idle poll instead of the tick")
			})
		})
	}
}

func TestWorkerRetriesAtTheLeaseAfterASettleError(t *testing.T) {
	cases := []struct {
		name  string
		store func(outbox.Store) *flakyStore
		err   error
	}{
		{name: "succeed error", store: func(s outbox.Store) *flakyStore { return &flakyStore{Store: s, succeedFails: 1} }},
		{name: "fail error", store: func(s outbox.Store) *flakyStore { return &flakyStore{Store: s, failFails: 1} }, err: errors.New("endpoint refused")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				tc := workerScope()
				base := fakes.NewOutbox()
				e := entry(tc, uuid.New())
				require.NoError(t, base.Enqueue(tenant.Into(t.Context(), tc), e))

				d := newRecordingDeliverer(c.err)
				w := outbox.NewWorker(c.store(base), d, singleTenant{tc: tc}, slog.New(slog.DiscardHandler),
					outbox.WorkerOpts{Tick: 5 * time.Second, IdlePoll: time.Hour})

				cancel, done := runWorker(t, w)
				time.Sleep(outbox.ClaimLease + 10*time.Second)
				cancel()
				require.NoError(t, <-done)

				assert.Equal(t, 2, d.count(e.ID), "an unsettled claim waited for the idle poll instead of its lease")
			})
		})
	}
}
