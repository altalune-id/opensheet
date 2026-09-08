package fakes

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/apikey"
)

// APIKey is an in-memory apikey.Store for tests.
type APIKey struct {
	mu       sync.Mutex
	byID     map[uuid.UUID]*apikey.APIKey
	byPrefix map[string]uuid.UUID
	// SaveErr, ByIDErr, ByPrefixErr, ListErr, DeleteErr, TouchLastUsedErr — inject failures.
	SaveErr          error
	ByIDErr          error
	ByPrefixErr      error
	ListErr          error
	DeleteErr        error
	TouchLastUsedErr error
	StickyError      bool
}

// NewAPIKey builds an empty fake apikey.Store.
func NewAPIKey() *APIKey {
	return &APIKey{
		byID:     map[uuid.UUID]*apikey.APIKey{},
		byPrefix: map[string]uuid.UUID{},
	}
}

var _ apikey.Store = (*APIKey)(nil)

func clonePublic(k *apikey.APIKey) *apikey.APIKey {
	cp := cloneFull(k)
	// SECURITY: mirrors the adapters' PublicColumns read — only ByPrefix returns secret material.
	cp.SecretHash = nil
	return cp
}

func cloneFull(k *apikey.APIKey) *apikey.APIKey {
	cp := *k
	cp.SecretHash = slices.Clone(k.SecretHash)
	cp.Scopes = slices.Clone(k.Scopes)
	cp.SheetIDs = slices.Clone(k.SheetIDs)
	return &cp
}

func (f *APIKey) Save(_ context.Context, k *apikey.APIKey) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.SaveErr != nil {
		err := f.SaveErr
		if !f.StickyError {
			f.SaveErr = nil
		}
		return err
	}
	cp := cloneFull(k)
	// SECURITY: mirrors the adapters — a key read without secret material must not blank the stored hash.
	if len(cp.SecretHash) == 0 {
		prev, ok := f.byID[k.ID]
		if !ok {
			return &apikey.NotFoundError{ID: k.ID.String()}
		}
		cp.SecretHash = slices.Clone(prev.SecretHash)
		cp.KeyPrefix = prev.KeyPrefix
	}
	if id, ok := f.byPrefix[cp.KeyPrefix]; ok && id != cp.ID {
		return &apikey.AlreadyExistsError{Field: "key_prefix", Value: cp.KeyPrefix}
	}
	f.byID[cp.ID] = cp
	f.byPrefix[cp.KeyPrefix] = cp.ID
	return nil
}

func (f *APIKey) ByID(_ context.Context, id uuid.UUID) (*apikey.APIKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ByIDErr != nil {
		err := f.ByIDErr
		if !f.StickyError {
			f.ByIDErr = nil
		}
		return nil, err
	}
	k, ok := f.byID[id]
	if !ok {
		return nil, &apikey.NotFoundError{ID: id.String()}
	}
	return clonePublic(k), nil
}

func (f *APIKey) ByPrefix(_ context.Context, prefix string) (*apikey.APIKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ByPrefixErr != nil {
		err := f.ByPrefixErr
		if !f.StickyError {
			f.ByPrefixErr = nil
		}
		return nil, err
	}
	id, ok := f.byPrefix[prefix]
	if !ok {
		return nil, &apikey.NotFoundError{}
	}
	return cloneFull(f.byID[id]), nil
}

func (f *APIKey) List(_ context.Context, orgID, projectID uuid.UUID) ([]*apikey.APIKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ListErr != nil {
		err := f.ListErr
		if !f.StickyError {
			f.ListErr = nil
		}
		return nil, err
	}
	out := make([]*apikey.APIKey, 0, len(f.byID))
	for _, k := range f.byID {
		if k.OrgID != orgID || k.ProjectID != projectID {
			continue
		}
		out = append(out, clonePublic(k))
	}
	sort.Slice(out, func(a, b int) bool { return out[a].CreatedAt.After(out[b].CreatedAt) })
	return out, nil
}

func (f *APIKey) Delete(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.DeleteErr != nil {
		err := f.DeleteErr
		if !f.StickyError {
			f.DeleteErr = nil
		}
		return err
	}
	k, ok := f.byID[id]
	if !ok {
		return &apikey.NotFoundError{ID: id.String()}
	}
	delete(f.byPrefix, k.KeyPrefix)
	delete(f.byID, id)
	return nil
}

// TouchLastUsed stamps the key's LastUsedAt; an unknown or out-of-tenant row is a no-op, not an error.
func (f *APIKey) TouchLastUsed(_ context.Context, orgID, projectID, id uuid.UUID, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.TouchLastUsedErr != nil {
		err := f.TouchLastUsedErr
		if !f.StickyError {
			f.TouchLastUsedErr = nil
		}
		return err
	}
	k, ok := f.byID[id]
	if !ok || k.OrgID != orgID || k.ProjectID != projectID {
		return nil
	}
	t := at.UTC()
	k.LastUsedAt = &t
	return nil
}

// Len returns the number of stored keys (test helper).
func (f *APIKey) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.byID)
}

// APIKeySheets is an in-memory apikey.Sheets.
type APIKeySheets struct {
	mu    sync.Mutex
	known map[sheetKey]bool
	// Err — inject a lookup failure.
	Err error
}

type sheetKey struct {
	orgID     uuid.UUID
	projectID uuid.UUID
	sheetID   uuid.UUID
}

// NewAPIKeySheets builds an empty fake apikey.Sheets.
func NewAPIKeySheets() *APIKeySheets {
	return &APIKeySheets{known: map[sheetKey]bool{}}
}

var _ apikey.Sheets = (*APIKeySheets)(nil)

// Add registers sheetID as belonging to the given project.
func (f *APIKeySheets) Add(orgID, projectID, sheetID uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.known[sheetKey{orgID: orgID, projectID: projectID, sheetID: sheetID}] = true
}

func (f *APIKeySheets) IDsInProject(_ context.Context, orgID, projectID uuid.UUID, ids []uuid.UUID) ([]uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return nil, f.Err
	}
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if f.known[sheetKey{orgID: orgID, projectID: projectID, sheetID: id}] {
			out = append(out, id)
		}
	}
	return out, nil
}
