package session

import (
	"context"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/db"
	pgent "altalune.id/opensheet/internal/platform/db/entity/postgres"
	"altalune.id/opensheet/internal/platform/sealer"
)

// NewStore returns the session store matching the database driver.
func NewStore(
	cfg db.DBConfig,
	pool db.Pool,
	sl sealer.Sealer,
	unexpected apperror.UnexpectedFunc,
) Store {
	if cfg.Driver != db.DriverPostgres {
		return NewMemoryStore()
	}
	if unexpected == nil {
		unexpected = discardUnexpected
	}
	// NOTE: pool.W for reads too — pool.R may lag, and the redirect after WriteSession would miss.
	return &pgStore{
		db:         pool.W,
		table:      pgent.NewSessions(cfg.Schema, cfg.TablePrefix),
		sealer:     sl,
		unexpected: unexpected,
	}
}

func discardUnexpected(context.Context, string, error, ...any) *apperror.AppError { return nil }
