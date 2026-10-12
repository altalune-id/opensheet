package sheet

import (
	"context"
	"fmt"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/tenant"
)

func (s *postgresStore) Save(ctx context.Context, sh *Sheet) error {
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	headerColumns, err := encodeHeaderColumns(sh.HeaderColumns)
	if err != nil {
		return s.endTx(tx, owned, err)
	}
	secs := secsFromTTL(sh.CacheTTL)
	stmt := s.table.INSERT(s.table.AllColumns).
		VALUES(
			sh.ID, sh.OrgID, sh.ProjectID, sh.SpreadsheetID,
			sh.Tab, sh.Slug, string(sh.Visibility), secs, sh.Writable,
			sh.CreatedAt.UTC(), sh.UpdatedAt.UTC(),
			sh.Generation, pgNullableTime(sh.ValidatedAt), sh.ContractOK, sh.ContractReason,
			sh.SoftDelete, sh.ContentDigest, headerColumns,
		).
		ON_CONFLICT(s.table.ID).
		// SECURITY: the conflict clause carries the tenant predicate; without it an attacker-supplied row id updates another org's row.
		DO_UPDATE(
			postgres.SET(
				s.table.Tab.SET(postgres.String(sh.Tab)),
				s.table.Slug.SET(postgres.String(sh.Slug)),
				s.table.Visibility.SET(postgres.String(string(sh.Visibility))),
				s.table.CacheTTLSecs.SET(postgres.Int64(secs)),
				s.table.Writable.SET(postgres.Bool(sh.Writable)),
				s.table.UpdatedAt.SET(postgres.TimestampzT(sh.UpdatedAt.UTC())),
				s.table.SoftDelete.SET(postgres.Bool(sh.SoftDelete)),
				s.table.HeaderColumns.SET(postgres.StringExp(
					postgres.CASE().
						WHEN(s.table.Tab.EQ(postgres.String(sh.Tab))).THEN(s.table.HeaderColumns).
						ELSE(postgres.String("")),
				)),
				// NOTE: a retab moves the stored generation, never the loaded one, so a refresh of the old tab fails its guard.
				s.table.Generation.SET(postgres.IntExp(
					postgres.CASE().
						WHEN(s.table.Tab.EQ(postgres.String(sh.Tab))).THEN(s.table.Generation).
						ELSE(s.table.Generation.ADD(postgres.Int(1))),
				)),
			).WHERE(s.table.OrgID.EQ(postgres.UUID(tc.OrgID))),
		)
	res, execErr := stmt.ExecContext(ctx, tx)
	if execErr != nil {
		if isPgUniqueViolation(execErr) {
			return s.endTx(tx, owned, &AlreadyExistsError{Field: "slug", Value: sh.Slug})
		}
		return s.endTx(tx, owned, fmt.Errorf("sheet.postgres.Save: %w", execErr))
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("sheet.postgres.Save: rows affected: %w", raErr))
	}
	if n == 0 {
		return s.endTx(tx, owned, &NotFoundError{ID: sh.ID.String()})
	}
	return s.endTx(tx, owned, nil)
}

func (s *postgresStore) Delete(ctx context.Context, id uuid.UUID) error {
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	// SECURITY: org predicate, not RLS alone — a BYPASSRLS role would otherwise delete another org's row.
	stmt := s.table.DELETE().WHERE(s.table.ID.EQ(postgres.UUID(id)).
		AND(s.table.OrgID.EQ(postgres.UUID(tc.OrgID))))
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
