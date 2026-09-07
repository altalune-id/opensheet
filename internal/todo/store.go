package todo

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Store is the driven port.
type Store interface {
	Save(ctx context.Context, t *Todo) error
	ByID(ctx context.Context, id uuid.UUID) (*Todo, error)
	List(ctx context.Context, orgID, projectID uuid.UUID, opts ListOpts) ([]*Todo, error)
	Delete(ctx context.Context, id uuid.UUID) error
	ClearDone(ctx context.Context, orgID, projectID uuid.UUID) (int, error)
	MarkDoneOlderThan(ctx context.Context, orgID uuid.UUID, cutoff time.Time, batch int) (int, error)
}
