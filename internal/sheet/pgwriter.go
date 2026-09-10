package sheet

import (
	"context"
	"fmt"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/google/uuid"
)

func (s *postgresStore) Save(ctx context.Context, sh *Sheet) error {
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	secs := secsFromTTL(sh.CacheTTL)
	stmt := s.table.INSERT(s.table.AllColumns).
		VALUES(
			sh.ID, sh.OrgID, sh.ProjectID, sh.SpreadsheetID,
			sh.Tab, sh.Slug, string(sh.Visibility), secs, sh.Writable,
			sh.CreatedAt.UTC(), sh.UpdatedAt.UTC(),
			sh.Generation, pgNullableTime(sh.ValidatedAt), sh.ContractOK, sh.ContractReason,
		).
		ON_CONFLICT(s.table.ID).
		DO_UPDATE(
			postgres.SET(
				s.table.Tab.SET(postgres.String(sh.Tab)),
				s.table.Slug.SET(postgres.String(sh.Slug)),
				s.table.Visibility.SET(postgres.String(string(sh.Visibility))),
				s.table.CacheTTLSecs.SET(postgres.Int64(secs)),
				s.table.Writable.SET(postgres.Bool(sh.Writable)),
				s.table.UpdatedAt.SET(postgres.TimestampzT(sh.UpdatedAt.UTC())),
			),
		)
	if _, execErr := stmt.ExecContext(ctx, tx); execErr != nil {
		if isPgUniqueViolation(execErr) {
			return s.endTx(tx, owned, &AlreadyExistsError{Field: "slug", Value: sh.Slug})
		}
		return s.endTx(tx, owned, fmt.Errorf("sheet.postgres.Save: %w", execErr))
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
		return s.endTx(tx, owned, fmt.Errorf("sheet.postgres.Delete: %w", execErr))
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("sheet.postgres.Delete: rows affected: %w", raErr))
	}
	if n == 0 {
		return s.endTx(tx, owned, &NotFoundError{ID: id.String()})
	}
	return s.endTx(tx, owned, nil)
}
