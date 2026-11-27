package role

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
	"github.com/jackc/pgx/v5/pgconn"

	pdb "altalune.id/opensheet/internal/platform/db"
	pgent "altalune.id/opensheet/internal/platform/db/entity/postgres"
	"altalune.id/opensheet/internal/platform/tenant"
)

type postgresStore struct {
	pc      *tenant.PgConn
	table   *pgent.Roles
	members string
	invites string
}

func newPostgresStore(pc *tenant.PgConn, schema, tablePrefix string) *postgresStore {
	if schema == "" {
		schema = "public"
	}
	return &postgresStore{pc: pc, table: pgent.NewRoles(schema, tablePrefix),
		members: schema + "." + tablePrefix + "memberships", invites: schema + "." + tablePrefix + "invites"}
}

type pgRoleRow struct {
	ID          uuid.UUID `alias:"roles.id"`
	OrgID       uuid.UUID `alias:"roles.org_id"`
	Name        string    `alias:"roles.name"`
	Description string    `alias:"roles.description"`
	Permissions string    `alias:"roles.permissions"`
	CreatedAt   time.Time `alias:"roles.created_at"`
	UpdatedAt   time.Time `alias:"roles.updated_at"`
	Version     int64     `alias:"roles.version"`
}

func (r *pgRoleRow) toRole() *Role {
	return &Role{
		ID: r.ID, OrgID: r.OrgID, Name: r.Name, Description: r.Description,
		Permissions: parsePgPermissions(r.Permissions),
		CreatedAt:   r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(), Version: r.Version,
	}
}

func (s *postgresStore) columns() postgres.ProjectionList {
	t := s.table
	return postgres.ProjectionList{t.ID, t.OrgID, t.Name, t.Description,
		postgres.CAST(t.Permissions).AS_TEXT().AS("roles.permissions"), t.CreatedAt, t.UpdatedAt, t.Version}
}

func (s *postgresStore) Save(ctx context.Context, r *Role, ifVersion int64) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	perms := pgPermissionsExpr(r.Permissions)
	// SECURITY: the conflict clause carries the tenant predicate, so a Save naming another org's role id updates nothing.
	version := postgres.Bool(true)
	if ifVersion != 0 {
		version = s.table.Version.EQ(postgres.Int(ifVersion))
	}
	stmt := s.table.INSERT(s.table.AllColumns).
		VALUES(r.ID, tc.OrgID, r.Name, r.Description, perms, r.CreatedAt.UTC(), r.UpdatedAt.UTC(), r.Version).
		ON_CONFLICT(s.table.ID).
		DO_UPDATE(postgres.SET(
			s.table.Name.SET(postgres.String(r.Name)),
			s.table.Description.SET(postgres.String(r.Description)),
			s.table.Permissions.SET(perms),
			s.table.UpdatedAt.SET(postgres.TimestampzT(r.UpdatedAt.UTC())),
			s.table.Version.SET(s.table.Version.ADD(postgres.Int(1))),
		).WHERE(s.table.OrgID.EQ(postgres.UUID(tc.OrgID)).AND(version)))
	res, err := stmt.ExecContext(ctx, tx)
	if pdb.IsRowSecurityViolation(err) {
		return s.endTx(tx, owned, &NotFoundError{ID: r.ID.String()})
	}
	if err != nil {
		if pgCode(err) == "23505" {
			return s.endTx(tx, owned, &AlreadyExistsError{Name: r.Name})
		}
		return s.endTx(tx, owned, fmt.Errorf("role.postgres.Save: %w", err))
	}
	n, err := res.RowsAffected()
	if err != nil {
		return s.endTx(tx, owned, fmt.Errorf("role.postgres.Save: rows affected: %w", err))
	}
	if n == 0 {
		return s.endTx(tx, owned, s.refused(ctx, tx, r.ID, ifVersion))
	}
	return s.endTx(tx, owned, nil)
}

func (s *postgresStore) refused(ctx context.Context, tx *sql.Tx, id uuid.UUID, ifVersion int64) error {
	cur, err := s.byID(ctx, tx, id)
	if err != nil {
		return err
	}
	return &StaleVersionError{Want: ifVersion, Got: cur.Version}
}

func (s *postgresStore) ByID(ctx context.Context, id uuid.UUID) (*Role, error) {
	tx, owned, _, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	r, err := s.byID(ctx, tx, id)
	return r, s.endTx(tx, owned, err)
}

func (s *postgresStore) byID(ctx context.Context, tx *sql.Tx, id uuid.UUID) (*Role, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	var row pgRoleRow
	err = postgres.SELECT(s.columns()).FROM(s.table).
		WHERE(s.table.ID.EQ(postgres.UUID(id)).AND(s.table.OrgID.EQ(postgres.UUID(tc.OrgID)))).
		LIMIT(1).QueryContext(ctx, tx, &row)
	if errors.Is(err, qrm.ErrNoRows) {
		return nil, &NotFoundError{ID: id.String()}
	}
	if err != nil {
		return nil, fmt.Errorf("role.postgres.ByID: %w", err)
	}
	return row.toRole(), nil
}

func (s *postgresStore) List(ctx context.Context) ([]*Role, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	var rows []pgRoleRow
	err = postgres.SELECT(s.columns()).FROM(s.table).
		WHERE(s.table.OrgID.EQ(postgres.UUID(tc.OrgID))).
		ORDER_BY(postgres.LOWER(s.table.Name).ASC(), s.table.ID.ASC()).
		QueryContext(ctx, tx, &rows)
	if err != nil && !errors.Is(err, qrm.ErrNoRows) {
		return nil, s.endTx(tx, owned, fmt.Errorf("role.postgres.List: %w", err))
	}
	out := make([]*Role, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].toRole())
	}
	return out, s.endTx(tx, owned, nil)
}

func (s *postgresStore) Delete(ctx context.Context, id uuid.UUID) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM "+s.invites+" WHERE org_id = $1 AND role_id = $2 AND accepted_at IS NOT NULL", tc.OrgID, id); err != nil { //nolint:gosec // G202: the configured table name with fixed SQL, never input
		return s.endTx(tx, owned, fmt.Errorf("role.postgres.Delete: accepted invites: %w", err))
	}
	res, err := s.table.DELETE().
		WHERE(s.table.ID.EQ(postgres.UUID(id)).AND(s.table.OrgID.EQ(postgres.UUID(tc.OrgID)))).
		ExecContext(ctx, tx)
	if err != nil {
		if pgCode(err) == "23503" {
			return s.endTx(tx, owned, &InUseError{ID: id.String()})
		}
		return s.endTx(tx, owned, fmt.Errorf("role.postgres.Delete: %w", err))
	}
	n, err := res.RowsAffected()
	if err != nil {
		return s.endTx(tx, owned, fmt.Errorf("role.postgres.Delete: rows affected: %w", err))
	}
	if n == 0 {
		return s.endTx(tx, owned, &NotFoundError{ID: id.String()})
	}
	return s.endTx(tx, owned, nil)
}

func (s *postgresStore) HeldBy(ctx context.Context) (map[uuid.UUID]int, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	q := "SELECT role_id, count(*) FROM (" + //nolint:gosec // G202: q joins the configured table names with fixed SQL, never input
		"SELECT role_id FROM " + s.members + " WHERE org_id = $1 AND role_id IS NOT NULL " +
		"UNION ALL SELECT role_id FROM " + s.invites + " WHERE org_id = $1 AND role_id IS NOT NULL AND accepted_at IS NULL" +
		") h GROUP BY role_id"
	rows, err := tx.QueryContext(ctx, q, tc.OrgID)
	if err != nil {
		return nil, s.endTx(tx, owned, fmt.Errorf("role.postgres.HeldBy: %w", err))
	}
	out, err := scanHeld(rows)
	return out, s.endTx(tx, owned, err)
}

func (s *postgresStore) Permissions(ctx context.Context, roleID uuid.UUID) ([]Permission, error) {
	r, err := s.ByID(ctx, roleID)
	if err != nil {
		return nil, err
	}
	return r.Permissions, nil
}

func (s *postgresStore) txAcquire(ctx context.Context) (*sql.Tx, bool, tenant.Context, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, false, tenant.Context{}, err
	}
	if tx, ok := pdb.CurrentTx(ctx); ok {
		return tx, false, tc, nil
	}
	tx, err := s.pc.BeginTenanted(ctx, tc)
	if err != nil {
		return nil, false, tenant.Context{}, fmt.Errorf("role.postgres: begin: %w", err)
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
		return fmt.Errorf("role.postgres: commit: %w", cerr)
	}
	return nil
}

func pgCode(err error) string {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Code
	}
	return ""
}

// NOTE: the cast is explicit because Postgres has no assignment cast from a text parameter to text[]; catalogue names hold no comma, quote or brace.
func pgPermissionsExpr(perms []Permission) postgres.StringExpression {
	parts := make([]string, len(perms))
	for i, p := range perms {
		parts[i] = string(p)
	}
	return postgres.StringExp(postgres.CAST(postgres.String("{" + strings.Join(parts, ",") + "}")).AS("text[]"))
}

func parsePgPermissions(raw string) []Permission {
	raw = strings.Trim(strings.TrimSpace(raw), "{}")
	if raw == "" {
		return []Permission{}
	}
	parts := strings.Split(raw, ",")
	out := make([]Permission, len(parts))
	for i, p := range parts {
		out[i] = Permission(strings.Trim(p, `"`))
	}
	return out
}
