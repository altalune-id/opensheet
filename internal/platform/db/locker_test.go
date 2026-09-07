package db_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/platform/db"
)

func TestNoopLocker_AlwaysAcquires(t *testing.T) {
	release, acquired, err := db.NoopLocker{}.TryLock(t.Context(), "any")
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotNil(t, release)
	release()
}

func TestNewLocker_DispatchesByDriver(t *testing.T) {
	tests := []struct {
		name   string
		driver db.Driver
		want   any
	}{
		{"sqlite gets noop", db.DriverSQLite, db.NoopLocker{}},
		{"postgres gets pg", db.DriverPostgres, &db.PgLocker{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := db.NewLocker(db.DBConfig{Driver: tt.driver}, db.Pool{}, nil)
			require.IsType(t, tt.want, got)
		})
	}
}

func TestLockKey_IsStableAndDistinct(t *testing.T) {
	require.Equal(t, db.LockKey("todo-autocomplete-stale"), db.LockKey("todo-autocomplete-stale"))
	require.NotEqual(t, db.LockKey("a"), db.LockKey("b"))
}
