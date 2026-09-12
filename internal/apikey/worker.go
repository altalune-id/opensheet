package apikey

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/worker"
)

var _ worker.Worker = (*UsageWorker)(nil)

const (
	usageBuffer     = 1024
	usageFlushEvery = 5 * time.Second
	usageFlushAt    = 256
)

// UsageRecorder accepts a best-effort note that a key was used.
type UsageRecorder interface {
	Record(orgID, projectID, keyID uuid.UUID, at time.Time)
}

type usageKey struct {
	orgID     uuid.UUID
	projectID uuid.UUID
	keyID     uuid.UUID
}

type usageSample struct {
	key usageKey
	at  time.Time
}

// UsageWorker drains LastUsedAt samples off the authentication path and flushes them in batches.
type UsageWorker struct {
	store   Store
	log     *slog.Logger
	clock   func() time.Time
	samples chan usageSample
	dropped atomic.Int64
}

// NewUsageWorker builds the worker; a nil clock falls back to time.Now.
func NewUsageWorker(store Store, log *slog.Logger, clock func() time.Time) *UsageWorker {
	if clock == nil {
		clock = time.Now
	}
	return &UsageWorker{
		store:   store,
		log:     log.With("module", "apikey"),
		clock:   clock,
		samples: make(chan usageSample, usageBuffer),
	}
}

// Name identifies the worker to the Supervisor.
func (w *UsageWorker) Name() string { return "apikey-lastused" }

// Record notes that a key was used.
// SECURITY: it must never block — the authentication path may drop a timestamp, never a request.
func (w *UsageWorker) Record(orgID, projectID, keyID uuid.UUID, at time.Time) {
	if at.IsZero() {
		at = w.clock()
	}
	s := usageSample{
		key: usageKey{orgID: orgID, projectID: projectID, keyID: keyID},
		at:  at.UTC(),
	}
	select {
	case w.samples <- s:
	default:
		w.dropped.Add(1)
	}
}

// Dropped reports how many samples were discarded because the buffer was full.
func (w *UsageWorker) Dropped() int64 { return w.dropped.Load() }

// Run coalesces samples per key and flushes them on the ticker, at the batch threshold, and once more on shutdown.
func (w *UsageWorker) Run(ctx context.Context) error {
	// NOTE: a request context is cancelled the moment the response is written, so the flush must not inherit its cancellation.
	flushCtx := context.WithoutCancel(ctx)
	pending := make(map[usageKey]time.Time, usageFlushAt)
	t := time.NewTicker(usageFlushEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			w.drain(pending)
			w.flush(flushCtx, pending)
			return nil
		case s := <-w.samples:
			w.keep(pending, s)
			if len(pending) >= usageFlushAt {
				w.flush(flushCtx, pending)
			}
		case <-t.C:
			w.flush(flushCtx, pending)
		}
	}
}

func (w *UsageWorker) keep(pending map[usageKey]time.Time, s usageSample) {
	if prev, ok := pending[s.key]; ok && !s.at.After(prev) {
		return
	}
	pending[s.key] = s.at
}

func (w *UsageWorker) drain(pending map[usageKey]time.Time) {
	for {
		select {
		case s := <-w.samples:
			w.keep(pending, s)
		default:
			return
		}
	}
}

// SECURITY: a write failure never leaves Run — a database outage must not kill the process on every tick, and LastUsedAt is best-effort.
func (w *UsageWorker) flush(ctx context.Context, pending map[usageKey]time.Time) {
	if len(pending) == 0 {
		return
	}
	ctx, span := tracer.Start(ctx, "apikey.UsageWorker.flush")
	defer span.End()
	span.SetAttributes(attribute.Int("apikey.samples", len(pending)))

	for k, at := range pending {
		// NOTE: samples span orgs, so each write is scoped to the org and project the key was resolved under.
		scoped := tenant.Into(ctx, tenant.Context{OrgID: k.orgID, ProjectID: k.projectID})
		if err := w.store.TouchLastUsed(scoped, k.orgID, k.projectID, k.keyID, at); err != nil {
			span.RecordError(err)
			w.log.WarnContext(ctx, "apikey: last used flush failed",
				slog.String("api_key_id", k.keyID.String()),
				slog.String("err", err.Error()))
		}
	}
	clear(pending)
}
