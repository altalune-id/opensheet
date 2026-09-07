// Package fakes ships in-memory Store implementations for domain modules.
package fakes

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"altalune.id/template/internal/todo"
)

// Todo is an in-memory todo.Store.
type Todo struct {
	mu   sync.Mutex
	data map[uuid.UUID]*todo.Todo

	MarkDoneOlderThanFn func(ctx context.Context, orgID uuid.UUID, cutoff time.Time, batch int) (int, error)
}

// NewTodo returns an empty in-memory todo.Store.
func NewTodo() *Todo { return &Todo{data: map[uuid.UUID]*todo.Todo{}} }

var _ todo.Store = (*Todo)(nil)

func (f *Todo) Save(_ context.Context, t *todo.Todo) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *t
	f.data[t.ID] = &cp
	return nil
}

func (f *Todo) ByID(_ context.Context, id uuid.UUID) (*todo.Todo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.data[id]
	if !ok {
		return nil, &todo.NotFoundError{ID: id.String()}
	}
	cp := *t
	return &cp, nil
}

func (f *Todo) List(_ context.Context, orgID, projectID uuid.UUID, opts todo.ListOpts) ([]*todo.Todo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*todo.Todo, 0, len(f.data))
	for _, t := range f.data {
		if t.OrgID != orgID || t.ProjectID != projectID {
			continue
		}
		if opts.Done != nil && t.Done != *opts.Done {
			continue
		}
		cp := *t
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (f *Todo) Delete(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.data[id]; !ok {
		return &todo.NotFoundError{ID: id.String()}
	}
	delete(f.data, id)
	return nil
}

func (f *Todo) ClearDone(_ context.Context, orgID, projectID uuid.UUID) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for id, t := range f.data {
		if t.OrgID == orgID && t.ProjectID == projectID && t.Done {
			delete(f.data, id)
			n++
		}
	}
	return n, nil
}

func (f *Todo) MarkDoneOlderThan(ctx context.Context, orgID uuid.UUID, cutoff time.Time, batch int) (int, error) {
	if f.MarkDoneOlderThanFn != nil {
		return f.MarkDoneOlderThanFn(ctx, orgID, cutoff, batch)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, t := range f.data {
		if t.OrgID != orgID || t.Done || !t.CreatedAt.Before(cutoff) {
			continue
		}
		t.Done = true
		t.UpdatedAt = time.Now().UTC()
		n++
	}
	return n, nil
}
