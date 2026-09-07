package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

type stubWorker struct {
	name string
	run  func(context.Context) error
}

func (s stubWorker) Name() string                  { return s.name }
func (s stubWorker) Run(ctx context.Context) error { return s.run(ctx) }

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSupervisor_AllReturnNil(t *testing.T) {
	s := New(newTestLogger())
	var a, b atomic.Bool
	s.Register(stubWorker{"a", func(ctx context.Context) error { a.Store(true); return nil }})
	s.Register(stubWorker{"b", func(ctx context.Context) error { b.Store(true); return nil }})
	if err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !a.Load() || !b.Load() {
		t.Fatal("expected both workers to run")
	}
}

func TestSupervisor_ErrorCancelsSiblings(t *testing.T) {
	s := New(newTestLogger())
	fatal := errors.New("boom")
	s.Register(stubWorker{"bad", func(context.Context) error { return fatal }})
	var ok atomic.Bool
	s.Register(stubWorker{"good", func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			ok.Store(true)
			return ctx.Err()
		case <-time.After(2 * time.Second):
			return nil
		}
	}})
	err := s.Run(context.Background())
	if !errors.Is(err, fatal) {
		t.Fatalf("err=%v, want %v", err, fatal)
	}
	if !ok.Load() {
		t.Fatal("expected sibling worker to observe cancellation")
	}
}

func TestSupervisor_ContextCancel(t *testing.T) {
	s := New(newTestLogger())
	s.Register(stubWorker{"blocker", func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := s.Run(ctx); err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestSupervisor_NoWorkers(t *testing.T) {
	if err := New(newTestLogger()).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSupervisor_NilLogger(t *testing.T) {
	s := New(nil)
	if s.log == nil {
		t.Fatal("expected New(nil) to fall back to slog.Default()")
	}
}

func TestSupervisor_WorkersReportsRegistrationOrder(t *testing.T) {
	blockUntilDone := func(ctx context.Context) error { <-ctx.Done(); return nil }
	s := New(nil)
	s.Register(stubWorker{name: "a", run: blockUntilDone})
	s.Register(stubWorker{name: "b", run: blockUntilDone})

	got := s.Workers()
	if len(got) != 2 || got[0].Name() != "a" || got[1].Name() != "b" {
		t.Fatalf("Workers() = %v, want [a b] in order", got)
	}

	got[0] = stubWorker{name: "mutated", run: blockUntilDone}
	if s.Workers()[0].Name() != "a" {
		t.Fatal("Workers() must return a copy the caller cannot use to mutate the supervisor")
	}
}
