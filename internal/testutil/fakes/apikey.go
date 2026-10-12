package fakes

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/platform/tenant"
)

// APIKey is an in-memory apikey.Store for tests.
type APIKey struct {
	mu   sync.Mutex
	byID map[uuid.UUID]*apikey.APIKey

	// BySecretHashCalled records whether BySecretHash was invoked.
	BySecretHashCalled bool
	// BySecretHashErr, when set, is returned by the next BySecretHash call instead of a lookup.
	BySecretHashErr error
}

// NewAPIKey returns an empty in-memory apikey.Store.
func NewAPIKey() *APIKey {
	return &APIKey{byID: map[uuid.UUID]*apikey.APIKey{}}
}

var _ apikey.Store = (*APIKey)(nil)

// Seed inserts k directly, bypassing Save.
func (f *APIKey) Seed(k *apikey.APIKey) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byID[k.ID] = cloneAPIKey(k)
}

// All returns every stored key, in no particular order.
func (f *APIKey) All() []*apikey.APIKey {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*apikey.APIKey, 0, len(f.byID))
	for _, k := range f.byID {
		out = append(out, cloneAPIKey(k))
	}
	return out
}

func (f *APIKey) Save(_ context.Context, k *apikey.APIKey) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byID[k.ID] = cloneAPIKey(k)
	return nil
}

func (f *APIKey) ByID(_ context.Context, id uuid.UUID) (*apikey.APIKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k, ok := f.byID[id]
	if !ok {
		return nil, &apikey.NotFoundError{}
	}
	return cloneAPIKey(k), nil
}

func (f *APIKey) BySecretHash(_ context.Context, hash [32]byte) (*apikey.APIKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.BySecretHashCalled = true
	if f.BySecretHashErr != nil {
		return nil, f.BySecretHashErr
	}
	for _, k := range f.byID {
		if k.SecretHash == hash {
			return cloneAPIKey(k), nil
		}
	}
	return nil, &apikey.NotFoundError{}
}

func (f *APIKey) List(_ context.Context, projectID uuid.UUID) ([]*apikey.APIKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*apikey.APIKey, 0, len(f.byID))
	for _, k := range f.byID {
		if k.Kind != apikey.KindProject || k.ProjectID != projectID {
			continue
		}
		out = append(out, cloneAPIKey(k))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// ListOrg returns the org keys of the ctx's org. NOTE: the real store scopes by org, so the fake must too.
func (f *APIKey) ListOrg(ctx context.Context) ([]*apikey.APIKey, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*apikey.APIKey, 0, len(f.byID))
	for _, k := range f.byID {
		if k.Kind != apikey.KindOrg || k.OrgID != tc.OrgID {
			continue
		}
		out = append(out, cloneAPIKey(k))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// ListPersonal returns ownerID's personal tokens in the ctx's org. NOTE: the real store scopes by org, so the fake must too.
func (f *APIKey) ListPersonal(ctx context.Context, ownerID uuid.UUID) ([]*apikey.APIKey, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*apikey.APIKey, 0, len(f.byID))
	for _, k := range f.byID {
		if k.Kind != apikey.KindPersonal || k.OrgID != tc.OrgID || k.CreatedBy != ownerID {
			continue
		}
		out = append(out, cloneAPIKey(k))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// RevokePersonal revokes ownerID's unrevoked personal tokens in the ctx's org.
func (f *APIKey) RevokePersonal(ctx context.Context, ownerID uuid.UUID, at time.Time) error {
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, k := range f.byID {
		if k.Kind == apikey.KindPersonal && k.OrgID == tc.OrgID && k.CreatedBy == ownerID && k.RevokedAt == nil {
			t := at
			k.RevokedAt = &t
		}
	}
	return nil
}

func (f *APIKey) TouchLastUsed(ctx context.Context, id uuid.UUID, at time.Time) error {
	// NOTE: the real store scopes this write by org; the fake must too, or it hides defects.
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	k, ok := f.byID[id]
	if !ok || k.OrgID != tc.OrgID {
		return &apikey.NotFoundError{}
	}
	t := at
	k.LastUsedAt = &t
	return nil
}

func cloneAPIKey(k *apikey.APIKey) *apikey.APIKey {
	cp := *k
	cp.Scopes = slices.Clone(k.Scopes)
	cp.ResourceIDs = slices.Clone(k.ResourceIDs)
	cp.ProjectIDs = slices.Clone(k.ProjectIDs)
	return &cp
}
