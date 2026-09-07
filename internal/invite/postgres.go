package invite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"

	pdb "altalune.id/template/internal/platform/db"
	pgent "altalune.id/template/internal/platform/db/entity/postgres"
	"altalune.id/template/internal/platform/tenant"
)

type postgresStore struct {
	pool               pdb.Pool
	pc                 *tenant.PgConn
	table              *pgent.Invites
	byTokenHashStmt    string
	pendingByEmailStmt string
}

func newPostgresStore(pool pdb.Pool, pc *tenant.PgConn, schema, tablePrefix string) *postgresStore {
	if schema == "" {
		schema = "public"
	}
	cols := `i.id AS "invites.id", i.org_id AS "invites.org_id", i.email AS "invites.email", ` +
		`i.role AS "invites.role", i.token_hash AS "invites.token_hash", ` +
		`i.expires_at AS "invites.expires_at", i.accepted_at AS "invites.accepted_at", ` +
		`i.created_at AS "invites.created_at"`
	fn := schema + "." + tablePrefix
	return &postgresStore{
		pool:  pool,
		pc:    pc,
		table: pgent.NewInvites(schema, tablePrefix),
		// NOTE: RawStatement because go-jet has no builder for a set-returning function in FROM position.
		byTokenHashStmt:    "SELECT " + cols + " FROM " + fn + "resolve_invite_by_token_hash(#hash) i",
		pendingByEmailStmt: "SELECT " + cols + " FROM " + fn + "list_pending_invites_for_email(#email) i",
	}
}

type pgInviteRow struct {
	ID         uuid.UUID  `alias:"invites.id"`
	OrgID      uuid.UUID  `alias:"invites.org_id"`
	Email      string     `alias:"invites.email"`
	Role       string     `alias:"invites.role"`
	TokenHash  string     `alias:"invites.token_hash"`
	ExpiresAt  time.Time  `alias:"invites.expires_at"`
	AcceptedAt *time.Time `alias:"invites.accepted_at"`
	CreatedAt  time.Time  `alias:"invites.created_at"`
}

func (r *pgInviteRow) toInvite() *Invite {
	inv := &Invite{
		ID:        r.ID,
		OrgID:     r.OrgID,
		Email:     r.Email,
		Role:      Role(r.Role),
		TokenHash: r.TokenHash,
		ExpiresAt: r.ExpiresAt,
		CreatedAt: r.CreatedAt,
	}
	if r.AcceptedAt != nil {
		t := r.AcceptedAt.UTC()
		inv.UsedAt = &t
	}
	return inv
}

func (s *postgresStore) txAcquire(ctx context.Context) (*sql.Tx, bool, tenant.Context, error) {
	if tx, ok := pdb.CurrentTx(ctx); ok {
		tc, _ := tenant.From(ctx)
		return tx, false, tc, nil
	}
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, false, tenant.Context{}, err
	}
	tx, err := s.pc.BeginTenanted(ctx, tc)
	if err != nil {
		return nil, false, tenant.Context{}, fmt.Errorf("invite.postgres: begin: %w", err)
	}
	return tx, true, tc, nil
}

func (s *postgresStore) endTx(tx *sql.Tx, owned bool, err error) error {
	if !owned {
		return err
	}
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if cerr := tx.Commit(); cerr != nil {
		return fmt.Errorf("invite.postgres: commit: %w", cerr)
	}
	return nil
}

func (s *postgresStore) queryOne(ctx context.Context, execer qrm.DB, cond postgres.BoolExpression, notFound *NotFoundError) (*Invite, error) {
	stmt := postgres.SELECT(s.table.AllColumns).
		FROM(s.table).
		WHERE(cond).
		LIMIT(1)
	var row pgInviteRow
	if err := stmt.QueryContext(ctx, execer, &row); err != nil {
		if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return nil, notFound
		}
		return nil, fmt.Errorf("invite.postgres.query: %w", err)
	}
	return row.toInvite(), nil
}

func pgNullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}

func pgNullableTimeExpr(t *time.Time) postgres.TimestampzExpression {
	if t == nil {
		return postgres.TimestampzExp(postgres.NULL)
	}
	return postgres.TimestampzT(t.UTC())
}
