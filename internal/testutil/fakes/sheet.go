package fakes

import (
	"context"
	"sort"
	"sync"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/sheet"
)

// Sheet is an in-memory sheet.Store.
type Sheet struct {
	mu   sync.Mutex
	data map[uuid.UUID]*sheet.Sheet

	SaveFn   func(ctx context.Context, s *sheet.Sheet) error
	ByIDFn   func(ctx context.Context, id uuid.UUID) (*sheet.Sheet, error)
	BySlugFn func(ctx context.Context, orgID, projectID uuid.UUID, slug string) (*sheet.Sheet, error)
	ListFn   func(ctx context.Context, orgID, projectID uuid.UUID) ([]*sheet.Sheet, error)
	DeleteFn func(ctx context.Context, id uuid.UUID) error
}

// NewSheet returns an empty in-memory sheet.Store.
func NewSheet() *Sheet { return &Sheet{data: map[uuid.UUID]*sheet.Sheet{}} }

var _ sheet.Store = (*Sheet)(nil)

func (f *Sheet) Save(ctx context.Context, s *sheet.Sheet) error {
	if f.SaveFn != nil {
		return f.SaveFn(ctx, s)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, existing := range f.data {
		if id != s.ID && existing.ProjectID == s.ProjectID && existing.Slug == s.Slug {
			return &sheet.AlreadyExistsError{Field: "slug", Value: s.Slug}
		}
	}
	cp := *s
	f.data[s.ID] = &cp
	return nil
}

func (f *Sheet) ByID(ctx context.Context, id uuid.UUID) (*sheet.Sheet, error) {
	if f.ByIDFn != nil {
		return f.ByIDFn(ctx, id)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.data[id]
	if !ok {
		return nil, &sheet.NotFoundError{ID: id.String()}
	}
	cp := *s
	return &cp, nil
}

func (f *Sheet) BySlug(ctx context.Context, orgID, projectID uuid.UUID, slug string) (*sheet.Sheet, error) {
	if f.BySlugFn != nil {
		return f.BySlugFn(ctx, orgID, projectID, slug)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.data {
		if s.OrgID == orgID && s.ProjectID == projectID && s.Slug == slug {
			cp := *s
			return &cp, nil
		}
	}
	return nil, &sheet.NotFoundError{OrgID: orgID.String(), ProjectID: projectID.String(), Slug: slug}
}

func (f *Sheet) List(ctx context.Context, orgID, projectID uuid.UUID) ([]*sheet.Sheet, error) {
	if f.ListFn != nil {
		return f.ListFn(ctx, orgID, projectID)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*sheet.Sheet, 0, len(f.data))
	for _, s := range f.data {
		if s.OrgID != orgID || s.ProjectID != projectID {
			continue
		}
		cp := *s
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

func (f *Sheet) Delete(ctx context.Context, id uuid.UUID) error {
	if f.DeleteFn != nil {
		return f.DeleteFn(ctx, id)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.data[id]; !ok {
		return &sheet.NotFoundError{ID: id.String()}
	}
	delete(f.data, id)
	return nil
}
