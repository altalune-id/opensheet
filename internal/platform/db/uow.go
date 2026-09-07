package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrNestedUnitOfWork is returned when RunInTx is invoked while another unit of work is already active on the same context.
var ErrNestedUnitOfWork = errors.New("db: nested unit of work is not supported")

type txCtxKey struct{}

type unitOfWork struct {
	tx *sql.Tx
}

// RunInTx begins a transaction on pool.W, exposes it via ctx so store methods enroll, and commits when fn returns nil.
func RunInTx(ctx context.Context, pool Pool, fn func(ctx context.Context) error) error {
	if _, ok := unitOfWorkFromContext(ctx); ok {
		return ErrNestedUnitOfWork
	}
	if pool.W == nil {
		return errors.New("db: RunInTx: pool.W is nil")
	}
	tx, err := pool.W.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("db: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := fn(contextWithUnitOfWork(ctx, &unitOfWork{tx: tx})); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db: commit: %w", err)
	}
	return nil
}

// CurrentTx returns the transaction of the unit of work bound to ctx, or (nil, false) if none is active.
func CurrentTx(ctx context.Context) (*sql.Tx, bool) {
	uow, ok := unitOfWorkFromContext(ctx)
	if !ok {
		return nil, false
	}
	return uow.tx, true
}

// ContextWithTx returns a derived context carrying tx as the active unit of work observable via CurrentTx.
func ContextWithTx(ctx context.Context, tx *sql.Tx) context.Context {
	return contextWithUnitOfWork(ctx, &unitOfWork{tx: tx})
}

func contextWithUnitOfWork(ctx context.Context, uow *unitOfWork) context.Context {
	return context.WithValue(ctx, txCtxKey{}, uow)
}

func unitOfWorkFromContext(ctx context.Context) (*unitOfWork, bool) {
	uow, ok := ctx.Value(txCtxKey{}).(*unitOfWork)
	return uow, ok
}
