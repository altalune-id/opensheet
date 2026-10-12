package sheet

import (
	"context"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/queue"
)

// Queue emits the broadcast that tells every replica a sheet's cached snapshots changed.
type Queue interface {
	Emit(ctx context.Context, b queue.Broadcast, data any) error
}

func snapshotInvalidated() queue.Broadcast {
	return queue.Broadcast{Name: "sheet.snapshot_invalidated", Version: 1}
}

type snapshotInvalidatedV1 struct {
	SheetID uuid.UUID `json:"sheet_id"`
	ETag    string    `json:"etag,omitempty"`
}

// Option configures a Service or a WriteWorkflow.
type Option func(*options)

type options struct {
	queue Queue
}

// WithQueue makes every snapshot invalidation reach the other replicas through q.
func WithQueue(q Queue) Option {
	return func(o *options) {
		if q != nil {
			o.queue = q
		}
	}
}

func optionsOf(opts []Option) options {
	o := options{queue: noQueue{}}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

type noQueue struct{}

func (noQueue) Emit(context.Context, queue.Broadcast, any) error { return nil }

type invalidator struct {
	queue      Queue
	unexpected apperror.UnexpectedFunc
}

// NOTE: called after the change commits; an emit failure is reported and never returned, because the change did save.
func (i invalidator) announce(ctx context.Context, op string, sheetID uuid.UUID, keepETag string) {
	if err := i.queue.Emit(ctx, snapshotInvalidated(), snapshotInvalidatedV1{SheetID: sheetID, ETag: keepETag}); err != nil {
		_ = i.unexpected(ctx, op+": emit", err, "sheet_id", sheetID)
	}
}

type localSnapshots interface {
	dropSheet(sheetID uuid.UUID, keepETag string)
}

// SnapshotListener drops this replica's in-process snapshots of a sheet some replica changed.
type SnapshotListener struct {
	snaps SnapshotStore
}

// NewSnapshotListener binds the listener to the replica's snapshot store.
func NewSnapshotListener(snaps SnapshotStore) *SnapshotListener {
	return &SnapshotListener{snaps: snaps}
}

// Listeners implements queue.ListenerProvider.
func (l *SnapshotListener) Listeners() []queue.Listener {
	return []queue.Listener{{
		Broadcast: snapshotInvalidated(),
		Handle: func(_ context.Context, m queue.Message) error {
			p, err := queue.Decode[snapshotInvalidatedV1](m)
			if err != nil {
				return err
			}
			if local, ok := l.snaps.(localSnapshots); ok {
				local.dropSheet(p.SheetID, p.ETag)
			}
			return nil
		},
	}}
}
