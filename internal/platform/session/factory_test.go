package session_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/sealer"
	"altalune.id/opensheet/internal/platform/session"
)

func newTestSealer(t *testing.T) sealer.Sealer {
	t.Helper()
	sl, err := sealer.New(make([]byte, sealer.KeyLen))
	require.NoError(t, err)
	return sl
}

func TestNewStore_DispatchesOnTheDatabaseDriver(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		driver db.Driver
		memory bool
	}{
		{"postgres", db.DriverPostgres, false},
		{"sqlite", db.DriverSQLite, true},
		{"empty", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := session.NewStore(db.DBConfig{Driver: tc.driver}, db.Pool{}, newTestSealer(t), nil)
			_, isMem := got.(*session.MemoryStore)
			if isMem != tc.memory {
				t.Fatalf("driver %q: memory store = %v, want %v", tc.driver, isMem, tc.memory)
			}
		})
	}
}
