package tenant

import (
	"context"
	"database/sql"
	"fmt"

	jetpg "github.com/go-jet/jet/v2/postgres"
	jetsqlite "github.com/go-jet/jet/v2/sqlite"
	"github.com/google/uuid"

	"altalune.id/template/internal/platform/db"
	sqliteent "altalune.id/template/internal/platform/db/entity/sqlite"
)

// OrgReader lists every org id, across every tenant scope.
type OrgReader interface {
	OrgIDs(ctx context.Context) ([]uuid.UUID, error)
}

// NewOrgReader returns the cross-tenant org reader for the configured driver.
func NewOrgReader(pool db.Pool, driver db.Driver, schema, tablePrefix string) OrgReader {
	if driver == db.DriverPostgres {
		if schema == "" {
			schema = "public"
		}
		// NOTE: RawStatement because go-jet has no builder for a set-returning function in FROM position. Only config-supplied identifiers are interpolated; bind any value as a named argument.
		query, args := jetpg.RawStatement(
			"SELECT id FROM " + schema + "." + tablePrefix + "list_org_ids()").Sql()
		return &pgOrgReader{conn: pool.W, query: query, args: args}
	}
	orgs := sqliteent.NewOrgs(tablePrefix)
	query, args := jetsqlite.SELECT(orgs.ID).FROM(orgs).ORDER_BY(orgs.CreatedAt.ASC()).Sql()
	return &sqliteOrgReader{conn: pool.W, query: query, args: args}
}

// SECURITY: reads the SECURITY DEFINER wrapper, never the table — a direct read returns zero rows under FORCE row level security.
type pgOrgReader struct {
	conn  *sql.DB
	query string
	args  []any
}

func (r *pgOrgReader) OrgIDs(ctx context.Context) ([]uuid.UUID, error) {
	return scanOrgIDs(ctx, r.conn, r.query, r.args)
}

type sqliteOrgReader struct {
	conn  *sql.DB
	query string
	args  []any
}

func (r *sqliteOrgReader) OrgIDs(ctx context.Context) ([]uuid.UUID, error) {
	return scanOrgIDs(ctx, r.conn, r.query, r.args)
}

// NOTE: drains the cursor before returning so a long sweep holds no open connection.
func scanOrgIDs(ctx context.Context, conn *sql.DB, query string, args []any) ([]uuid.UUID, error) {
	rows, err := conn.QueryContext(ctx, query, args...) //nolint:rowserrcheck // checked via rows.Err below
	if err != nil {
		return nil, fmt.Errorf("tenant: enumerate orgs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []uuid.UUID
	for rows.Next() {
		var raw string
		if scanErr := rows.Scan(&raw); scanErr != nil {
			return nil, fmt.Errorf("tenant: scan org id: %w", scanErr)
		}
		id, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			return nil, fmt.Errorf("tenant: parse org id %q: %w", raw, parseErr)
		}
		out = append(out, id)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("tenant: enumerate orgs: %w", rowsErr)
	}
	return out, nil
}
