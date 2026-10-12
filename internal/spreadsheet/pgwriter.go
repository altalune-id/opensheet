package spreadsheet

import (
	"context"
	"fmt"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/google/uuid"
)

func (s *postgresStore) Save(ctx context.Context, sp *Spreadsheet) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	updatedAt := sp.UpdatedAt.UTC()
	stmt := s.table.INSERT(s.table.AllColumns).
		VALUES(
			sp.ID, sp.OrgID, sp.ProjectID, sp.CredentialID,
			sp.GoogleFileID, sp.Title, sp.Writable, sp.CreatedAt.UTC(), updatedAt,
		).
		ON_CONFLICT(s.table.ID).
		// SECURITY: the conflict clause carries the tenant predicate; without it an attacker-supplied row id updates another org's row.
		DO_UPDATE(
			postgres.SET(
				s.table.CredentialID.SET(postgres.UUID(sp.CredentialID)),
				s.table.GoogleFileID.SET(postgres.String(sp.GoogleFileID)),
				s.table.Title.SET(postgres.String(sp.Title)),
				s.table.Writable.SET(postgres.Bool(sp.Writable)),
				s.table.UpdatedAt.SET(postgres.TimestampzT(updatedAt)),
			).WHERE(s.table.OrgID.EQ(postgres.UUID(tc.OrgID))),
		)
	res, execErr := stmt.ExecContext(ctx, tx)
	if execErr != nil {
		if isPgUniqueViolation(execErr) {
			return s.endTx(tx, owned, &AlreadyExistsError{
				ProjectID:    sp.ProjectID.String(),
				GoogleFileID: sp.GoogleFileID,
			})
		}
		return s.endTx(tx, owned, fmt.Errorf("spreadsheet.postgres.Save: %w", execErr))
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("spreadsheet.postgres.Save: rows affected: %w", raErr))
	}
	if n == 0 {
		return s.endTx(tx, owned, &NotFoundError{ID: sp.ID.String()})
	}
	return s.endTx(tx, owned, nil)
}

func (s *postgresStore) Delete(ctx context.Context, id uuid.UUID) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	// SECURITY: org predicate, not RLS alone — a BYPASSRLS role would otherwise delete another org's row.
	stmt := s.table.DELETE().WHERE(s.table.ID.EQ(postgres.UUID(id)).
		AND(s.table.OrgID.EQ(postgres.UUID(tc.OrgID))))
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
