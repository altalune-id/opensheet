package boot

import (
	"context"
	"fmt"
	"log/slog"

	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/db"
	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/schema"
)

func openDBAndMigrate(ctx context.Context, cfg *config.Config, log *slog.Logger) (db.Pool, *tenant.PgConn, error) {
	if cfg.DB.AutoMigrate {
		if err := runMigrations(ctx, cfg, log); err != nil {
			return db.Pool{}, nil, err
		}
	}
	pool, err := db.OpenPool(ctx, cfg.DB, log)
	if err != nil {
		return db.Pool{}, nil, err
	}
	if cfg.DB.Driver == db.DriverPostgres {
		if err := schema.RLSGuard(ctx, pool.W, cfg); err != nil {
			_ = pool.Close()
			return db.Pool{}, nil, fmt.Errorf("boot: rls guard: %w", err)
		}
	}
	return pool, tenant.NewPgConn(pool.W), nil
}

// MigratorDBConfig shapes the connection migrations run on: the migrator DSN when set, the migrator role always, on a single session.
func MigratorDBConfig(cfg *config.Config) db.DBConfig {
	migCfg := cfg.DB
	if cfg.DB.Migrator.DSN != "" {
		migCfg.DSN = cfg.DB.Migrator.DSN
	}
	migCfg.Role = cfg.DB.Migrator.Role
	// NOTE: one session keeps goose's advisory lock and SET ROLE state on the same connection.
	migCfg.MaxOpenConns = 1
	migCfg.MaxIdleConns = 1
	return migCfg
}

func runMigrations(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	migCfg := MigratorDBConfig(cfg)
	usingMigratorDSN := cfg.DB.Migrator.DSN != ""
	migDB, err := db.Open(ctx, migCfg, log)
	if err != nil {
		return fmt.Errorf("boot: open migrator: %w", err)
	}
	defer func() { _ = migDB.Close() }()

	migAwareCfg := *cfg
	migAwareCfg.DB = migCfg
	if err := schema.MigrateUp(ctx, migDB, &migAwareCfg); err != nil {
		return fmt.Errorf("boot: migrate: %w", err)
	}
	if log != nil && usingMigratorDSN {
		log.Info("boot: migrations applied via dedicated migrator connection")
	}
	return nil
}
