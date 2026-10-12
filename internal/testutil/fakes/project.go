// Package fakes provides in-memory Store implementations for domain modules.
package fakes

import (
	"context"
	"sort"
	"sync"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/project"
)

// Project is an in-memory project.Store.
type Project struct {
	mu         sync.Mutex
	rows       map[uuid.UUID]*project.Project
	takenSlugs int
}

// NewProject returns an empty in-memory project.Store.
func NewProject() *Project {
	return &Project{rows: make(map[uuid.UUID]*project.Project)}
}

var _ project.Store = (*Project)(nil)

// Save upserts the project by ID; it mirrors the per-org slug and one-system-project unique indexes.
func (r *Project) Save(_ context.Context, p *project.Project) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, existing := range r.rows {
		if id == p.ID {
			continue
		}
		if existing.OrgID == p.OrgID && existing.Slug == p.Slug {
			return &project.AlreadyExistsError{Field: "slug", Value: p.Slug}
		}
		if p.System && existing.System && existing.OrgID == p.OrgID {
			return &project.SystemProjectExistsError{OrgID: p.OrgID.String(), Slug: p.Slug}
		}
	}
	c := *p
	r.rows[p.ID] = &c
	return nil
}

// ByID returns the project with the given ID or a *NotFoundError.
func (r *Project) ByID(_ context.Context, id uuid.UUID) (*project.Project, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.rows[id]
	if !ok {
		return nil, &project.NotFoundError{ID: id.String()}
	}
	c := *p
	return &c, nil
}

// TakeNextSlugs makes the next n BySlug lookups report the slug as taken, without storing a row.
func (r *Project) TakeNextSlugs(n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.takenSlugs = n
}

// BySlug returns the project with the given (org, slug) or a *NotFoundError.
func (r *Project) BySlug(_ context.Context, orgID uuid.UUID, slug string) (*project.Project, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.takenSlugs > 0 {
		r.takenSlugs--
		return &project.Project{ID: uuid.New(), OrgID: orgID, Slug: slug, Name: slug}, nil
	}
	for _, p := range r.rows {
		if p.OrgID == orgID && p.Slug == slug {
			c := *p
			return &c, nil
		}
	}
	return nil, &project.NotFoundError{OrgID: orgID.String(), Slug: slug}
}

// List returns every project in orgID ordered by CreatedAt.
func (r *Project) List(_ context.Context, orgID uuid.UUID) ([]*project.Project, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*project.Project, 0)
	for _, p := range r.rows {
		if p.OrgID != orgID {
			continue
		}
		c := *p
		out = append(out, &c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}
