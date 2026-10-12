//go:build integration

package db_test

import (
	"cmp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/testutil/pgtest"
)

func TestOpen_PostgresSessionTimeZoneIsUTC(t *testing.T) {
	h := pgtest.New(t)
	for _, param := range []string{"timezone=Asia/Jakarta", "TimeZone=Asia/Jakarta"} {
		t.Run(param, func(t *testing.T) {
			conn, err := db.Open(t.Context(), db.DBConfig{
				Driver: db.DriverPostgres, DSN: withParam(h.DSN, param), MaxOpenConns: 2,
			}, nil)
			require.NoError(t, err)
			t.Cleanup(func() { _ = conn.Close() })

			var tz, now string
			require.NoError(t, conn.QueryRowContext(t.Context(), `SHOW TimeZone`).Scan(&tz))
			require.Equal(t, "UTC", tz)
			require.NoError(t, conn.QueryRowContext(t.Context(),
				`SELECT TIMESTAMPTZ '2026-10-01 23:30:00+07'::text`).Scan(&now))
			require.Equal(t, "2026-10-01 16:30:00+00", now)
		})
	}
}

func TestOpen_PostgresScansTimestamptzInUTC(t *testing.T) {
	h := pgtest.New(t)
	for _, role := range []string{"", "with-role"} {
		t.Run(cmp.Or(role, "no-role"), func(t *testing.T) {
			cfg := db.DBConfig{Driver: db.DriverPostgres, DSN: h.DSN}
			if role != "" {
				cfg.Role = currentUser(t, h.DSN)
			}
			conn, err := db.Open(t.Context(), cfg, nil)
			require.NoError(t, err)
			t.Cleanup(func() { _ = conn.Close() })

			var got time.Time
			require.NoError(t, conn.QueryRowContext(t.Context(),
				`SELECT TIMESTAMPTZ '2026-10-01 23:30:00+07'`).Scan(&got))
			require.Same(t, time.UTC, got.Location(), "timestamptz must scan in UTC, got %s", got.Location())
			require.Equal(t, time.Date(2026, 10, 1, 16, 30, 0, 0, time.UTC), got)
		})
	}
}

func withParam(dsn, kv string) string {
	if strings.Contains(dsn, "?") {
		return dsn + "&" + kv
	}
	return dsn + "?" + kv
}

func currentUser(t *testing.T, dsn string) string {
	t.Helper()
	conn, err := db.Open(t.Context(), db.DBConfig{Driver: db.DriverPostgres, DSN: dsn}, nil)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	var u string
	require.NoError(t, conn.QueryRowContext(t.Context(), `SELECT current_user`).Scan(&u))
	return u
}
