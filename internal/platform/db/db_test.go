package db_test

import (
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/db"
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

func TestOpenSQLite_EnforcesForeignKeysForEveryDSNShape(t *testing.T) {
	dir := t.TempDir()
	for _, dsn := range []string{
		filepath.Join(dir, "bare.db"),
		"file:" + filepath.Join(dir, "prefixed.db"),
		"file:" + filepath.Join(dir, "withparams.db") + "?_pragma=busy_timeout(5000)",
	} {
		t.Run(dsn, func(t *testing.T) {
			sqlDB, err := db.Open(t.Context(), db.DBConfig{Driver: db.DriverSQLite, DSN: dsn}, nil)
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })

			var on int
			require.NoError(t, sqlDB.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&on))
			require.Equal(t, 1, on, "foreign keys must be enforced; ON DELETE RESTRICT silently no-ops otherwise")
		})
	}
}

func TestOpenSQLite_EnforcesForeignKeysOnEveryPooledConnection(t *testing.T) {
	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "pooled.db")

	sqlDB, err := db.Open(t.Context(), db.DBConfig{Driver: db.DriverSQLite, DSN: dsn}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	conn1, err := sqlDB.Conn(t.Context())
	require.NoError(t, err)
	defer conn1.Close()

	var on1 int
	require.NoError(t, conn1.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&on1))
	require.Equal(t, 1, on1, "first pooled connection must enforce foreign keys")

	conn2, err := sqlDB.Conn(t.Context())
	require.NoError(t, err)
	defer conn2.Close()

	var on2 int
	require.NoError(t, conn2.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&on2))
	require.Equal(t, 1, on2, "a second, freshly-opened pooled connection must also enforce foreign keys since the pragma lives in the DSN, not a per-session PRAGMA call")
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
