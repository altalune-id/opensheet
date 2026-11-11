package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
)

// ErrNestedUnitOfWork is returned when RunInTx is invoked while another unit of work is already active on the same context.
var ErrNestedUnitOfWork = errors.New("db: nested unit of work is not supported")

type txCtxKey struct{}

type unitOfWork struct {
	tx    *sql.Tx
	hooks *afterCommitHooks
}

type afterCommitHooks struct {
	mu  sync.Mutex
	fns []func()
}

// RunInTx begins a transaction on the pool's unit-of-work writer (pool.W unless OpenPool gave SQLite its own), exposes it via ctx so store methods enroll, and commits when fn returns nil.
func RunInTx(ctx context.Context, pool Pool, fn func(ctx context.Context) error) error {
	if _, ok := unitOfWorkFromContext(ctx); ok {
		return ErrNestedUnitOfWork
	}
	w := pool.W
	if pool.uow != nil {
		w = pool.uow
	}
	if w == nil {
		return errors.New("db: RunInTx: pool.W is nil")
	}
	tx, err := w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("db: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := ContextWithTx(ctx, tx)
	if err := fn(txCtx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db: commit: %w", err)
	}
	RunAfterCommit(txCtx)
	return nil
}

// AfterCommit runs fn once the unit of work bound to ctx commits, never on rollback or a failed commit, or at once when ctx has none.
func AfterCommit(ctx context.Context, fn func()) {
	uow, ok := unitOfWorkFromContext(ctx)
	if !ok {
		fn()
		return
	}
	uow.hooks.mu.Lock()
	defer uow.hooks.mu.Unlock()
	uow.hooks.fns = append(uow.hooks.fns, fn)
}

// RunAfterCommit runs and clears the hooks registered on ctx's unit of work; the unit's owner calls it after a successful Commit.
func RunAfterCommit(ctx context.Context) {
	uow, ok := unitOfWorkFromContext(ctx)
	if !ok {
		return
	}
	uow.hooks.mu.Lock()
	fns := uow.hooks.fns
	uow.hooks.fns = nil
	uow.hooks.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// CurrentTx returns the transaction of the unit of work bound to ctx, or (nil, false) if none is active.
func CurrentTx(ctx context.Context) (*sql.Tx, bool) {
	uow, ok := unitOfWorkFromContext(ctx)
	if !ok {
		return nil, false
	}
	return uow.tx, true
}

// ContextWithTx returns a derived context carrying tx as the active unit of work observable via CurrentTx, with its own after-commit hooks.
func ContextWithTx(ctx context.Context, tx *sql.Tx) context.Context {
	return contextWithUnitOfWork(ctx, &unitOfWork{tx: tx, hooks: &afterCommitHooks{}})
}

func contextWithUnitOfWork(ctx context.Context, uow *unitOfWork) context.Context {
	return context.WithValue(ctx, txCtxKey{}, uow)
}

func unitOfWorkFromContext(ctx context.Context) (*unitOfWork, bool) {
	uow, ok := ctx.Value(txCtxKey{}).(*unitOfWork)
	return uow, ok
}
