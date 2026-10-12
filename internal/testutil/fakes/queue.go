package fakes

import (
	"context"
	"slices"
	"sync"

	"altalune.id/opensheet/internal/platform/queue"
)

// QueueCall is one recorded Queue.Submit.
type QueueCall struct {
	Job  queue.Job
	Data any
}

// EmitCall is one recorded Queue.Emit.
type EmitCall struct {
	Broadcast queue.Broadcast
	Data      any
}

// Queue is an in-memory todo.Queue and sheet.Queue that records every Submit and Emit.
type Queue struct {
	mu        sync.Mutex
	submitted []QueueCall
	emitted   []EmitCall
	Err       error
	EmitErr   error
}

// Submit records the call and returns f.Err.
func (f *Queue) Submit(_ context.Context, j queue.Job, data any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submitted = append(f.submitted, QueueCall{Job: j, Data: data})
	return f.Err
}

// Emit records the call and returns f.EmitErr.
func (f *Queue) Emit(_ context.Context, b queue.Broadcast, data any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.emitted = append(f.emitted, EmitCall{Broadcast: b, Data: data})
	return f.EmitErr
}

// Recorded returns a copy of the calls so far.
func (f *Queue) Recorded() []QueueCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.submitted)
}

// Emitted returns a copy of the broadcasts emitted so far.
func (f *Queue) Emitted() []EmitCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.emitted)
}

// Reset clears the recorded submissions and broadcasts.
func (f *Queue) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submitted = nil
	f.emitted = nil
}
