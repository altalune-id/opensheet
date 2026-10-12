package fakes_test

import (
	"context"
	"errors"
	"testing"

	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/testutil/fakes"
)

func TestUnitOfWork_RunsFnWithATxSlotOnCtx(t *testing.T) {
	ctx := t.Context()
	var ran bool
	err := fakes.UnitOfWork(ctx, func(ctx context.Context) error {
		ran = true
		_, ok := db.CurrentTx(ctx)
		if !ok {
			t.Error("CurrentTx did not find a tx slot inside the unit of work")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("UnitOfWork() error = %v, want nil", err)
	}
	if !ran {
		t.Fatal("fn was not called")
	}
}

func TestUnitOfWork_RefusesNesting(t *testing.T) {
	ctx := t.Context()
	err := fakes.UnitOfWork(ctx, func(ctx context.Context) error {
		return fakes.UnitOfWork(ctx, func(ctx context.Context) error {
			t.Fatal("nested fn must not run")
			return nil
		})
	})
	if !errors.Is(err, db.ErrNestedUnitOfWork) {
		t.Errorf("UnitOfWork() nested error = %v, want db.ErrNestedUnitOfWork", err)
	}
}
