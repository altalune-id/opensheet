package project

import (
	"altalune.id/template/internal/platform/db"
	"altalune.id/template/internal/platform/tenant"
)

// NewStore dispatches to the driver-specific Store implementation.
func NewStore(cfg db.DBConfig, pool db.Pool, pc *tenant.PgConn) Store {
	if cfg.Driver == db.DriverPostgres {
		return newPostgresStore(pool, pc, cfg.Schema, cfg.TablePrefix)
	}
	return newSQLiteStore(pool.W, cfg.TablePrefix)
}
