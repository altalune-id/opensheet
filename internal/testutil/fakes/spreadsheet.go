package fakes

import (
	"context"
	"sort"
	"sync"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/spreadsheet"
)

// Spreadsheet is an in-memory spreadsheet.Store.
type Spreadsheet struct {
	mu   sync.Mutex
	data map[uuid.UUID]*spreadsheet.Spreadsheet

	SaveFn           func(ctx context.Context, s *spreadsheet.Spreadsheet) error
	ByIDFn           func(ctx context.Context, id uuid.UUID) (*spreadsheet.Spreadsheet, error)
	ByGoogleFileIDFn func(ctx context.Context, orgID, projectID uuid.UUID, fileID string) (*spreadsheet.Spreadsheet, error)
	ListFn           func(ctx context.Context, orgID, projectID uuid.UUID) ([]*spreadsheet.Spreadsheet, error)
	DeleteFn         func(ctx context.Context, id uuid.UUID) error
}

// NewSpreadsheet returns an empty in-memory spreadsheet.Store.
func NewSpreadsheet() *Spreadsheet {
	return &Spreadsheet{data: map[uuid.UUID]*spreadsheet.Spreadsheet{}}
}

var _ spreadsheet.Store = (*Spreadsheet)(nil)

func (f *Spreadsheet) Save(ctx context.Context, s *spreadsheet.Spreadsheet) error {
	if f.SaveFn != nil {
		return f.SaveFn(ctx, s)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, existing := range f.data {
		if existing.ID == s.ID {
			continue
		}
		if existing.ProjectID == s.ProjectID && existing.GoogleFileID == s.GoogleFileID {
			return &spreadsheet.AlreadyExistsError{
				ProjectID:    s.ProjectID.String(),
				GoogleFileID: s.GoogleFileID,
			}
		}
	}
	cp := *s
	f.data[s.ID] = &cp
	return nil
}

func (f *Spreadsheet) ByID(ctx context.Context, id uuid.UUID) (*spreadsheet.Spreadsheet, error) {
	if f.ByIDFn != nil {
		return f.ByIDFn(ctx, id)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.data[id]
	if !ok {
		return nil, &spreadsheet.NotFoundError{ID: id.String()}
	}
	cp := *s
	return &cp, nil
}

func (f *Spreadsheet) ByGoogleFileID(ctx context.Context, orgID, projectID uuid.UUID, fileID string) (*spreadsheet.Spreadsheet, error) {
	if f.ByGoogleFileIDFn != nil {
		return f.ByGoogleFileIDFn(ctx, orgID, projectID, fileID)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.data {
		if s.OrgID == orgID && s.ProjectID == projectID && s.GoogleFileID == fileID {
			cp := *s
			return &cp, nil
		}
	}
	return nil, &spreadsheet.NotFoundError{ProjectID: projectID.String(), GoogleFileID: fileID}
}

func (f *Spreadsheet) List(ctx context.Context, orgID, projectID uuid.UUID) ([]*spreadsheet.Spreadsheet, error) {
	if f.ListFn != nil {
		return f.ListFn(ctx, orgID, projectID)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*spreadsheet.Spreadsheet, 0, len(f.data))
	for _, s := range f.data {
		if s.OrgID != orgID || s.ProjectID != projectID {
			continue
		}
		cp := *s
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (f *Spreadsheet) Delete(ctx context.Context, id uuid.UUID) error {
	if f.DeleteFn != nil {
		return f.DeleteFn(ctx, id)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.data[id]; !ok {
		return &spreadsheet.NotFoundError{ID: id.String()}
	}
	delete(f.data, id)
	return nil
}
