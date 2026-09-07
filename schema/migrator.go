// Package schema owns the goose-backed migration runner and the RLS boot guard.
package schema

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"

	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/db"
)

//go:embed migrations/sqlite/VERSION migrations/postgres/VERSION migrations/sqlite/*.sql migrations/postgres/*.sql
var migrationsFS embed.FS

// TargetVersion reads migrations/<driver>/VERSION and returns the pinned goose target for that driver. Each dialect pins independently since postgres and sqlite may have different migration counts.
func TargetVersion(driver db.Driver) (int64, error) {
	_, dir, err := gooseDialect(driver)
	if err != nil {
		return 0, err
	}
	path := dir + "/VERSION"
	b, err := fs.ReadFile(migrationsFS, path)
	if err != nil {
		return 0, fmt.Errorf("migrator: read %s: %w — pin the target migration version (a single integer, one line)", path, err)
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return 0, fmt.Errorf("migrator: %s is empty — pin the target migration version (a single integer, one line)", path)
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("migrator: %s %q is not a valid version: %w", path, s, err)
	}
	if v < 0 {
		return 0, fmt.Errorf("migrator: %s %d is negative", path, v)
	}
	return v, nil
}

func gooseDialect(d db.Driver) (database.Dialect, string, error) {
	switch d {
	case db.DriverSQLite:
		return database.DialectSQLite3, "migrations/sqlite", nil
	case db.DriverPostgres:
		return database.DialectPostgres, "migrations/postgres", nil
	default:
		return "", "", fmt.Errorf("migrator: unknown driver %q", d)
	}
}

func migrationsBookkeepingTable(cfg *config.Config) string {
	if cfg.DB.Driver == db.DriverPostgres {
		return cfg.DB.TablePrefix + "goose_db_version"
	}
	return "altempl_goose_db_version"
}

func migrationProvider(sqldb *sql.DB, cfg *config.Config) (*goose.Provider, error) {
	dbDialect, dir, err := gooseDialect(cfg.DB.Driver)
	if err != nil {
		return nil, err
	}
	sub, err := fs.Sub(migrationsFS, dir)
	if err != nil {
		return nil, fmt.Errorf("migrator: sub-fs %s: %w", dir, err)
	}
	tpl := newTemplatedFS(sub, templateVars{
		Schema:      cfg.DB.Schema,
		TablePrefix: cfg.DB.TablePrefix,
		RLSEnforce:  cfg.Tenant.RLSEnforce,
	})
	store, err := database.NewStore(dbDialect, migrationsBookkeepingTable(cfg))
	if err != nil {
		return nil, fmt.Errorf("migrator: store: %w", err)
	}
	return goose.NewProvider("", sqldb, tpl, goose.WithStore(store))
}

// MigrateUp applies pending migrations up to the version pinned in migrations/<driver>/VERSION. Newer migration files sitting beyond the pinned version are ignored.
func MigrateUp(ctx context.Context, sqldb *sql.DB, cfg *config.Config) error {
	target, err := TargetVersion(cfg.DB.Driver)
	if err != nil {
		return err
	}
	p, err := migrationProvider(sqldb, cfg)
	if err != nil {
		return err
	}
	if _, err := p.UpTo(ctx, target); err != nil {
		return fmt.Errorf("migrator: up to %d: %w", target, err)
	}
	return nil
}

// MigrationRow is the CLI-facing projection of one migration's status.
type MigrationRow struct {
	Version   int64
	Applied   bool
	AppliedAt time.Time
	Source    string
}

// MigrateStatus reports every known migration and whether it has been applied.
func MigrateStatus(ctx context.Context, sqldb *sql.DB, cfg *config.Config) ([]MigrationRow, error) {
	p, err := migrationProvider(sqldb, cfg)
	if err != nil {
		return nil, err
	}
	items, err := p.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("migrator: status: %w", err)
	}
	rows := make([]MigrationRow, 0, len(items))
	for _, it := range items {
		row := MigrationRow{Applied: it.State == goose.StateApplied, AppliedAt: it.AppliedAt}
		if it.Source != nil {
			row.Version = it.Source.Version
			row.Source = it.Source.Path
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// MigrateDownTo rolls migrations back to the given goose version.
func MigrateDownTo(ctx context.Context, sqldb *sql.DB, cfg *config.Config, version int64) error {
	p, err := migrationProvider(sqldb, cfg)
	if err != nil {
		return err
	}
	if _, err := p.DownTo(ctx, version); err != nil {
		return fmt.Errorf("migrator: down-to %d: %w", version, err)
	}
	return nil
}
