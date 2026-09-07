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
		DO_UPDATE(
			postgres.SET(
				s.table.Name.SET(postgres.String(p.Name)),
				s.table.UpdatedAt.SET(postgres.TimestampzT(now)),
				s.table.System.SET(postgres.Bool(p.System)),
			),
		)
	if _, execErr := stmt.ExecContext(ctx, tx); execErr != nil {
		if isPgUniqueViolation(execErr) {
			return s.endTx(tx, owned, &AlreadyExistsError{Field: "slug", Value: p.Slug})
		}
		return s.endTx(tx, owned, fmt.Errorf("project.postgres.Save: %w", execErr))
	}
	return s.endTx(tx, owned, nil)
}
