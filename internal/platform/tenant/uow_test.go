package tenant_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/tenant"
)

func openSQLite(t *testing.T) db.Pool {
	t.Helper()
	pool, err := db.OpenPool(t.Context(), db.DBConfig{Driver: db.DriverSQLite, DSN: ":memory:"}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Close() })
	_, err = pool.W.ExecContext(t.Context(), "CREATE TABLE scratch (id INTEGER PRIMARY KEY)")
	require.NoError(t, err)
	return pool
}

func countScratch(t *testing.T, pool db.Pool) int {
	t.Helper()
	var n int
	require.NoError(t, pool.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM scratch").Scan(&n))
	return n
}

func TestUnitOfWork_SQLiteCommitsOnSuccess(t *testing.T) {
	pool := openSQLite(t)
	uow := tenant.NewUnitOfWork(db.DBConfig{Driver: db.DriverSQLite}, pool, nil)

	err := uow(t.Context(), func(ctx context.Context) error {
		tx, ok := db.CurrentTx(ctx)
		require.True(t, ok)
		_, xErr := tx.ExecContext(ctx, "INSERT INTO scratch(id) VALUES (1)")
		return xErr
	})
	require.NoError(t, err)
	require.Equal(t, 1, countScratch(t, pool))
}

func TestUnitOfWork_SQLiteRollsBackOnError(t *testing.T) {
	pool := openSQLite(t)
	uow := tenant.NewUnitOfWork(db.DBConfig{Driver: db.DriverSQLite}, pool, nil)
	boom := errors.New("boom")
	err := uow(t.Context(), func(ctx context.Context) error {
		tx, ok := db.CurrentTx(ctx)
		require.True(t, ok)
		_, xErr := tx.ExecContext(ctx, "INSERT INTO scratch(id) VALUES (1)")
		require.NoError(t, xErr)
		return boom
	})
	require.ErrorIs(t, err, boom)
	require.Equal(t, 0, countScratch(t, pool))
}

func TestUnitOfWork_SQLiteRejectsNestedUnitOfWork(t *testing.T) {
	pool := openSQLite(t)
	uow := tenant.NewUnitOfWork(db.DBConfig{Driver: db.DriverSQLite}, pool, nil)

	err := uow(t.Context(), func(ctx context.Context) error {
		return uow(ctx, func(context.Context) error {
			t.Fatal("nested fn should not run")
			return nil
		})
	})
	require.ErrorIs(t, err, db.ErrNestedUnitOfWork)
}
