package spreadsheet

import (
	"context"
	"fmt"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/google/uuid"
)

func (s *postgresStore) Save(ctx context.Context, sp *Spreadsheet) error {
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	updatedAt := sp.UpdatedAt.UTC()
	stmt := s.table.INSERT(s.table.AllColumns).
		VALUES(
			sp.ID, sp.OrgID, sp.ProjectID, sp.CredentialID,
			sp.GoogleFileID, sp.Title, sp.CreatedAt.UTC(), updatedAt,
		).
		ON_CONFLICT(s.table.ID).
		DO_UPDATE(
			postgres.SET(
				s.table.CredentialID.SET(postgres.UUID(sp.CredentialID)),
				s.table.GoogleFileID.SET(postgres.String(sp.GoogleFileID)),
				s.table.Title.SET(postgres.String(sp.Title)),
				s.table.UpdatedAt.SET(postgres.TimestampzT(updatedAt)),
			),
		)
	if _, execErr := stmt.ExecContext(ctx, tx); execErr != nil {
		if isPgUniqueViolation(execErr) {
			return s.endTx(tx, owned, &AlreadyExistsError{
				ProjectID:    sp.ProjectID.String(),
				GoogleFileID: sp.GoogleFileID,
			})
		}
		return s.endTx(tx, owned, fmt.Errorf("spreadsheet.postgres.Save: %w", execErr))
	}
	return s.endTx(tx, owned, nil)
}

func (s *postgresStore) Delete(ctx context.Context, id uuid.UUID) error {
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	stmt := s.table.DELETE().WHERE(s.table.ID.EQ(postgres.UUID(id)))
	res, execErr := stmt.ExecContext(ctx, tx)
	if execErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("spreadsheet.postgres.Delete: %w", execErr))
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("spreadsheet.postgres.Delete: rows affected: %w", raErr))
	}
	if n == 0 {
		return s.endTx(tx, owned, &NotFoundError{ID: id.String()})
	}
	return s.endTx(tx, owned, nil)
}
