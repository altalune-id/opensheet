package schema

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"altalune.id/opensheet/internal/platform/db"
)

// StaleSchemaError reports required tables that the database does not have.
type StaleSchemaError struct{ Missing []string }

func (e *StaleSchemaError) Error() string {
	return fmt.Sprintf(
		"schema: missing table(s) %s — goose records only a version number, so editing an already-applied migration is a silent no-op; recreate the database or add the table by hand",
		strings.Join(e.Missing, ", "),
	)
}

// IsStaleSchemaError reports whether err's tree contains a *StaleSchemaError.
func IsStaleSchemaError(err error) bool {
	_, ok := errors.AsType[*StaleSchemaError](err)
	return ok
}

// RequiredTableSuffixes lists tables that carry no org_id and so are invisible to TenantTableSuffixes.
var RequiredTableSuffixes = []string{"sessions"} //nolint:gochecknoglobals // Immutable manifest; not runtime state.

// AssertRequiredTables reports any table in RequiredTableSuffixes that the database is missing.
func AssertRequiredTables(ctx context.Context, conn *sql.DB, cfg *db.DBConfig) error {
	if cfg == nil || len(RequiredTableSuffixes) == 0 {
		return nil
	}
	want := make([]string, 0, len(RequiredTableSuffixes))
	for _, s := range RequiredTableSuffixes {
		want = append(want, cfg.TablePrefix+s)
	}

	present, err := existingTables(ctx, conn, cfg.Driver, want)
	if err != nil {
		return err
	}
	missing := make([]string, 0, len(want))
	for _, name := range want {
		if !present[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return &StaleSchemaError{Missing: missing}
}

func existingTables(ctx context.Context, conn *sql.DB, driver db.Driver, want []string) (map[string]bool, error) {
	if len(want) == 0 {
		return map[string]bool{}, nil
	}
	query := `
		SELECT c.relname
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind IN ('r','p')
		  AND n.nspname = ANY (current_schemas(false))
		  AND c.relname = ANY ($1)
	`
	args := []any{want}
	if driver == db.DriverSQLite {
		query = `SELECT name FROM sqlite_master WHERE type = 'table' AND name IN (?` +
			strings.Repeat(",?", len(want)-1) + `)`
		args = make([]any, 0, len(want))
		for _, name := range want {
			args = append(args, name)
		}
	}

	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("schema: required tables: %w", err)
	}
	defer func() { _ = rows.Close() }()
	present := make(map[string]bool, len(want))
	for rows.Next() {
		var name string
		if scanErr := rows.Scan(&name); scanErr != nil {
			return nil, fmt.Errorf("schema: scan required tables: %w", scanErr)
		}
		present[name] = true
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("schema: required tables rows: %w", rowsErr)
	}
	return present, nil
}
