package fakes

import (
	"context"

	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/tenant"
)

var _ tenant.UnitOfWork = UnitOfWork

// UnitOfWork runs fn with a placeholder nil transaction on ctx, for fake stores only — a real store would dereference the nil tx.
func UnitOfWork(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := db.CurrentTx(ctx); ok {
		return db.ErrNestedUnitOfWork
	}
	return fn(db.ContextWithTx(ctx, nil))
}
