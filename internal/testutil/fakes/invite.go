package fakes

import (
	"context"
	"sort"
	"sync"

	"github.com/google/uuid"

	"altalune.id/template/internal/invite"
)

// Invite is an in-memory invite.Store for tests.
type Invite struct {
	mu     sync.Mutex
	byID   map[uuid.UUID]*invite.Invite
	byHash map[string]uuid.UUID
	// SaveErr, ByIDErr, ByHashErr, ListErr, FindByEmailErr, DeleteErr — inject failures.
	SaveErr        error
	ByIDErr        error
	ByHashErr      error
	ListErr        error
	FindByEmailErr error
	DeleteErr      error
	StickyError    bool
}

// NewInvite builds an empty fake invite.Store.
func NewInvite() *Invite {
	return &Invite{
		byID:   map[uuid.UUID]*invite.Invite{},
		byHash: map[string]uuid.UUID{},
	}
}

var _ invite.Store = (*Invite)(nil)

func (f *Invite) Save(_ context.Context, i *invite.Invite) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.SaveErr != nil {
		err := f.SaveErr
		if !f.StickyError {
			f.SaveErr = nil
		}
		return err
	}
	cp := *i
	f.byID[i.ID] = &cp
	f.byHash[i.TokenHash] = i.ID
	return nil
}

func (f *Invite) ByID(_ context.Context, id uuid.UUID) (*invite.Invite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ByIDErr != nil {
		err := f.ByIDErr
		if !f.StickyError {
			f.ByIDErr = nil
		}
		return nil, err
	}
	i, ok := f.byID[id]
	if !ok {
		return nil, &invite.NotFoundError{ID: id.String()}
	}
	cp := *i
	return &cp, nil
}

func (f *Invite) ByTokenHash(_ context.Context, hash string) (*invite.Invite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ByHashErr != nil {
		err := f.ByHashErr
		if !f.StickyError {
			f.ByHashErr = nil
		}
		return nil, err
	}
	id, ok := f.byHash[hash]
	if !ok {
		return nil, &invite.NotFoundError{}
	}
	cp := *f.byID[id]
	return &cp, nil
}

func (f *Invite) ListPending(_ context.Context, orgID uuid.UUID) ([]*invite.Invite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ListErr != nil {
		err := f.ListErr
		if !f.StickyError {
			f.ListErr = nil
		}
		return nil, err
	}
	out := make([]*invite.Invite, 0, len(f.byID))
	for _, i := range f.byID {
		if i.OrgID != orgID || i.IsUsed() {
			continue
		}
		cp := *i
		out = append(out, &cp)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].CreatedAt.Before(out[b].CreatedAt) })
	return out, nil
}

func (f *Invite) FindPendingForEmail(_ context.Context, email string) ([]*invite.Invite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FindByEmailErr != nil {
		err := f.FindByEmailErr
		if !f.StickyError {
			f.FindByEmailErr = nil
		}
		return nil, err
	}
	out := make([]*invite.Invite, 0, len(f.byID))
	for _, i := range f.byID {
		if i.IsUsed() {
			continue
		}
		if i.Email != email {
			continue
		}
		cp := *i
		out = append(out, &cp)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].CreatedAt.Before(out[b].CreatedAt) })
	return out, nil
}

func (f *Invite) Delete(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.DeleteErr != nil {
		err := f.DeleteErr
		if !f.StickyError {
			f.DeleteErr = nil
		}
		return err
	}
	i, ok := f.byID[id]
	if !ok {
		return &invite.NotFoundError{ID: id.String()}
	}
	delete(f.byHash, i.TokenHash)
	delete(f.byID, id)
	return nil
}

// Len returns the number of stored invites (test helper).
func (f *Invite) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.byID)
}
