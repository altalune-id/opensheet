package apikey

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/tenant"
)

// UsageWorker batches TouchLastUsed writes on a ticker, so the authentication path never blocks on one.
type UsageWorker struct {
	store        Store
	interval     time.Duration
	drainTimeout time.Duration
	log          *slog.Logger

	mu      sync.Mutex
	pending map[uuid.UUID]usageEntry
}

type usageEntry struct {
	tc tenant.Context
	at time.Time
}

// NewUsageWorker returns a UsageWorker that flushes queued usage timestamps to store every interval.
func NewUsageWorker(store Store, interval time.Duration, log *slog.Logger) *UsageWorker {
	if log == nil {
		log = slog.Default()
	}
	return &UsageWorker{
		store:        store,
		interval:     interval,
		drainTimeout: 5 * time.Second,
		log:          log.With("module", "apikey.usage"),
		pending:      make(map[uuid.UUID]usageEntry),
	}
}

// Record queues id's last-used timestamp for the next flush without touching the store. NOTE: tc travels with the entry because the worker runs on the untenanted process context.
func (w *UsageWorker) Record(id uuid.UUID, tc tenant.Context, at time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if cur, ok := w.pending[id]; !ok || at.After(cur.at) {
		w.pending[id] = usageEntry{tc: tc, at: at}
	}
}

// Name implements worker.Worker.
func (w *UsageWorker) Name() string { return "apikey.usage" }

// Run implements worker.Worker, flushing pending writes on a ticker until ctx is done.
func (w *UsageWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			w.drain(ctx)
			return ctx.Err()
		case <-ticker.C:
			w.flush(ctx)
		}
	}
}

// NOTE: detached from the cancelled ctx so the last interval of timestamps is not lost.
func (w *UsageWorker) drain(ctx context.Context) {
	out, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.drainTimeout)
	defer cancel()
	w.flush(out)
}

func (w *UsageWorker) flush(ctx context.Context) {
	w.mu.Lock()
	if len(w.pending) == 0 {
		w.mu.Unlock()
		return
	}
	batch := w.pending
	w.pending = make(map[uuid.UUID]usageEntry)
	w.mu.Unlock()

	for id, e := range batch {
		if err := w.store.TouchLastUsed(tenant.Into(ctx, e.tc), id, e.at); err != nil {
			w.log.Error("apikey usage flush failed", slog.String("apikey_id", id.String()), slog.Any("err", err))
		}
	}
}
