package db

import (
	"context"
	"database/sql"
	"log/slog"
)

// Pool bundles writer (W) and reader (R) handles. R aliases W when no separate DSN is configured.
type Pool struct {
	W *sql.DB
	R *sql.DB
}

// OpenPool opens the writer and, for Postgres, any configured reader connection.
func OpenPool(ctx context.Context, cfg DBConfig, log *slog.Logger) (Pool, error) {
	writer, err := Open(ctx, cfg, log)
	if err != nil {
		return Pool{}, err
	}
	if cfg.Driver == DriverSQLite {
		if log != nil && cfg.Reader.DSN != "" {
			log.Debug("db: reader DSN ignored for sqlite driver")
		}
		return Pool{W: writer, R: writer}, nil
	}

	p := Pool{W: writer, R: writer}

	if cfg.Reader.DSN != "" {
		readerCfg := cfg
		readerCfg.DSN = cfg.Reader.DSN
		readerCfg.Role = cfg.Reader.Role
		readerCfg.MaxOpenConns = cfg.Reader.MaxOpenConns
		readerCfg.MaxIdleConns = cfg.Reader.MaxIdleConns
		readerCfg.ConnMaxLifetime = cfg.Reader.ConnMaxLifetime
		readerCfg.ConnMaxIdleTime = cfg.Reader.ConnMaxIdleTime
		reader, rErr := Open(ctx, readerCfg, log)
		if rErr != nil {
			_ = p.Close()
			return Pool{}, rErr
		}
		p.R = reader
	}

	return p, nil
}

// Close closes every distinct handle; safe when R aliases W.
func (p Pool) Close() error {
	if p.R != nil && p.R != p.W {
		_ = p.R.Close()
	}
	if p.W != nil {
		return p.W.Close()
	}
	return nil
}
