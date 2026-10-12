package outbox

import (
	"context"
	"log/slog"
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
	// Tick is how often the worker sweeps for due entries.
	Tick time.Duration
	// Batch caps how many entries one tenant yields per sweep.
	Batch int
	// Concurrency caps deliveries in flight within one tenant's batch.
	Concurrency int
	// SettleGrace bounds the Succeed or Fail that records a delivery outcome during shutdown.
	SettleGrace time.Duration
}

// Worker claims due entries every tick and hands each to a Deliverer.
type Worker struct {
	store   Store
	deliver Deliverer
	tenants Tenants
	log     *slog.Logger
	opts    WorkerOpts
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
	if opts.Concurrency <= 0 {
		opts.Concurrency = defaultConcurrency
	}
	if opts.SettleGrace <= 0 {
		opts.SettleGrace = defaultSettleGrace
	}
	return &Worker{
		store:   s,
		deliver: d,
		tenants: tenants,
		log:     log.With(slog.String("component", WorkerName)),
		opts:    opts,
	}
}

// Name implements worker.Worker.
func (w *Worker) Name() string { return WorkerName }

// Run implements worker.Worker, sweeping due entries every tick until ctx is done.
func (w *Worker) Run(ctx context.Context) error {
	if w.store == nil || w.deliver == nil || w.tenants == nil {
		w.log.Info("outbox: dispatch idle, no deliverer configured")
		return nil
	}
	t := time.NewTicker(w.opts.Tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			w.sweep(ctx)
		}
	}
}

func (w *Worker) sweep(ctx context.Context) {
	err := w.tenants.Each(ctx, func(tctx context.Context, tenantID string) error {
		w.drain(tctx, tenantID)
		return ctx.Err()
	})
	if err == nil || ctx.Err() != nil {
		return
	}
	w.log.ErrorContext(ctx, "outbox: enumerate tenants", slog.String("err", err.Error()))
}

func (w *Worker) drain(ctx context.Context, tenantID string) {
	claimed, err := w.store.ClaimDue(ctx, time.Now().UTC(), w.opts.Batch)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		w.log.ErrorContext(ctx, "outbox: claim due",
			slog.String("tenant", tenantID), slog.String("err", err.Error()))
		return
	}
	if len(claimed) == 0 {
		return
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
				w.deliverOne(ctx, e)
			}
			return nil
		})
	}
	_ = g.Wait()
}

func (w *Worker) deliverOne(ctx context.Context, e Entry) {
	err := w.deliver.Deliver(ctx, e)

	// NOTE: settling is detached from ctx so a cancel mid-delivery still records the outcome.
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.opts.SettleGrace)
	defer cancel()

	if err == nil {
		if sErr := w.store.Succeed(settleCtx, e, time.Now().UTC()); sErr != nil {
			w.log.ErrorContext(settleCtx, "outbox: settle delivered",
				slog.String("entry", e.ID.String()), slog.String("err", sErr.Error()))
		}
		return
	}

	retryAt := time.Now().UTC().Add(Backoff(e.Attempt + 1))
	if fErr := w.store.Fail(settleCtx, e, retryAt, err.Error()); fErr != nil {
		w.log.ErrorContext(settleCtx, "outbox: settle failed delivery",
			slog.String("entry", e.ID.String()), slog.String("err", fErr.Error()))
	}
}
