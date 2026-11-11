package outbox

import (
	"context"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/db"
)

// Waker coalesces wake-ups for one dispatch Worker.
type Waker struct {
	ch chan struct{}
}

// NewWaker returns a Waker that holds at most one pending wake-up.
func NewWaker() *Waker { return &Waker{ch: make(chan struct{}, 1)} }

// Wake signals the worker without blocking.
func (w *Waker) Wake() {
	select {
	case w.ch <- struct{}{}:
	default:
	}
}

type wakingStore struct {
	Store
	waker *Waker
}

// WakeOnEnqueue returns s with every committed Enqueue, Requeue and RequeueFailed waking waker.
func WakeOnEnqueue(s Store, waker *Waker) Store { return &wakingStore{Store: s, waker: waker} }

func (s *wakingStore) Enqueue(ctx context.Context, e Entry) error {
	if err := s.Store.Enqueue(ctx, e); err != nil {
		return err
	}
	db.AfterCommit(ctx, s.waker.Wake)
	return nil
}

func (s *wakingStore) Requeue(ctx context.Context, id uuid.UUID, target string) error {
	if err := s.Store.Requeue(ctx, id, target); err != nil {
		return err
	}
	db.AfterCommit(ctx, s.waker.Wake)
	return nil
}

func (s *wakingStore) RequeueFailed(ctx context.Context, target string) (int, error) {
	n, err := s.Store.RequeueFailed(ctx, target)
	if err != nil || n == 0 {
		return n, err
	}
	db.AfterCommit(ctx, s.waker.Wake)
	return n, nil
}
