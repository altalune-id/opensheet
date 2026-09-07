package invite

import (
	"context"
	"fmt"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"

	pdb "altalune.id/template/internal/platform/db"
)

func (s *postgresStore) ByID(ctx context.Context, id uuid.UUID) (*Invite, error) {
	tx, owned, _, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	return s.queryOne(ctx, tx, s.table.ID.EQ(postgres.UUID(id)), &NotFoundError{ID: id.String()})
}

// SECURITY: resolves a token before any tenant scope exists; the SECURITY DEFINER wrapper lifts RLS, and the token hash is unguessable.
func (s *postgresStore) ByTokenHash(ctx context.Context, hash string) (*Invite, error) {
	rows, err := s.scanWrapped(ctx, s.byTokenHashStmt, postgres.RawArgs{"#hash": hash})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, &NotFoundError{}
	}
	return rows[0], nil
}

func (s *postgresStore) scanWrapped(ctx context.Context, rawQuery string, args postgres.RawArgs) ([]*Invite, error) {
	execer := qrm.DB(s.pc.DB)
	if tx, ok := pdb.CurrentTx(ctx); ok {
		execer = tx
	}
	var rows []pgInviteRow
	if err := postgres.RawStatement(rawQuery, args).QueryContext(ctx, execer, &rows); err != nil {
		return nil, fmt.Errorf("invite.postgres: %w", err)
	}
	out := make([]*Invite, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].toInvite())
	}
	return out, nil
}

func (s *postgresStore) ListPending(ctx context.Context, orgID uuid.UUID) ([]*Invite, error) {
	tx, owned, _, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	stmt := postgres.SELECT(s.table.AllColumns).
		FROM(s.table).
		WHERE(s.table.OrgID.EQ(postgres.UUID(orgID)).
			AND(s.table.AcceptedAt.IS_NULL())).
		ORDER_BY(s.table.CreatedAt.ASC())
	var rows []pgInviteRow
	if qErr := stmt.QueryContext(ctx, tx, &rows); qErr != nil {
		return nil, fmt.Errorf("invite.postgres.ListPending: %w", qErr)
	}
	out := make([]*Invite, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].toInvite())
	}
	return out, nil
}

// SECURITY: lists invites across orgs to route invited signups before any tenant scope exists; the SECURITY DEFINER wrapper lifts RLS, and the email is the caller's own.
func (s *postgresStore) FindPendingForEmail(ctx context.Context, email string) ([]*Invite, error) {
	return s.scanWrapped(ctx, s.pendingByEmailStmt, postgres.RawArgs{"#email": email})
}
