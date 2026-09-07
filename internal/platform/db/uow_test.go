package db_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"

	"altalune.id/template/internal/platform/db"
)

func openTestPool(t *testing.T) db.Pool {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := sqlDB.ExecContext(t.Context(), "CREATE TABLE things (id INTEGER PRIMARY KEY, name TEXT NOT NULL)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	return db.Pool{W: sqlDB, R: sqlDB}
}

func TestRunInTx_CommitOnSuccess(t *testing.T) {
	t.Parallel()
	pool := openTestPool(t)

	err := db.RunInTx(t.Context(), pool, func(ctx context.Context) error {
		tx, ok := db.CurrentTx(ctx)
		if !ok {
			return errors.New("expected CurrentTx to return the enrolled tx")
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO things (name) VALUES ('committed')"); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RunInTx: %v", err)
	}

	var n int
	if err := pool.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM things WHERE name = 'committed'").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("committed row missing: n=%d", n)
	}
}

func TestRunInTx_RollbackOnError(t *testing.T) {
	t.Parallel()
	pool := openTestPool(t)

	want := errors.New("boom")
	err := db.RunInTx(t.Context(), pool, func(ctx context.Context) error {
		tx, _ := db.CurrentTx(ctx)
		if _, err := tx.ExecContext(ctx, "INSERT INTO things (name) VALUES ('rolled_back')"); err != nil {
			return err
		}
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}

	var n int
	if err := pool.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM things WHERE name = 'rolled_back'").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("row not rolled back: n=%d", n)
	}
}

func TestRunInTx_RejectsNestedUnitOfWork(t *testing.T) {
	t.Parallel()
	pool := openTestPool(t)

	outerErr := db.RunInTx(t.Context(), pool, func(outer context.Context) error {
		return db.RunInTx(outer, pool, func(inner context.Context) error {
			t.Fatalf("nested fn should not run")
			return nil
		})
	})
	if !errors.Is(outerErr, db.ErrNestedUnitOfWork) {
		t.Fatalf("err = %v, want ErrNestedUnitOfWork", outerErr)
	}
}
