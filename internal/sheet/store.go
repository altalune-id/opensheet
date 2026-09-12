package sheet

import (
	"context"

	"github.com/google/uuid"
)

// Store is the driven port.
type Store interface {
	Save(ctx context.Context, s *Sheet) error
	ByID(ctx context.Context, id uuid.UUID) (*Sheet, error)
	BySlug(ctx context.Context, orgID, projectID uuid.UUID, slug string) (*Sheet, error)
	List(ctx context.Context, orgID, projectID uuid.UUID) ([]*Sheet, error)
	Delete(ctx context.Context, id uuid.UUID) error
}
