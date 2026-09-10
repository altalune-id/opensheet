package sheet

import (
	"context"
	"database/sql"
	"fmt"

	pdb "altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/tenant"
)

// UnitOfWork runs a multi-step write inside one transaction, enrolled on the context it hands fn.
type UnitOfWork interface {
	Run(ctx context.Context, fn func(ctx context.Context) error) error
}

type pgUnitOfWork struct {
	pgTxn
}

func newPgUnitOfWork(pc *tenant.PgConn) *pgUnitOfWork {
	return &pgUnitOfWork{pgTxn: pgTxn{pc: pc}}
}

// NOTE: the transaction is published through pdb.ContextWithTx, so a store method called with this context enrolls instead of owning — which is what keeps SELECT … FOR UPDATE held past the call that took it.
func (u *pgUnitOfWork) Run(ctx context.Context, fn func(ctx context.Context) error) error {
	tx, owned, err := u.txAcquire(ctx)
	if err != nil {
		return err
	}
	if fnErr := fn(pdb.ContextWithTx(ctx, tx)); fnErr != nil {
		return u.endTx(tx, owned, fnErr)
	}
	return u.endTx(tx, owned, nil)
}

type sqliteUnitOfWork struct {
	db *sql.DB
}

func newSQLiteUnitOfWork(sqlDB *sql.DB) *sqliteUnitOfWork {
	return &sqliteUnitOfWork{db: sqlDB}
}

func (u *sqliteUnitOfWork) Run(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := pdb.CurrentTx(ctx); ok {
		return fn(ctx)
	}
	tx, err := u.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sheet.unit.sqlite: begin: %w", err)
	}
	if fnErr := fn(pdb.ContextWithTx(ctx, tx)); fnErr != nil {
		_ = tx.Rollback()
		return fnErr
	}
	if cErr := tx.Commit(); cErr != nil {
		return fmt.Errorf("sheet.unit.sqlite: commit: %w", cErr)
	}
	return nil
}
