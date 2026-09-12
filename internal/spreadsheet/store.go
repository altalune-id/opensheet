package spreadsheet

import (
	"context"

	"github.com/google/uuid"
)

// Store is the driven port. Adapters (postgres.go, sqlite.go, fakes.Spreadsheet) implement it.
type Store interface {
	Save(ctx context.Context, s *Spreadsheet) error
	ByID(ctx context.Context, id uuid.UUID) (*Spreadsheet, error)
	ByGoogleFileID(ctx context.Context, orgID, projectID uuid.UUID, fileID string) (*Spreadsheet, error)
	List(ctx context.Context, orgID, projectID uuid.UUID) ([]*Spreadsheet, error)
	Delete(ctx context.Context, id uuid.UUID) error
}
