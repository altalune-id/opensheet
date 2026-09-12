package fakes

import (
	"context"
	"sort"
	"sync"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/credential"
)

// Credential is an in-memory credential.Store.
type Credential struct {
	mu   sync.Mutex
	data map[uuid.UUID]*credential.Credential

	SaveFn   func(ctx context.Context, c *credential.Credential) error
	ByIDFn   func(ctx context.Context, id uuid.UUID) (*credential.Credential, error)
	ListFn   func(ctx context.Context, orgID, projectID uuid.UUID) ([]*credential.Credential, error)
	DeleteFn func(ctx context.Context, id uuid.UUID) error
}

// NewCredential returns an empty in-memory credential.Store.
func NewCredential() *Credential {
	return &Credential{data: map[uuid.UUID]*credential.Credential{}}
}

var _ credential.Store = (*Credential)(nil)

// Seed stores c without going through the SaveFn hook.
func (f *Credential) Seed(c *credential.Credential) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *c
	f.data[c.ID] = &cp
}

func (f *Credential) Save(ctx context.Context, c *credential.Credential) error {
	if f.SaveFn != nil {
		return f.SaveFn(ctx, c)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, existing := range f.data {
		if existing.ID != c.ID && existing.ProjectID == c.ProjectID && existing.Name == c.Name {
			return &credential.AlreadyExistsError{Name: c.Name}
		}
	}
	cp := *c
	f.data[c.ID] = &cp
	return nil
}

func (f *Credential) ByID(ctx context.Context, id uuid.UUID) (*credential.Credential, error) {
	if f.ByIDFn != nil {
		return f.ByIDFn(ctx, id)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.data[id]
	if !ok {
		return nil, &credential.NotFoundError{ID: id.String()}
	}
	cp := *c
	return &cp, nil
}

func (f *Credential) List(ctx context.Context, orgID, projectID uuid.UUID) ([]*credential.Credential, error) {
	if f.ListFn != nil {
		return f.ListFn(ctx, orgID, projectID)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*credential.Credential, 0, len(f.data))
	for _, c := range f.data {
		if c.OrgID != orgID || c.ProjectID != projectID {
			continue
		}
		cp := *c
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (f *Credential) Delete(ctx context.Context, id uuid.UUID) error {
	if f.DeleteFn != nil {
		return f.DeleteFn(ctx, id)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.data[id]; !ok {
		return &credential.NotFoundError{ID: id.String()}
	}
	delete(f.data, id)
	return nil
}
