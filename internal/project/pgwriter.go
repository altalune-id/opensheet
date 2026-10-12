package project

import (
	"context"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/postgres"
)

func (s *postgresStore) Save(ctx context.Context, p *Project) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	stmt := s.table.INSERT(s.table.AllColumns).
		VALUES(
			p.ID, p.OrgID, p.Slug, p.Name, tc.UserID,
			p.CreatedAt.UTC(), now, p.System,
		).
		ON_CONFLICT(s.table.ID).
		// SECURITY: the conflict clause carries the tenant predicate; without it an attacker-supplied row id updates another org's row.
		DO_UPDATE(
			postgres.SET(
				s.table.Name.SET(postgres.String(p.Name)),
				s.table.UpdatedAt.SET(postgres.TimestampzT(now)),
				s.table.System.SET(postgres.Bool(p.System)),
			).WHERE(s.table.OrgID.EQ(postgres.UUID(tc.OrgID))),
		)
	res, execErr := stmt.ExecContext(ctx, tx)
	if execErr != nil {
		if isPgOneSystemViolation(execErr) {
			return s.endTx(tx, owned, &SystemProjectExistsError{OrgID: p.OrgID.String(), Slug: p.Slug})
		}
		if isPgUniqueViolation(execErr) {
			return s.endTx(tx, owned, &AlreadyExistsError{Field: "slug", Value: p.Slug})
		}
		return s.endTx(tx, owned, fmt.Errorf("project.postgres.Save: %w", execErr))
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("project.postgres.Save: rows affected: %w", raErr))
	}
	if n == 0 {
		return s.endTx(tx, owned, &NotFoundError{ID: p.ID.String()})
	}
	return s.endTx(tx, owned, nil)
}
