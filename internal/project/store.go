package project

import (
	"context"

	"github.com/google/uuid"
)

// Store is the driven port. Adapters (postgres.go, sqlite.go, fakes.Project) implement it.
type Store interface {
	Save(ctx context.Context, p *Project) error
	ByID(ctx context.Context, id uuid.UUID) (*Project, error)
	BySlug(ctx context.Context, orgID uuid.UUID, slug string) (*Project, error)
	List(ctx context.Context, orgID uuid.UUID) ([]*Project, error)
}
