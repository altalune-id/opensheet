package org

import (
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/tenant"
)

// NewStore selects the Store implementation for the configured driver.
func NewStore(cfg db.DBConfig, pool db.Pool, pc *tenant.PgConn) Store {
	if cfg.Driver == db.DriverPostgres {
		return newPostgresStore(pool, pc, cfg.Schema, cfg.TablePrefix)
	}
	return newSQLiteStore(pool.W, cfg.TablePrefix)
}
