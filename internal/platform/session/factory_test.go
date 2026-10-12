package session

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/sealer"
)

func TestNewStore_SelectsTheStoreForTheDriver(t *testing.T) {
	conn := &sql.DB{}
	pool := db.Pool{W: conn, R: conn}

	tests := []struct {
		name   string
		driver db.Driver
		want   any
	}{
		{"postgres", db.DriverPostgres, &pgStore{}},
		{"sqlite", db.DriverSQLite, &sqliteStore{}},
		{"unset falls back to memory", "", &MemoryStore{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewStore(db.DBConfig{Driver: tt.driver, TablePrefix: "opensheet_"}, pool, sealer.Disabled(), nil)
			assert.IsType(t, tt.want, got)
		})
	}
}

// SECURITY: sqlite must get a real store, not the memory fallback — otherwise a restart signs everyone out.
func TestNewStore_SQLiteIsNotTheMemoryFallback(t *testing.T) {
	conn := &sql.DB{}
	got := NewStore(
		db.DBConfig{Driver: db.DriverSQLite, TablePrefix: "opensheet_"},
		db.Pool{W: conn, R: conn}, sealer.Disabled(), nil)
	_, isMemory := got.(*MemoryStore)
	assert.False(t, isMemory, "sqlite must persist sessions, not hold them in memory")
}

func TestNewStore_NilUnexpectedIsTolerated(t *testing.T) {
	conn := &sql.DB{}
	s, ok := NewStore(
		db.DBConfig{Driver: db.DriverPostgres, TablePrefix: "opensheet_"},
		db.Pool{W: conn, R: conn}, sealer.Disabled(), nil).(*pgStore)
	require.True(t, ok)
	require.NotNil(t, s.unexpected, "a nil reporter must be replaced, not stored and later dereferenced")
	assert.Nil(t, s.unexpected(t.Context(), "msg", nil))
}

func TestNewStore_PostgresReadsThroughTheWriter(t *testing.T) {
	w, r := &sql.DB{}, &sql.DB{}
	s, ok := NewStore(
		db.DBConfig{Driver: db.DriverPostgres, TablePrefix: "opensheet_"},
		db.Pool{W: w, R: r}, sealer.Disabled(), nil).(*pgStore)
	require.True(t, ok)
	assert.Same(t, w, s.db, "a lagging replica would miss the row the post-login redirect reads")
}
