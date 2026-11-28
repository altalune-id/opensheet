package invite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"

	pdb "altalune.id/opensheet/internal/platform/db"
	pgent "altalune.id/opensheet/internal/platform/db/entity/postgres"
	"altalune.id/opensheet/internal/platform/tenant"
)

type postgresStore struct {
	pool               pdb.Pool
	pc                 *tenant.PgConn
	table              *pgent.Invites
	grants             *pgent.InviteProjects
	grantsExpr         postgres.Expression
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
		`i.created_at AS "invites.created_at", i.role_id AS "invites.role_id", ` +
		`i.all_projects AS "invites.all_projects", i.project_ids AS "invites.project_ids"`
	fn := schema + "." + tablePrefix
	return &postgresStore{
		pool:   pool,
		pc:     pc,
		table:  pgent.NewInvites(schema, tablePrefix),
		grants: pgent.NewInviteProjects(schema, tablePrefix),
		// NOTE: Raw because go-jet has no builder for a correlated array_agg subquery in a projection.
		grantsExpr: postgres.Raw("COALESCE((SELECT array_agg(g.project_id ORDER BY g.created_at, g.project_id) FROM " + fn +
			"invite_projects g WHERE g.org_id = invites.org_id AND g.invite_id = invites.id), '{}')::text"),
		// NOTE: RawStatement because go-jet has no builder for a set-returning function in FROM position.
		byTokenHashStmt: "SELECT " + cols + " FROM " + fn + "resolve_invite_by_token_hash(#hash) i",
		// NOTE: a SELECT without ORDER BY has no guaranteed row order, whatever ordering the wrapper body carries.
		pendingByEmailStmt: "SELECT " + cols + " FROM " + fn + "list_pending_invites_for_email(#email) i" +
			" ORDER BY i.created_at ASC, i.id ASC",
	}
}

type pgInviteRow struct {
	ID          uuid.UUID  `alias:"invites.id"`
	OrgID       uuid.UUID  `alias:"invites.org_id"`
	Email       string     `alias:"invites.email"`
	Role        string     `alias:"invites.role"`
	TokenHash   string     `alias:"invites.token_hash"`
	ExpiresAt   time.Time  `alias:"invites.expires_at"`
	AcceptedAt  *time.Time `alias:"invites.accepted_at"`
	CreatedAt   time.Time  `alias:"invites.created_at"`
	RoleID      *uuid.UUID `alias:"invites.role_id"`
	AllProjects bool       `alias:"invites.all_projects"`
	ProjectIDs  string     `alias:"invites.project_ids"`
}

func (r *pgInviteRow) toInvite() (*Invite, error) {
	projectIDs, err := parsePgUUIDArray(r.ProjectIDs)
	if err != nil {
		return nil, fmt.Errorf("invite.postgres: parse project_ids: %w", err)
	}
	inv := &Invite{
		ID:          r.ID,
		OrgID:       r.OrgID,
		Email:       r.Email,
		Role:        Role(r.Role),
		TokenHash:   r.TokenHash,
		ExpiresAt:   r.ExpiresAt.UTC(),
		CreatedAt:   r.CreatedAt.UTC(),
		AllProjects: r.AllProjects,
		ProjectIDs:  projectIDs,
	}
	if r.AcceptedAt != nil {
		t := r.AcceptedAt.UTC()
		inv.UsedAt = &t
	}
	if r.RoleID != nil {
		inv.RoleID = *r.RoleID
	}
	return inv, nil
}

func parsePgUUIDArray(raw string) ([]uuid.UUID, error) {
	body := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(raw), "{"), "}")
	if body == "" {
		return []uuid.UUID{}, nil
	}
	parts := strings.Split(body, ",")
	out := make([]uuid.UUID, 0, len(parts))
	for _, p := range parts {
		id, err := uuid.Parse(p)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
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
	stmt := postgres.SELECT(s.table.AllColumns, s.grantsExpr.AS("invites.project_ids")).
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
	return row.toInvite()
}

func pgNullableUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

func pgRoleIDExpr(id uuid.UUID) postgres.StringExpression {
	if id == uuid.Nil {
		return pgent.NullUUID()
	}
	return postgres.UUID(id)
}

func pgNullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}

func pgNullableTimeExpr(t *time.Time) postgres.TimestampzExpression {
	if t == nil {
		return pgent.NullTimestampz()
	}
	return postgres.TimestampzT(t.UTC())
}
