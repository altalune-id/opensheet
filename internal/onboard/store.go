package onboard

import "context"

// Store is the driven port persisting the singleton Bootstrap row.
type Store interface {
	Get(ctx context.Context) (*Bootstrap, error)
	Save(ctx context.Context, b *Bootstrap) error
}
