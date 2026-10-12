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
