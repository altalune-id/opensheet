package invite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/google/uuid"

	pdb "altalune.id/opensheet/internal/platform/db"
)

func (s *postgresStore) Save(ctx context.Context, i *Invite) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	var existing []struct {
		ID uuid.UUID `alias:"invites.id"`
	}
	if qErr := postgres.SELECT(s.table.ID).FROM(s.table).WHERE(s.table.ID.EQ(postgres.UUID(i.ID))).QueryContext(ctx, tx, &existing); qErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("invite.postgres.Save: lookup: %w", qErr))
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
			pgNullableUUID(i.RoleID),
			i.AllProjects,
		).
		ON_CONFLICT(s.table.ID).
		// SECURITY: the conflict clause carries the tenant predicate; without it an attacker-supplied row id updates another org's row.
		DO_UPDATE(postgres.SET(
			s.table.Email.SET(postgres.String(i.Email)),
			s.table.Role.SET(postgres.String(string(i.Role))),
			s.table.RoleID.SET(pgRoleIDExpr(i.RoleID)),
			s.table.TokenHash.SET(postgres.String(i.TokenHash)),
			s.table.ExpiresAt.SET(postgres.TimestampzT(i.ExpiresAt.UTC())),
			s.table.AcceptedAt.SET(pgNullableTimeExpr(i.UsedAt)),
		).WHERE(s.table.OrgID.EQ(postgres.UUID(tc.OrgID))))
	res, execErr := stmt.ExecContext(ctx, tx)
	// NOTE: the conflict clause's org guard refuses with zero rows, or with a row-level security refusal on newer Postgres.
	if pdb.IsRowSecurityViolation(execErr) {
		return s.endTx(tx, owned, &NotFoundError{ID: i.ID.String()})
	}
	if execErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("invite.postgres.Save: %w", execErr))
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("invite.postgres.Save: rows affected: %w", raErr))
	}
	if n == 0 {
		return s.endTx(tx, owned, &NotFoundError{ID: i.ID.String()})
	}
	if len(existing) > 0 {
		return s.endTx(tx, owned, nil)
	}
	return s.endTx(tx, owned, s.addGrants(ctx, tx, tc.OrgID, i))
}

// NOTE: a grant is written with the invite only; a later Save, such as the one that accepts it, never changes it.
func (s *postgresStore) addGrants(ctx context.Context, tx *sql.Tx, orgID uuid.UUID, i *Invite) error {
	if len(i.ProjectIDs) == 0 {
		return nil
	}
	now := time.Now().UTC()
	ins := s.grants.INSERT(s.grants.AllColumns)
	for _, id := range i.ProjectIDs {
		ins = ins.VALUES(orgID, i.ID, id, now)
	}
	if _, err := ins.ON_CONFLICT(s.grants.InviteID, s.grants.ProjectID).DO_NOTHING().ExecContext(ctx, tx); err != nil {
		return fmt.Errorf("invite.postgres.Save: add grants: %w", err)
	}
	return nil
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
