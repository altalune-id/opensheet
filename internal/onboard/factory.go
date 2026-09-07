package onboard

import (
	"altalune.id/template/internal/platform/db"
)

// NewStore dispatches to the Postgres or SQLite adapter based on cfg.Driver.
func NewStore(cfg db.DBConfig, pool db.Pool) Store {
	if cfg.Driver == db.DriverPostgres {
		return newPostgresStore(pool, cfg.Schema, cfg.TablePrefix)
	}
	return newSQLiteStore(pool.W, cfg.TablePrefix)
}
