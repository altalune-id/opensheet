package role

import (
	"context"

	"github.com/google/uuid"
)

// Store is the driven port. Every method reads the org from the tenant scope on ctx.
type Store interface {
	// Save inserts r, or updates it when ifVersion matches the stored version; ifVersion 0 updates unconditionally. A save bumps the stored version.
	Save(ctx context.Context, r *Role, ifVersion int64) error
	ByID(ctx context.Context, id uuid.UUID) (*Role, error)
	List(ctx context.Context) ([]*Role, error)
	// Delete removes the role, refusing with *InUseError while a member or invite holds it.
	Delete(ctx context.Context, id uuid.UUID) error
	// HeldBy counts, per role id, the members and pending invites that hold it.
	HeldBy(ctx context.Context) (map[uuid.UUID]int, error)
	// Permissions returns the permissions of roleID, or *NotFoundError.
	Permissions(ctx context.Context, roleID uuid.UUID) ([]Permission, error)
}
