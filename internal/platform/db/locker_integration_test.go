//go:build integration

package db_test

import (
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/platform/db"
	"altalune.id/template/internal/testutil/pgtest"
)

func TestPgLocker_SecondAcquireIsRefusedThenSucceedsAfterRelease(t *testing.T) {
	h := pgtest.New(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Distinct pools so each holds its own backend session, as two replicas would.
	poolA, err := db.OpenPool(t.Context(), db.DBConfig{Driver: db.DriverPostgres, DSN: h.DSN}, log)
	require.NoError(t, err)
	t.Cleanup(func() { _ = poolA.Close() })
	poolB, err := db.OpenPool(t.Context(), db.DBConfig{Driver: db.DriverPostgres, DSN: h.DSN}, log)
	require.NoError(t, err)
	t.Cleanup(func() { _ = poolB.Close() })

	a := db.NewPgLocker(poolA, log)
	b := db.NewPgLocker(poolB, log)

	release, acquired, err := a.TryLock(t.Context(), "contended-job")
	require.NoError(t, err)
	require.True(t, acquired)

	_, acquiredB, err := b.TryLock(t.Context(), "contended-job")
	require.NoError(t, err)
	require.False(t, acquiredB, "a second process must not acquire a held lock")

	release()

	releaseB, acquiredB2, err := b.TryLock(t.Context(), "contended-job")
	require.NoError(t, err)
	require.True(t, acquiredB2, "the lock must be available after release")
	releaseB()
}

func TestPgLocker_DifferentJobsDoNotContend(t *testing.T) {
	h := pgtest.New(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pool, err := db.OpenPool(t.Context(), db.DBConfig{Driver: db.DriverPostgres, DSN: h.DSN}, log)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Close() })

	l := db.NewPgLocker(pool, log)
	r1, ok1, err := l.TryLock(t.Context(), "job-one")
	require.NoError(t, err)
	require.True(t, ok1)
	r2, ok2, err := l.TryLock(t.Context(), "job-two")
	require.NoError(t, err)
	require.True(t, ok2)
	r1()
	r2()
}
