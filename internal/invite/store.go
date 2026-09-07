package invite

import (
	"context"

	"github.com/google/uuid"
)

// Store is the driven port. Adapters (postgres.go, sqlite.go, fakes.Invite) implement it.
type Store interface {
	Save(ctx context.Context, i *Invite) error
	ByID(ctx context.Context, id uuid.UUID) (*Invite, error)
	ByTokenHash(ctx context.Context, hash string) (*Invite, error)
	ListPending(ctx context.Context, orgID uuid.UUID) ([]*Invite, error)
	FindPendingForEmail(ctx context.Context, email string) ([]*Invite, error)
	Delete(ctx context.Context, id uuid.UUID) error
}
