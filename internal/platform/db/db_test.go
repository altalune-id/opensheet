package db_test

import (
	"database/sql"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/platform/db"
)

func TestOpen_UnknownDriver(t *testing.T) {
	t.Parallel()
	if _, err := db.Open(t.Context(), db.DBConfig{Driver: "mysql", DSN: "ignored"}, nil); err == nil {
		t.Fatal("Open with unknown driver = nil error, want failure")
	}
}

func TestOpen_EmptyDriver(t *testing.T) {
	t.Parallel()
	if _, err := db.Open(t.Context(), db.DBConfig{DSN: "ignored"}, nil); err == nil {
		t.Fatal("Open with empty driver = nil error, want failure")
	}
}

func TestOpen_EmptyDSN(t *testing.T) {
	t.Parallel()
	if _, err := db.Open(t.Context(), db.DBConfig{Driver: db.DriverSQLite}, nil); err == nil {
		t.Fatal("Open with empty DSN = nil error, want failure")
	}
}

func TestOpen_SQLiteMemory(t *testing.T) {
	t.Parallel()
	sqlDB, err := db.Open(t.Context(), db.DBConfig{Driver: db.DriverSQLite, DSN: ":memory:"}, nil)
	if err != nil {
		t.Fatalf("Open(sqlite :memory:) err = %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := sqlDB.Ping(); err != nil {
		t.Fatalf("Ping = %v", err)
	}
}

func TestOpen_SQLiteAppliesPoolTuning(t *testing.T) {
	t.Parallel()
	sqlDB, err := db.Open(t.Context(), db.DBConfig{
		Driver:       db.DriverSQLite,
		DSN:          ":memory:",
		MaxOpenConns: 3,
		MaxIdleConns: 2,
	}, nil)
	if err != nil {
		t.Fatalf("Open err = %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if got := sqlDB.Stats().MaxOpenConnections; got != 3 {
		t.Fatalf("MaxOpenConnections = %d, want 3", got)
	}
}

func TestDBConfig_Validate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cfg     db.DBConfig
		wantErr bool
	}{
		{"ok sqlite", db.DBConfig{Driver: db.DriverSQLite, DSN: ":memory:"}, false},
		{"ok postgres", db.DBConfig{Driver: db.DriverPostgres, DSN: "postgres://x"}, false},
		{"missing driver", db.DBConfig{DSN: "x"}, true},
		{"unknown driver", db.DBConfig{Driver: "mysql", DSN: "x"}, true},
		{"missing DSN", db.DBConfig{Driver: db.DriverSQLite}, true},
		{"negative MaxOpenConns", db.DBConfig{Driver: db.DriverSQLite, DSN: ":memory:", MaxOpenConns: -1}, true},
		{"negative MaxIdleConns", db.DBConfig{Driver: db.DriverSQLite, DSN: ":memory:", MaxIdleConns: -1}, true},
		{"negative ConnMaxLifetime", db.DBConfig{Driver: db.DriverSQLite, DSN: ":memory:", ConnMaxLifetime: -1}, true},
		{"negative ConnMaxIdleTime", db.DBConfig{Driver: db.DriverSQLite, DSN: ":memory:", ConnMaxIdleTime: -1}, true},
		{"negative connectTimeout", db.DBConfig{Driver: db.DriverSQLite, DSN: ":memory:", ConnectTimeout: -1}, true},
		{"negative connectBackoff", db.DBConfig{Driver: db.DriverSQLite, DSN: ":memory:", ConnectBackoff: -1}, true},
		{"negative health.interval", db.DBConfig{Driver: db.DriverSQLite, DSN: ":memory:", Health: db.HealthConfig{Interval: -1}}, true},
		{"negative health.timeout", db.DBConfig{Driver: db.DriverSQLite, DSN: ":memory:", Health: db.HealthConfig{Timeout: -1}}, true},
		{"negative reader.maxOpenConns", db.DBConfig{Driver: db.DriverSQLite, DSN: ":memory:", Reader: db.ReaderConfig{MaxOpenConns: -1}}, true},
		{"ok reader and health set", db.DBConfig{Driver: db.DriverSQLite, DSN: ":memory:", Reader: db.ReaderConfig{DSN: "postgres://x", MaxOpenConns: 2}, Health: db.HealthConfig{Interval: 30 * time.Second, Timeout: 2 * time.Second}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestOpen_RetriesThenFailsWithinBudget(t *testing.T) {
	cfg := db.DBConfig{
		Driver:         db.DriverPostgres,
		DSN:            "postgres://nobody@127.0.0.1:1/none?sslmode=disable&connect_timeout=1",
		ConnectTimeout: 2 * time.Second,
		ConnectBackoff: 200 * time.Millisecond,
	}
	start := time.Now()
	_, err := db.Open(t.Context(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.Error(t, err)
	require.GreaterOrEqual(t, time.Since(start), 2*time.Second, "must have retried across the budget")
}

func TestOpenPool_ReaderAliasesWriterWhenUnset(t *testing.T) {
	cfg := db.DBConfig{Driver: db.DriverSQLite, DSN: ":memory:"}
	p, err := db.OpenPool(t.Context(), cfg, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })

	require.NotNil(t, p.W)
	require.Same(t, p.W, p.R, "reader must alias writer when unset")
}

func TestPool_Close_HandlesAliasing(t *testing.T) {
	tests := []struct {
		name string
		pool func(w *sql.DB) db.Pool
	}{
		{"reader aliased", func(w *sql.DB) db.Pool { return db.Pool{W: w, R: w} }},
		{"nil reader", func(w *sql.DB) db.Pool { return db.Pool{W: w} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, err := sql.Open("sqlite", ":memory:")
			require.NoError(t, err)
			require.NoError(t, tt.pool(w).Close())
		})
	}
}

func TestPool_Close_DistinctHandles(t *testing.T) {
	open := func() *sql.DB {
		d, err := sql.Open("sqlite", ":memory:")
		require.NoError(t, err)
		return d
	}
	w, r := open(), open()
	require.NoError(t, db.Pool{W: w, R: r}.Close())

	for name, d := range map[string]*sql.DB{"writer": w, "reader": r} {
		require.Error(t, d.PingContext(t.Context()), "%s must be closed", name)
	}
}
