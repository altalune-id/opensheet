package outbox

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"altalune.id/opensheet/worker"
)

var _ worker.Worker = (*Worker)(nil)

// WorkerName is the name the dispatch worker registers under on a Supervisor.
const WorkerName = "outbox.dispatch"

const (
	defaultTick        = 5 * time.Second
	defaultBatch       = 100
	defaultConcurrency = 8
	defaultSettleGrace = 5 * time.Second
)

// Deliverer hands one claimed entry to the outbound transport; the webhook module ships the implementation.
type Deliverer interface {
	Deliver(ctx context.Context, e Entry) error
}

// Tenants enumerates the org scopes one sweep covers, binding each to the ctx it passes to fn.
type Tenants interface {
	Each(ctx context.Context, fn func(ctx context.Context, tenantID string) error) error
}

// WorkerOpts tunes one dispatch loop, with every non-positive field taking the package default.
type WorkerOpts struct {
	// Tick is the shortest wait between two timed sweeps.
	Tick time.Duration
	// IdlePoll is the longest wait between two sweeps when nothing wakes the worker; it defaults to Tick.
	IdlePoll time.Duration
	// Batch caps how many entries one tenant yields per sweep.
	Batch int
	// Concurrency caps deliveries in flight within one tenant's batch.
	Concurrency int
	// SettleGrace bounds the Succeed or Fail that records a delivery outcome during shutdown.
	SettleGrace time.Duration
	// Waker is the signal WakeOnEnqueue sends after a commit; nil gives the worker its own.
	Waker *Waker
}

// Worker claims due entries when woken, when a retry it scheduled falls due, or every idle poll, and hands each to a Deliverer.
type Worker struct {
	store   Store
	deliver Deliverer
	tenants Tenants
	log     *slog.Logger
	opts    WorkerOpts

	mu      sync.Mutex
	retries []time.Time
}

// NewWorker returns the dispatch worker, whose Run is a no-op when Store, Deliverer or Tenants is nil.
func NewWorker(s Store, d Deliverer, tenants Tenants, log *slog.Logger, opts WorkerOpts) *Worker {
	if log == nil {
		log = slog.Default()
	}
	if opts.Tick <= 0 {
		opts.Tick = defaultTick
	}
	if opts.Batch <= 0 {
		opts.Batch = defaultBatch
	}
	opts.Batch = min(opts.Batch, MaxClaimLimit)
	if opts.Concurrency <= 0 {
		opts.Concurrency = defaultConcurrency
	}
	if opts.SettleGrace <= 0 {
		opts.SettleGrace = defaultSettleGrace
	}
	if opts.IdlePoll <= 0 {
		opts.IdlePoll = opts.Tick
	}
	if opts.Waker == nil {
		opts.Waker = NewWaker()
	}
	return &Worker{
		store:   s,
		deliver: d,
		tenants: tenants,
		log:     log.With(slog.String("component", WorkerName)),
		opts:    opts,
	}
}

// Wake asks the worker to sweep now; call it after the transaction that enqueued an entry commits.
func (w *Worker) Wake() { w.opts.Waker.Wake() }

// Name implements worker.Worker.
func (w *Worker) Name() string { return WorkerName }

// Run implements worker.Worker, sweeping at start and then on Wake, on a due retry, or every idle poll until ctx is done.
func (w *Worker) Run(ctx context.Context) error {
	if w.store == nil || w.deliver == nil || w.tenants == nil {
		w.log.Info("outbox: dispatch idle, no deliverer configured")
		return nil
	}
	t := time.NewTimer(0)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-w.opts.Waker.ch:
		case <-t.C:
		}
		start := time.Now()
		full := w.sweep(ctx)
		t.Reset(w.nextWait(start, full))
	}
}

func (w *Worker) nextWait(sweptAt time.Time, full bool) time.Duration {
	if full {
		return w.opts.Tick
	}
	wait := w.opts.IdlePoll
	w.mu.Lock()
	w.retries = slices.DeleteFunc(w.retries, func(at time.Time) bool { return !at.After(sweptAt) })
	if len(w.retries) > 0 {
		wait = min(wait, time.Until(slices.MinFunc(w.retries, time.Time.Compare)))
	}
	w.mu.Unlock()
	return max(wait, w.opts.Tick)
}

func (w *Worker) sweep(ctx context.Context) bool {
	full := false
	err := w.tenants.Each(ctx, func(tctx context.Context, tenantID string) error {
		full = w.drain(tctx, tenantID) || full
		return ctx.Err()
	})
	if err == nil || ctx.Err() != nil {
		return full
	}
	w.log.ErrorContext(ctx, "outbox: enumerate tenants", slog.String("err", err.Error()))
	w.retryAt(time.Now().Add(w.opts.Tick))
	return full
}

func (w *Worker) retryAt(at time.Time) {
	w.mu.Lock()
	w.retries = append(w.retries, at)
	w.mu.Unlock()
}

func (w *Worker) drain(ctx context.Context, tenantID string) bool {
	now := time.Now().UTC()
	claimed, err := w.store.ClaimDue(ctx, now, w.opts.Batch)
	if err != nil {
		if ctx.Err() != nil {
			return false
		}
		w.log.ErrorContext(ctx, "outbox: claim due",
			slog.String("tenant", tenantID), slog.String("err", err.Error()))
		w.retryAt(time.Now().Add(w.opts.Tick))
		return false
	}
	leaseEnd := now.Add(ClaimLease)
	if len(claimed) == 0 {
		return false
	}
	g := new(errgroup.Group)
	g.SetLimit(w.opts.Concurrency)
	for _, e := range claimed {
		if ctx.Err() != nil {
			break
		}
		g.Go(func() error {
			// NOTE: a claim not yet started at cancel skips Deliver and Fail; its lease releases it.
			if ctx.Err() == nil {
				w.deliverOne(ctx, e, leaseEnd)
			}
			return nil
		})
	}
	_ = g.Wait()
	return len(claimed) >= w.opts.Batch
}

func (w *Worker) deliverOne(ctx context.Context, e Entry, leaseEnd time.Time) {
	err := w.deliver.Deliver(ctx, e)

	// NOTE: settling is detached from ctx so a cancel mid-delivery still records the outcome.
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.opts.SettleGrace)
	defer cancel()

	if err == nil {
		if sErr := w.store.Succeed(settleCtx, e, time.Now().UTC()); sErr != nil {
			w.log.ErrorContext(settleCtx, "outbox: settle delivered",
				slog.String("entry", e.ID.String()), slog.String("err", sErr.Error()))
			w.retryAt(leaseEnd)
		}
		return
	}

	retryAt := time.Now().UTC().Add(Backoff(e.Attempt + 1))
	if fErr := w.store.Fail(settleCtx, e, retryAt, err.Error()); fErr != nil {
		w.log.ErrorContext(settleCtx, "outbox: settle failed delivery",
			slog.String("entry", e.ID.String()), slog.String("err", fErr.Error()))
		w.retryAt(leaseEnd)
		return
	}
	if e.Attempt >= MaxAttempts {
		return
	}
	w.retryAt(retryAt)
}
