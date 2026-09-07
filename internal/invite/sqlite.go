package invite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/qrm"
	"github.com/go-jet/jet/v2/sqlite"
	"github.com/google/uuid"

	sqliteent "altalune.id/template/internal/platform/db/entity/sqlite"
	"altalune.id/template/internal/platform/tenant"
)

type sqliteStore struct {
	db    *sql.DB
	table *sqliteent.Invites
}

func newSQLiteStore(db *sql.DB, tablePrefix string) *sqliteStore {
	return &sqliteStore{db: db, table: sqliteent.NewInvites(tablePrefix)}
}

type sqliteInviteRow struct {
	ID         string  `alias:"invites.id"`
	OrgID      string  `alias:"invites.org_id"`
	Email      string  `alias:"invites.email"`
	Role       string  `alias:"invites.role"`
	TokenHash  string  `alias:"invites.token_hash"`
	ExpiresAt  string  `alias:"invites.expires_at"`
	AcceptedAt *string `alias:"invites.accepted_at"`
	CreatedAt  string  `alias:"invites.created_at"`
}

func (r *sqliteInviteRow) toInvite() (*Invite, error) {
	id, err := uuid.Parse(r.ID)
	if err != nil {
		return nil, fmt.Errorf("invite.sqlite: parse id: %w", err)
	}
	oid, err := uuid.Parse(r.OrgID)
	if err != nil {
		return nil, fmt.Errorf("invite.sqlite: parse org_id: %w", err)
	}
	exp, err := time.Parse(time.RFC3339Nano, r.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("invite.sqlite: parse expires_at: %w", err)
	}
	ca, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("invite.sqlite: parse created_at: %w", err)
	}
	inv := &Invite{
		ID:        id,
		OrgID:     oid,
		Email:     r.Email,
		Role:      Role(r.Role),
		TokenHash: r.TokenHash,
		ExpiresAt: exp,
		CreatedAt: ca,
	}
	if r.AcceptedAt != nil && *r.AcceptedAt != "" {
		t, err := time.Parse(time.RFC3339Nano, *r.AcceptedAt)
		if err != nil {
			return nil, fmt.Errorf("invite.sqlite: parse accepted_at: %w", err)
		}
		inv.UsedAt = &t
	}
	return inv, nil
}

func (s *sqliteStore) Save(ctx context.Context, i *Invite) error {
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	stmt := s.table.INSERT(s.table.AllColumns).
		VALUES(
			i.ID.String(),
			i.OrgID.String(),
			i.Email,
			string(i.Role),
			i.TokenHash,
			i.ExpiresAt.UTC().Format(time.RFC3339Nano),
			sqliteNullableTimeArg(i.UsedAt),
			tc.UserID.String(),
			i.CreatedAt.UTC().Format(time.RFC3339Nano),
		).
		ON_CONFLICT(s.table.ID).
		DO_UPDATE(sqlite.SET(
			s.table.Email.SET(sqlite.String(i.Email)),
			s.table.Role.SET(sqlite.String(string(i.Role))),
			s.table.TokenHash.SET(sqlite.String(i.TokenHash)),
			s.table.ExpiresAt.SET(sqlite.String(i.ExpiresAt.UTC().Format(time.RFC3339Nano))),
			s.table.AcceptedAt.SET(sqliteNullableTimeExpr(i.UsedAt)),
		))
	if _, err := stmt.ExecContext(ctx, s.db); err != nil {
		return fmt.Errorf("invite.sqlite.Save: %w", err)
	}
	return nil
}

func (s *sqliteStore) ByID(ctx context.Context, id uuid.UUID) (*Invite, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	return s.queryOne(ctx,
		s.table.ID.EQ(sqlite.String(id.String())).
			AND(s.table.OrgID.EQ(sqlite.String(tc.OrgID.String()))),
		&NotFoundError{ID: id.String()},
	)
}

// SECURITY: unauthenticated lookup; the hash is unguessable and every follow-up action re-checks org membership.
func (s *sqliteStore) ByTokenHash(ctx context.Context, hash string) (*Invite, error) {
	return s.queryOne(ctx,
		s.table.TokenHash.EQ(sqlite.String(hash)),
		&NotFoundError{},
	)
}

func (s *sqliteStore) ListPending(ctx context.Context, orgID uuid.UUID) ([]*Invite, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	if tc.OrgID != orgID {
		return []*Invite{}, nil
	}
	stmt := sqlite.SELECT(s.table.AllColumns).
		FROM(s.table).
		WHERE(s.table.OrgID.EQ(sqlite.String(orgID.String())).
			AND(s.table.AcceptedAt.IS_NULL())).
		ORDER_BY(s.table.CreatedAt.ASC())
	var rows []sqliteInviteRow
	if err := stmt.QueryContext(ctx, s.db, &rows); err != nil {
		return nil, fmt.Errorf("invite.sqlite.ListPending: %w", err)
	}
	out := make([]*Invite, 0, len(rows))
	for i := range rows {
		inv, err := rows[i].toInvite()
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, nil
}

// SECURITY: cross-tenant lookup by email is required to route invited OIDC signups; the email is scoped to the caller's own identity.
func (s *sqliteStore) FindPendingForEmail(ctx context.Context, email string) ([]*Invite, error) {
	stmt := sqlite.SELECT(s.table.AllColumns).
		FROM(s.table).
		WHERE(s.table.Email.EQ(sqlite.String(email)).
			AND(s.table.AcceptedAt.IS_NULL())).
		ORDER_BY(s.table.CreatedAt.ASC())
	var rows []sqliteInviteRow
	if err := stmt.QueryContext(ctx, s.db, &rows); err != nil {
		return nil, fmt.Errorf("invite.sqlite.FindPendingForEmail: %w", err)
	}
	out := make([]*Invite, 0, len(rows))
	for i := range rows {
		inv, err := rows[i].toInvite()
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, nil
}

func (s *sqliteStore) Delete(ctx context.Context, id uuid.UUID) error {
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	stmt := s.table.DELETE().
		WHERE(s.table.ID.EQ(sqlite.String(id.String())).
			AND(s.table.OrgID.EQ(sqlite.String(tc.OrgID.String()))))
	res, err := stmt.ExecContext(ctx, s.db)
	if err != nil {
		return fmt.Errorf("invite.sqlite.Delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("invite.sqlite.Delete: rows affected: %w", err)
	}
	if n == 0 {
		return &NotFoundError{ID: id.String()}
	}
	return nil
}

func (s *sqliteStore) queryOne(ctx context.Context, cond sqlite.BoolExpression, notFound *NotFoundError) (*Invite, error) {
	stmt := sqlite.SELECT(s.table.AllColumns).
		FROM(s.table).
		WHERE(cond).
		LIMIT(1)
	var row sqliteInviteRow
	if err := stmt.QueryContext(ctx, s.db, &row); err != nil {
		if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return nil, notFound
		}
		return nil, fmt.Errorf("invite.sqlite.query: %w", err)
	}
	return row.toInvite()
}

func sqliteNullableTimeArg(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func sqliteNullableTimeExpr(t *time.Time) sqlite.StringExpression {
	if t == nil {
		return sqlite.StringExp(sqlite.NULL)
	}
	return sqlite.String(t.UTC().Format(time.RFC3339Nano))
}
