package todo

import (
	"context"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/google/uuid"

	"altalune.id/template/internal/platform/tenant"
)

func (s *postgresStore) Save(ctx context.Context, t *Todo) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	stmt := s.table.INSERT(s.table.AllColumns).
		VALUES(
			t.ID, t.OrgID, t.ProjectID, tc.UserID, t.Title, t.Done,
			t.CreatedAt.UTC(), t.UpdatedAt.UTC(),
		).
		ON_CONFLICT(s.table.ID).
		DO_UPDATE(
			postgres.SET(
				s.table.Title.SET(postgres.String(t.Title)),
				s.table.Done.SET(postgres.Bool(t.Done)),
				s.table.UpdatedAt.SET(postgres.TimestampzT(t.UpdatedAt.UTC())),
			),
		)
	if _, execErr := stmt.ExecContext(ctx, tx); execErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("todo.postgres.Save: %w", execErr))
	}
	return s.endTx(tx, owned, nil)
}

func (s *postgresStore) Delete(ctx context.Context, id uuid.UUID) error {
	tx, owned, _, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	stmt := s.table.DELETE().WHERE(s.table.ID.EQ(postgres.UUID(id)))
	res, execErr := stmt.ExecContext(ctx, tx)
	if execErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("todo.postgres.Delete: %w", execErr))
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("todo.postgres.Delete: rows affected: %w", raErr))
	}
	if n == 0 {
		return s.endTx(tx, owned, &NotFoundError{ID: id.String()})
	}
	return s.endTx(tx, owned, nil)
}

func (s *postgresStore) ClearDone(ctx context.Context, orgID, projectID uuid.UUID) (int, error) {
	tx, owned, _, err := s.txAcquire(ctx)
	if err != nil {
		return 0, err
	}
	stmt := s.table.DELETE().
		WHERE(s.table.OrgID.EQ(postgres.UUID(orgID)).
			AND(s.table.ProjectID.EQ(postgres.UUID(projectID))).
			AND(s.table.Done.EQ(postgres.Bool(true))))
	res, execErr := stmt.ExecContext(ctx, tx)
	if execErr != nil {
		return 0, s.endTx(tx, owned, fmt.Errorf("todo.postgres.ClearDone: %w", execErr))
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return 0, s.endTx(tx, owned, fmt.Errorf("todo.postgres.ClearDone: rows affected: %w", raErr))
	}
	if endErr := s.endTx(tx, owned, nil); endErr != nil {
		return 0, endErr
	}
	return int(n), nil
}

// MarkDoneOlderThan marks stale open todos done in batches. NOTE: each batch commits separately, so a mid-loop failure leaves earlier batches applied; the sweep is idempotent and the next run finishes it.
func (s *postgresStore) MarkDoneOlderThan(ctx context.Context, orgID uuid.UUID, cutoff time.Time, batch int) (int, error) {
	if batch <= 0 {
		batch = SweepBatchSize
	}
	total := 0
	for {
		n, err := s.markDoneBatch(ctx, orgID, cutoff, batch)
		if err != nil {
			return total, err
		}
		total += n
		if n < batch {
			return total, nil
		}
	}
}

func (s *postgresStore) markDoneBatch(ctx context.Context, orgID uuid.UUID, cutoff time.Time, batch int) (int, error) {
	tx, err := s.pc.BeginTenanted(ctx, tenant.Context{OrgID: orgID})
	if err != nil {
		return 0, fmt.Errorf("todo.postgres.MarkDoneOlderThan: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stale := postgres.SELECT(s.table.ID).
		FROM(s.table).
		WHERE(
			s.table.OrgID.EQ(postgres.UUID(orgID)).
				AND(s.table.Done.EQ(postgres.Bool(false))).
				AND(s.table.CreatedAt.LT(postgres.TimestampzT(cutoff.UTC()))),
		).
		ORDER_BY(s.table.CreatedAt.ASC()).
		LIMIT(int64(batch))

	stmt := s.table.UPDATE(s.table.Done, s.table.UpdatedAt).
		SET(postgres.Bool(true), postgres.TimestampzT(time.Now().UTC())).
		WHERE(s.table.ID.IN(stale))

	res, err := stmt.ExecContext(ctx, tx)
	if err != nil {
		return 0, fmt.Errorf("todo.postgres.MarkDoneOlderThan: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("todo.postgres.MarkDoneOlderThan: rows affected: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("todo.postgres.MarkDoneOlderThan: commit: %w", err)
	}
	return int(n), nil
}
