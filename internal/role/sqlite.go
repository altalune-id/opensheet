package role

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
	sqlitedrv "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	pdb "altalune.id/opensheet/internal/platform/db"
	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
	"altalune.id/opensheet/internal/platform/tenant"
)

type sqliteStore struct {
	db     *sql.DB
	table  *sqliteent.Roles
	prefix string
}

func newSQLiteStore(db *sql.DB, tablePrefix string) *sqliteStore {
	return &sqliteStore{db: db, table: sqliteent.NewRoles(tablePrefix), prefix: tablePrefix}
}

type sqliteRoleRow struct {
	ID          string `alias:"roles.id"`
	OrgID       string `alias:"roles.org_id"`
	Name        string `alias:"roles.name"`
	Description string `alias:"roles.description"`
	Permissions string `alias:"roles.permissions"`
	CreatedAt   string `alias:"roles.created_at"`
	UpdatedAt   string `alias:"roles.updated_at"`
	Version     int64  `alias:"roles.version"`
}

func (r *sqliteRoleRow) toRole() (*Role, error) {
	id, err := uuid.Parse(r.ID)
	if err != nil {
		return nil, fmt.Errorf("role.sqlite: parse id: %w", err)
	}
	oid, err := uuid.Parse(r.OrgID)
	if err != nil {
		return nil, fmt.Errorf("role.sqlite: parse org_id: %w", err)
	}
	var perms []Permission
	if err := json.Unmarshal([]byte(r.Permissions), &perms); err != nil {
		return nil, fmt.Errorf("role.sqlite: parse permissions: %w", err)
	}
	ca, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("role.sqlite: parse created_at: %w", err)
	}
	ua, err := time.Parse(time.RFC3339Nano, r.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("role.sqlite: parse updated_at: %w", err)
	}
	return &Role{ID: id, OrgID: oid, Name: r.Name, Description: r.Description, Permissions: perms,
		CreatedAt: ca.UTC(), UpdatedAt: ua.UTC(), Version: r.Version}, nil
}

func (s *sqliteStore) Save(ctx context.Context, r *Role, ifVersion int64) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	perms, err := json.Marshal(r.Permissions)
	if err != nil {
		return s.endTx(tx, owned, fmt.Errorf("role.sqlite.Save: encode permissions: %w", err))
	}
	// SECURITY: SQLite has no RLS, so this tenant predicate is the only thing stopping a Save naming another org's role id from updating it.
	version := sqlite.Bool(true)
	if ifVersion != 0 {
		version = s.table.Version.EQ(sqlite.Int(ifVersion))
	}
	updatedAt := sqliteent.SQLiteTime(r.UpdatedAt)
	stmt := s.table.INSERT(s.table.AllColumns).
		VALUES(r.ID.String(), tc.OrgID.String(), r.Name, r.Description, string(perms),
			sqliteent.SQLiteTime(r.CreatedAt), updatedAt, r.Version).
		ON_CONFLICT(s.table.ID).
		DO_UPDATE(sqlite.SET(
			s.table.Name.SET(sqlite.String(r.Name)),
			s.table.Description.SET(sqlite.String(r.Description)),
			s.table.Permissions.SET(sqlite.String(string(perms))),
			s.table.UpdatedAt.SET(sqlite.String(updatedAt)),
			s.table.Version.SET(s.table.Version.ADD(sqlite.Int(1))),
		).WHERE(s.table.OrgID.EQ(sqlite.String(tc.OrgID.String())).AND(version)))
	res, err := stmt.ExecContext(ctx, tx)
	if err != nil {
		if code, ok := sqliteCode(err); ok && code == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
			return s.endTx(tx, owned, &AlreadyExistsError{Name: r.Name})
		}
		return s.endTx(tx, owned, fmt.Errorf("role.sqlite.Save: %w", err))
	}
	n, err := res.RowsAffected()
	if err != nil {
		return s.endTx(tx, owned, fmt.Errorf("role.sqlite.Save: rows affected: %w", err))
	}
	if n == 0 {
		cur, err := s.byID(ctx, tx, r.ID)
		if err != nil {
			return s.endTx(tx, owned, err)
		}
		return s.endTx(tx, owned, &StaleVersionError{Want: ifVersion, Got: cur.Version})
	}
	return s.endTx(tx, owned, nil)
}

func (s *sqliteStore) ByID(ctx context.Context, id uuid.UUID) (*Role, error) {
	tx, owned, _, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	r, err := s.byID(ctx, tx, id)
	return r, s.endTx(tx, owned, err)
}

func (s *sqliteStore) byID(ctx context.Context, tx *sql.Tx, id uuid.UUID) (*Role, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	var row sqliteRoleRow
	err = sqlite.SELECT(s.table.AllColumns).FROM(s.table).
		WHERE(s.table.ID.EQ(sqlite.String(id.String())).AND(s.table.OrgID.EQ(sqlite.String(tc.OrgID.String())))).
		LIMIT(1).QueryContext(ctx, tx, &row)
	if errors.Is(err, qrm.ErrNoRows) {
		return nil, &NotFoundError{ID: id.String()}
	}
	if err != nil {
		return nil, fmt.Errorf("role.sqlite.ByID: %w", err)
	}
	return row.toRole()
}

func (s *sqliteStore) List(ctx context.Context) ([]*Role, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	var rows []sqliteRoleRow
	err = sqlite.SELECT(s.table.AllColumns).FROM(s.table).
		WHERE(s.table.OrgID.EQ(sqlite.String(tc.OrgID.String()))).
		ORDER_BY(sqlite.LOWER(s.table.Name).ASC(), s.table.ID.ASC()).
		QueryContext(ctx, tx, &rows)
	if err != nil && !errors.Is(err, qrm.ErrNoRows) {
		return nil, s.endTx(tx, owned, fmt.Errorf("role.sqlite.List: %w", err))
	}
	out := make([]*Role, 0, len(rows))
	for i := range rows {
		r, err := rows[i].toRole()
		if err != nil {
			return nil, s.endTx(tx, owned, err)
		}
		out = append(out, r)
	}
	return out, s.endTx(tx, owned, nil)
}

func (s *sqliteStore) Delete(ctx context.Context, id uuid.UUID) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM "+s.prefix+"invites WHERE org_id = ? AND role_id = ? AND accepted_at IS NOT NULL", tc.OrgID.String(), id.String()); err != nil { //nolint:gosec // G202: the configured table prefix with fixed SQL, never input
		return s.endTx(tx, owned, fmt.Errorf("role.sqlite.Delete: accepted invites: %w", err))
	}
	res, err := s.table.DELETE().
		WHERE(s.table.ID.EQ(sqlite.String(id.String())).AND(s.table.OrgID.EQ(sqlite.String(tc.OrgID.String())))).
		ExecContext(ctx, tx)
	if err != nil {
		if code, ok := sqliteCode(err); ok && (code == sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY || code == sqlite3.SQLITE_CONSTRAINT_TRIGGER) {
			return s.endTx(tx, owned, &InUseError{ID: id.String()})
		}
		return s.endTx(tx, owned, fmt.Errorf("role.sqlite.Delete: %w", err))
	}
	n, err := res.RowsAffected()
	if err != nil {
		return s.endTx(tx, owned, fmt.Errorf("role.sqlite.Delete: rows affected: %w", err))
	}
	if n == 0 {
		return s.endTx(tx, owned, &NotFoundError{ID: id.String()})
	}
	return s.endTx(tx, owned, nil)
}

func (s *sqliteStore) HeldBy(ctx context.Context) (map[uuid.UUID]int, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	q := "SELECT role_id, count(*) FROM (" + //nolint:gosec // G202: q joins the configured table prefix with fixed SQL, never input
		"SELECT role_id FROM " + s.prefix + "memberships WHERE org_id = ?1 AND role_id IS NOT NULL " +
		"UNION ALL SELECT role_id FROM " + s.prefix + "invites WHERE org_id = ?1 AND role_id IS NOT NULL AND accepted_at IS NULL" +
		") h GROUP BY role_id"
	rows, err := tx.QueryContext(ctx, q, tc.OrgID.String())
	if err != nil {
		return nil, s.endTx(tx, owned, fmt.Errorf("role.sqlite.HeldBy: %w", err))
	}
	out, err := scanHeld(rows)
	return out, s.endTx(tx, owned, err)
}

func (s *sqliteStore) Permissions(ctx context.Context, roleID uuid.UUID) ([]Permission, error) {
	r, err := s.ByID(ctx, roleID)
	if err != nil {
		return nil, err
	}
	return r.Permissions, nil
}

func (s *sqliteStore) txAcquire(ctx context.Context) (*sql.Tx, bool, tenant.Context, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, false, tenant.Context{}, err
	}
	if tx, ok := pdb.CurrentTx(ctx); ok {
		return tx, false, tc, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, tenant.Context{}, fmt.Errorf("role.sqlite: begin: %w", err)
	}
	return tx, true, tc, nil
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
		return fmt.Errorf("role.sqlite: commit: %w", cerr)
	}
	return nil
}

func sqliteCode(err error) (int, bool) {
	sqliteErr, ok := errors.AsType[*sqlitedrv.Error](err)
	if !ok {
		return 0, false
	}
	return sqliteErr.Code(), true
}

func scanHeld(rows *sql.Rows) (map[uuid.UUID]int, error) {
	defer func() { _ = rows.Close() }()
	out := map[uuid.UUID]int{}
	for rows.Next() {
		var raw string
		var n int
		if err := rows.Scan(&raw, &n); err != nil {
			return nil, fmt.Errorf("role: scan held: %w", err)
		}
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("role: parse held role_id: %w", err)
		}
		out[id] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("role: held rows: %w", err)
	}
	return out, nil
}
