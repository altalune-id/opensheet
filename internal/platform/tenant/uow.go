package tenant

import (
	"context"

	"altalune.id/opensheet/internal/platform/db"
)

// UnitOfWork runs fn in one transaction; every store called with fn's ctx joins it.
type UnitOfWork func(ctx context.Context, fn func(ctx context.Context) error) error

// NewUnitOfWork returns the UnitOfWork for cfg.Driver.
func NewUnitOfWork(cfg db.DBConfig, pool db.Pool, pc *PgConn) UnitOfWork {
	if cfg.Driver == db.DriverPostgres {
		return func(ctx context.Context, fn func(ctx context.Context) error) error {
			tc, err := From(ctx)
			if err != nil {
				return err
			}
			return RunInTx(ctx, pc, tc, fn)
		}
	}
	return func(ctx context.Context, fn func(ctx context.Context) error) error {
		return db.RunInTx(ctx, pool, fn)
	}
}
