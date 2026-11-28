package invite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/qrm"
	"github.com/go-jet/jet/v2/sqlite"
	"github.com/google/uuid"

	pdb "altalune.id/opensheet/internal/platform/db"
	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
	"altalune.id/opensheet/internal/platform/tenant"
)

type sqliteStore struct {
	db         *sql.DB
	table      *sqliteent.Invites
	grants     *sqliteent.InviteProjects
	grantsExpr sqlite.Expression
}

func newSQLiteStore(db *sql.DB, tablePrefix string) *sqliteStore {
	return &sqliteStore{
		db:     db,
		table:  sqliteent.NewInvites(tablePrefix),
		grants: sqliteent.NewInviteProjects(tablePrefix),
		// SECURITY: SQLite has no RLS, so the org predicate is the only thing keeping another org's grant rows out.
		grantsExpr: sqlite.Raw("COALESCE((SELECT json_group_array(o.project_id) FROM (SELECT g.project_id FROM " + tablePrefix +
			"invite_projects g WHERE g.org_id = invites.org_id AND g.invite_id = invites.id ORDER BY g.created_at, g.project_id) o), '[]')"),
	}
}

type sqliteInviteRow struct {
	ID          string  `alias:"invites.id"`
	OrgID       string  `alias:"invites.org_id"`
	Email       string  `alias:"invites.email"`
	Role        string  `alias:"invites.role"`
	TokenHash   string  `alias:"invites.token_hash"`
	ExpiresAt   string  `alias:"invites.expires_at"`
	AcceptedAt  *string `alias:"invites.accepted_at"`
	CreatedAt   string  `alias:"invites.created_at"`
	RoleID      *string `alias:"invites.role_id"`
	AllProjects bool    `alias:"invites.all_projects"`
	ProjectIDs  string  `alias:"invites.project_ids"`
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
	projectIDs := []uuid.UUID{}
	if err := json.Unmarshal([]byte(r.ProjectIDs), &projectIDs); err != nil {
		return nil, fmt.Errorf("invite.sqlite: parse project_ids: %w", err)
	}
	inv := &Invite{
		ID:          id,
		OrgID:       oid,
		Email:       r.Email,
		Role:        Role(r.Role),
		TokenHash:   r.TokenHash,
		ExpiresAt:   exp,
		CreatedAt:   ca,
		AllProjects: r.AllProjects,
		ProjectIDs:  projectIDs,
	}
	if r.AcceptedAt != nil && *r.AcceptedAt != "" {
		t, err := time.Parse(time.RFC3339Nano, *r.AcceptedAt)
		if err != nil {
			return nil, fmt.Errorf("invite.sqlite: parse accepted_at: %w", err)
		}
		inv.UsedAt = &t
	}
	if r.RoleID != nil && *r.RoleID != "" {
		if inv.RoleID, err = uuid.Parse(*r.RoleID); err != nil {
			return nil, fmt.Errorf("invite.sqlite: parse role_id: %w", err)
		}
	}
	return inv, nil
}

func (s *sqliteStore) Save(ctx context.Context, i *Invite) error {
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	return s.endTx(tx, owned, s.save(ctx, tx, tc, i))
}

func (s *sqliteStore) save(ctx context.Context, tx *sql.Tx, tc tenant.Context, i *Invite) error {
	var existing []struct {
		ID string `alias:"invites.id"`
	}
	if err := sqlite.SELECT(s.table.ID).FROM(s.table).WHERE(s.table.ID.EQ(sqlite.String(i.ID.String()))).QueryContext(ctx, tx, &existing); err != nil {
		return fmt.Errorf("invite.sqlite.Save: lookup: %w", err)
	}
	stmt := s.table.INSERT(s.table.AllColumns).
		VALUES(
			i.ID.String(),
			i.OrgID.String(),
			i.Email,
			string(i.Role),
			i.TokenHash,
			sqliteent.SQLiteTime(i.ExpiresAt),
			sqliteNullableTimeArg(i.UsedAt),
			tc.UserID.String(),
			sqliteent.SQLiteTime(i.CreatedAt),
			sqliteNullableUUID(i.RoleID),
			i.AllProjects,
		).
		ON_CONFLICT(s.table.ID).
		// SECURITY: SQLite has no RLS, so this tenant predicate is the only thing stopping an attacker-supplied row id from updating another org's row.
		DO_UPDATE(sqlite.SET(
			s.table.Email.SET(sqlite.String(i.Email)),
			s.table.Role.SET(sqlite.String(string(i.Role))),
			s.table.RoleID.SET(sqliteRoleIDExpr(i.RoleID)),
			s.table.TokenHash.SET(sqlite.String(i.TokenHash)),
			s.table.ExpiresAt.SET(sqlite.String(sqliteent.SQLiteTime(i.ExpiresAt))),
			s.table.AcceptedAt.SET(sqliteNullableTimeExpr(i.UsedAt)),
		).WHERE(s.table.OrgID.EQ(sqlite.String(tc.OrgID.String()))))
	res, err := stmt.ExecContext(ctx, tx)
	if err != nil {
		return fmt.Errorf("invite.sqlite.Save: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("invite.sqlite.Save: rows affected: %w", err)
	}
	if n == 0 {
		return &NotFoundError{ID: i.ID.String()}
	}
	if len(existing) > 0 {
		return nil
	}
	return s.addGrants(ctx, tx, tc.OrgID, i)
}

// NOTE: a grant is written with the invite only; a later Save, such as the one that accepts it, never changes it.
func (s *sqliteStore) addGrants(ctx context.Context, tx *sql.Tx, orgID uuid.UUID, i *Invite) error {
	if len(i.ProjectIDs) == 0 {
		return nil
	}
	now := sqliteent.SQLiteTime(time.Now().UTC())
	ins := s.grants.INSERT(s.grants.AllColumns)
	for _, id := range i.ProjectIDs {
		ins = ins.VALUES(orgID.String(), i.ID.String(), id.String(), now)
	}
	if _, err := ins.ON_CONFLICT(s.grants.InviteID, s.grants.ProjectID).DO_NOTHING().ExecContext(ctx, tx); err != nil {
		return fmt.Errorf("invite.sqlite.Save: add grants: %w", err)
	}
	return nil
}

// NOTE: enrolls in the caller's unit of work, so a save never opens a second SQLite writer transaction.
func (s *sqliteStore) txAcquire(ctx context.Context) (*sql.Tx, bool, error) {
	if tx, ok := pdb.CurrentTx(ctx); ok {
		return tx, false, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("invite.sqlite: begin: %w", err)
	}
	return tx, true, nil
}

func (s *sqliteStore) endTx(tx *sql.Tx, owned bool, err error) error {
	if !owned {
		return err
	}
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if cerr := tx.Commit(); cerr != nil {
		return fmt.Errorf("invite.sqlite: commit: %w", cerr)
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
	stmt := sqlite.SELECT(s.table.AllColumns, s.grantsExpr.AS("invites.project_ids")).
		FROM(s.table).
		WHERE(s.table.OrgID.EQ(sqlite.String(orgID.String())).
			AND(s.table.AcceptedAt.IS_NULL())).
		ORDER_BY(s.table.CreatedAt.ASC(), s.table.ID.ASC())
	var rows []sqliteInviteRow
	if err := stmt.QueryContext(ctx, s.conn(ctx), &rows); err != nil {
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
	stmt := sqlite.SELECT(s.table.AllColumns, s.grantsExpr.AS("invites.project_ids")).
		FROM(s.table).
		WHERE(s.table.Email.EQ(sqlite.String(email)).
			AND(s.table.AcceptedAt.IS_NULL())).
		ORDER_BY(s.table.CreatedAt.ASC(), s.table.ID.ASC())
	var rows []sqliteInviteRow
	if err := stmt.QueryContext(ctx, s.conn(ctx), &rows); err != nil {
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
	res, err := stmt.ExecContext(ctx, s.conn(ctx))
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
	stmt := sqlite.SELECT(s.table.AllColumns, s.grantsExpr.AS("invites.project_ids")).
		FROM(s.table).
		WHERE(cond).
		LIMIT(1)
	var row sqliteInviteRow
	if err := stmt.QueryContext(ctx, s.conn(ctx), &row); err != nil {
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
	return sqliteent.SQLiteTime(*t)
}

func sqliteNullableUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id.String()
}

func sqliteRoleIDExpr(id uuid.UUID) sqlite.StringExpression {
	if id == uuid.Nil {
		return sqliteent.NullText()
	}
	return sqlite.String(id.String())
}

func sqliteNullableTimeExpr(t *time.Time) sqlite.StringExpression {
	if t == nil {
		return sqliteent.NullText()
	}
	return sqlite.String(sqliteent.SQLiteTime(*t))
}

func (s *sqliteStore) conn(ctx context.Context) qrm.DB {
	if tx, ok := pdb.CurrentTx(ctx); ok {
		return tx
	}
	return s.db
}
