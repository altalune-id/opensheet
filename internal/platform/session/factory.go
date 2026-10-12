package session

import (
	"context"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/db"
	pgent "altalune.id/opensheet/internal/platform/db/entity/postgres"
	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
	"altalune.id/opensheet/internal/platform/sealer"
)

// NewStore returns the session store matching the database driver.
func NewStore(
	cfg db.DBConfig,
	pool db.Pool,
	sl sealer.Sealer,
	unexpected apperror.UnexpectedFunc,
) Store {
	if unexpected == nil {
		unexpected = discardUnexpected
	}
	switch cfg.Driver {
	case db.DriverPostgres:
		// NOTE: pool.W for reads too — pool.R may lag, and the redirect after a login would miss the row.
		return &pgStore{
			db:         pool.W,
			table:      pgent.NewSessions(cfg.Schema, cfg.TablePrefix),
			sealer:     sl,
			unexpected: unexpected,
		}
	case db.DriverSQLite:
		return &sqliteStore{
			db:         pool.W,
			table:      sqliteent.NewSessions(cfg.TablePrefix),
			sealer:     sl,
			unexpected: unexpected,
		}
	}
	return NewMemoryStore()
}

func discardUnexpected(context.Context, string, error, ...any) *apperror.AppError { return nil }
