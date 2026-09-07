package invite

import (
	"context"
	"fmt"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/google/uuid"
)

func (s *postgresStore) Save(ctx context.Context, i *Invite) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	stmt := s.table.INSERT(s.table.AllColumns).
		VALUES(
			i.ID,
			i.OrgID,
			i.Email,
			string(i.Role),
			i.TokenHash,
			i.ExpiresAt.UTC(),
			pgNullableTime(i.UsedAt),
			tc.UserID,
			i.CreatedAt.UTC(),
		).
		ON_CONFLICT(s.table.ID).
		DO_UPDATE(postgres.SET(
			s.table.Email.SET(postgres.String(i.Email)),
			s.table.Role.SET(postgres.String(string(i.Role))),
			s.table.TokenHash.SET(postgres.String(i.TokenHash)),
			s.table.ExpiresAt.SET(postgres.TimestampzT(i.ExpiresAt.UTC())),
			s.table.AcceptedAt.SET(pgNullableTimeExpr(i.UsedAt)),
		))
	if _, execErr := stmt.ExecContext(ctx, tx); execErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("invite.postgres.Save: %w", execErr))
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
		return s.endTx(tx, owned, fmt.Errorf("invite.postgres.Delete: %w", execErr))
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("invite.postgres.Delete: rows affected: %w", raErr))
	}
	if n == 0 {
		return s.endTx(tx, owned, &NotFoundError{ID: id.String()})
	}
	return s.endTx(tx, owned, nil)
}
