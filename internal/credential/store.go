package credential

import (
	"context"

	"github.com/google/uuid"
)

// Store is the driven port.
type Store interface {
	Save(ctx context.Context, c *Credential) error
	ByID(ctx context.Context, id uuid.UUID) (*Credential, error)
	List(ctx context.Context, orgID, projectID uuid.UUID) ([]*Credential, error)
	Delete(ctx context.Context, id uuid.UUID) error
}
