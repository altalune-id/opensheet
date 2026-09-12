package sheet

import (
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/tenant"
)

// NewStore dispatches to the driver-specific Store implementation.
func NewStore(cfg db.DBConfig, pool db.Pool, pc *tenant.PgConn) Store {
	if cfg.Driver == db.DriverPostgres {
		return newPostgresStore(pool, pc, cfg.Schema, cfg.TablePrefix)
	}
	return newSQLiteStore(pool.W, cfg.TablePrefix)
}

// NewRowStore dispatches to the driver-specific RowStore implementation.
// NOTE: unlike NewSnapshotStore there is no memory variant — the projection is always in the database, on both drivers.
func NewRowStore(cfg db.DBConfig, pool db.Pool, pc *tenant.PgConn) RowStore {
	if cfg.Driver == db.DriverPostgres {
		return newPostgresRowStore(pc, cfg.Schema, cfg.TablePrefix)
	}
	return newSQLiteRowStore(pool.W, cfg.TablePrefix)
}

// NewUnitOfWork dispatches to the driver-specific UnitOfWork implementation.
func NewUnitOfWork(cfg db.DBConfig, pool db.Pool, pc *tenant.PgConn) UnitOfWork {
	if cfg.Driver == db.DriverPostgres {
		return newPgUnitOfWork(pc)
	}
	return newSQLiteUnitOfWork(pool.W)
}
