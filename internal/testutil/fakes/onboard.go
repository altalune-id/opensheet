package fakes

import (
	"context"
	"sync"

	"altalune.id/template/internal/onboard"
)

// Onboard is an in-memory onboard.Store for tests.
type Onboard struct {
	mu      sync.Mutex
	row     *onboard.Bootstrap
	GetErr  error
	SaveErr error
}

var _ onboard.Store = (*Onboard)(nil)

// NewOnboard returns an empty fake onboard.Store.
func NewOnboard() *Onboard { return &Onboard{} }

// Get returns a copy of the stored row, or *NotOnboardedError.
func (f *Onboard) Get(_ context.Context) (*onboard.Bootstrap, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.GetErr != nil {
		return nil, f.GetErr
	}
	if f.row == nil {
		return nil, &onboard.NotOnboardedError{}
	}
	cp := *f.row
	return &cp, nil
}

// Save inserts the singleton row; returns *AlreadyOnboardedError on conflict.
func (f *Onboard) Save(_ context.Context, b *onboard.Bootstrap) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.SaveErr != nil {
		return f.SaveErr
	}
	if f.row != nil {
		return &onboard.AlreadyOnboardedError{}
	}
	cp := *b
	f.row = &cp
	return nil
}

// Reset clears the fake state (test helper).
func (f *Onboard) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.row = nil
	f.GetErr = nil
	f.SaveErr = nil
}
