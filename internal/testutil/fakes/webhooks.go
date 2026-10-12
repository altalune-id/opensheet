package fakes

import (
	"context"
	"slices"
	"sync"

	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/events"
)

// WebhookCall is one recorded Webhooks.Enqueue.
type WebhookCall struct {
	Type events.Type
	Data any
	InTx bool
}

// Webhooks is an in-memory blog.Webhooks that records every Enqueue and returns Err.
type Webhooks struct {
	mu    sync.Mutex
	Calls []WebhookCall
	Err   error
}

// Enqueue records the call, noting whether a unit of work is on ctx, and returns f.Err.
func (f *Webhooks) Enqueue(ctx context.Context, t events.Type, data any) error {
	_, inTx := db.CurrentTx(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, WebhookCall{Type: t, Data: data, InTx: inTx})
	return f.Err
}

// Recorded returns a copy of the calls so far.
func (f *Webhooks) Recorded() []WebhookCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.Calls)
}
