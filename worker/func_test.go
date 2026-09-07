package worker

import (
	"context"
	"errors"
	"testing"
)

func TestFunc_RunsAndReturns(t *testing.T) {
	sentinel := errors.New("sentinel")
	w := Func("worker-a", func(ctx context.Context) error { return sentinel })
	if w.Name() != "worker-a" {
		t.Fatalf("Name=%q, want worker-a", w.Name())
	}
	if err := w.Run(context.Background()); !errors.Is(err, sentinel) {
		t.Fatalf("Run err=%v, want %v", err, sentinel)
	}
}

func TestFunc_HonorsContext(t *testing.T) {
	w := Func("blocker", func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := w.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run err=%v, want context.Canceled", err)
	}
}
