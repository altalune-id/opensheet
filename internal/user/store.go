package user

import (
	"context"

	"github.com/google/uuid"
)

// Store is the driven port persisting User aggregates.
type Store interface {
	Save(ctx context.Context, u *User) error
	ByID(ctx context.Context, id uuid.UUID) (*User, error)
	ByEmail(ctx context.Context, email string) (*User, error)
	HasLocalUsers(ctx context.Context) (bool, error)
	UpdateLocale(ctx context.Context, id uuid.UUID, locale string) error
}
