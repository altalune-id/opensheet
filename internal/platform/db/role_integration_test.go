//go:build integration

package db_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/platform/db"
	"altalune.id/template/internal/testutil/pgtest"
	"altalune.id/template/nanoid"
)

func TestOpen_AppliesRoleOnEveryConnection(t *testing.T) {
	h := pgtest.New(t)

	admin, err := db.Open(t.Context(), db.DBConfig{Driver: db.DriverPostgres, DSN: h.DSN}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })
	role := uniqueRoleName(t)
	_, err = admin.ExecContext(t.Context(), fmt.Sprintf(`CREATE ROLE %q NOLOGIN`, role))
	require.NoError(t, err)
	// NOTE: t.Context() is already canceled by the time cleanups run, so teardown needs its own context.
	t.Cleanup(func() {
		_, dropErr := admin.ExecContext(context.Background(), fmt.Sprintf(`DROP OWNED BY %q`, role))
		require.NoError(t, dropErr, "leaked objects owned by %s", role)
		_, dropErr = admin.ExecContext(context.Background(), fmt.Sprintf(`DROP ROLE IF EXISTS %q`, role))
		require.NoError(t, dropErr, "leaked role %s", role)
	})
	_, err = admin.ExecContext(t.Context(), fmt.Sprintf(`GRANT %q TO CURRENT_USER`, role))
	require.NoError(t, err)

	roled, err := db.Open(t.Context(), db.DBConfig{
		Driver: db.DriverPostgres, DSN: h.DSN, Role: role, MaxOpenConns: 3,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = roled.Close() })

	for i := range 5 {
		var current string
		require.NoError(t, roled.QueryRowContext(t.Context(), `SELECT current_user`).Scan(&current))
		require.Equal(t, role, current, "connection %d did not carry the role", i)
	}
}

func TestOpen_EmptyRoleIsNoOp(t *testing.T) {
	h := pgtest.New(t)
	conn, err := db.Open(t.Context(), db.DBConfig{Driver: db.DriverPostgres, DSN: h.DSN}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	var current string
	require.NoError(t, conn.QueryRowContext(t.Context(), `SELECT current_user`).Scan(&current))
	require.NotEmpty(t, current)
}

func TestOpen_UnknownRoleFailsLoudly(t *testing.T) {
	h := pgtest.New(t)

	start := time.Now()
	_, err := db.Open(t.Context(), db.DBConfig{
		Driver: db.DriverPostgres, DSN: h.DSN, Role: "altempl_role_does_not_exist",
		ConnectTimeout: 30 * time.Second, ConnectBackoff: 250 * time.Millisecond,
	}, nil)
	elapsed := time.Since(start)

	require.Error(t, err, "an unknown role must not open silently")
	require.Contains(t, err.Error(), "altempl_role_does_not_exist")
	require.Less(t, elapsed, 5*time.Second,
		"a server-rejected SET ROLE is permanent and must not burn the whole connect budget (%s)", elapsed)
}

func TestOpen_InvalidRoleIdentRejected(t *testing.T) {
	h := pgtest.New(t)
	_, err := db.Open(t.Context(), db.DBConfig{
		Driver: db.DriverPostgres, DSN: h.DSN, Role: "bad\nrole",
	}, nil)
	require.Error(t, err)
	require.True(t, db.IsInvalidRoleError(err), "want InvalidRoleError, got %T", err)
}

func uniqueRoleName(t *testing.T) string {
	t.Helper()
	// NOTE: pgtest reuses TEST_PG_DSN when set, so a fixed role name collides on the second run.
	suffix, err := nanoid.New(10)
	require.NoError(t, err)
	return "altempl_test_" + strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(suffix))
}
