package db_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/db"
)

func TestRunInTx_SQLiteConcurrentReadThenWriteUnitsOfWorkNeverFail(t *testing.T) {
	pool, err := db.OpenPool(t.Context(), db.DBConfig{
		Driver: db.DriverSQLite,
		DSN:    filepath.Join(t.TempDir(), "uow.db"),
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Close() })
	_, err = pool.W.ExecContext(t.Context(), "CREATE TABLE counters (id INTEGER PRIMARY KEY, n INTEGER NOT NULL)")
	require.NoError(t, err)
	_, err = pool.W.ExecContext(t.Context(), "INSERT INTO counters (id, n) VALUES (1, 0)")
	require.NoError(t, err)

	const writers = 30
	errs := make([]error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Go(func() {
			errs[i] = db.RunInTx(t.Context(), pool, func(ctx context.Context) error {
				tx, _ := db.CurrentTx(ctx)
				var n int
				if err := tx.QueryRowContext(ctx, "SELECT n FROM counters WHERE id = 1").Scan(&n); err != nil {
					return err
				}
				_, err := tx.ExecContext(ctx, "UPDATE counters SET n = ? WHERE id = 1", n+1)
				return err
			})
		})
	}
	wg.Wait()
	for i, err := range errs {
		require.NoError(t, err, "unit of work %d", i)
	}

	var n int
	require.NoError(t, pool.W.QueryRowContext(t.Context(), "SELECT n FROM counters WHERE id = 1").Scan(&n))
	require.Equal(t, writers, n, "every read-then-write unit of work must see the previous one's commit")
}

func TestRunInTx_SQLiteStoreReadTransactionsStayConcurrentWithAUnitOfWork(t *testing.T) {
	pool, err := db.OpenPool(t.Context(), db.DBConfig{
		Driver: db.DriverSQLite,
		DSN:    filepath.Join(t.TempDir(), "ro.db"),
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Close() })
	_, err = pool.W.ExecContext(t.Context(), "CREATE TABLE things (id INTEGER PRIMARY KEY)")
	require.NoError(t, err)

	held := make(chan struct{})
	markHeld := sync.OnceFunc(func() { close(held) })
	release := make(chan struct{})
	var wg sync.WaitGroup
	var writerErr error
	wg.Go(func() {
		defer markHeld()
		writerErr = db.RunInTx(t.Context(), pool, func(ctx context.Context) error {
			tx, _ := db.CurrentTx(ctx)
			if _, err := tx.ExecContext(ctx, "INSERT INTO things (id) VALUES (1)"); err != nil {
				return err
			}
			markHeld()
			<-release
			return nil
		})
	})
	<-held
	var n int
	readErr := func() error {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		tx, err := pool.W.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		return tx.QueryRowContext(ctx, "SELECT count(*) FROM things").Scan(&n)
	}()
	close(release)
	wg.Wait()
	require.NoError(t, writerErr)
	require.NoError(t, readErr, "a store's own read transaction on W must not wait for the unit of work's write lock")
	require.Equal(t, 0, n, "the read sees the last committed state")
}
