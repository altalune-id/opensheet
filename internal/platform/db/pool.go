package db

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
)

// Pool bundles writer (W) and reader (R) handles. R aliases W when no separate DSN is configured.
type Pool struct {
	W *sql.DB
	R *sql.DB

	uow *sql.DB
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
		p := Pool{W: writer, R: writer}
		if sqliteInMemory(cfg.DSN) {
			return p, nil
		}
		uowCfg := cfg
		uowCfg.DSN = sqliteDSNWithImmediateTx(cfg.DSN)
		uow, uErr := Open(ctx, uowCfg, log)
		if uErr != nil {
			_ = p.Close()
			return Pool{}, uErr
		}
		p.uow = uow
		return p, nil
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

// Close closes every distinct handle and joins their errors; safe when R aliases W.
func (p Pool) Close() error {
	var errs []error
	if p.uow != nil {
		errs = append(errs, p.uow.Close())
	}
	if p.R != nil && p.R != p.W {
		errs = append(errs, p.R.Close())
	}
	if p.W != nil {
		errs = append(errs, p.W.Close())
	}
	return errors.Join(errs...)
}
