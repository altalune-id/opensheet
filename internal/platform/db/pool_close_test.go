package db_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/db"
)

type closeFailConnector struct{ err error }

func (c closeFailConnector) Connect(context.Context) (driver.Conn, error) {
	return closeFailConn(c), nil
}

func (c closeFailConnector) Driver() driver.Driver { return nil }

type closeFailConn struct{ err error }

func (closeFailConn) Prepare(string) (driver.Stmt, error) { return nil, errors.ErrUnsupported }

func (c closeFailConn) Close() error { return c.err }

func (closeFailConn) Begin() (driver.Tx, error) { return nil, errors.ErrUnsupported }

func openCloseFailing(t *testing.T, err error) *sql.DB {
	t.Helper()
	d := sql.OpenDB(closeFailConnector{err: err})
	conn, cErr := d.Conn(t.Context())
	require.NoError(t, cErr)
	require.NoError(t, conn.Close())
	return d
}

func TestPool_CloseJoinsTheErrorsOfEveryHandle(t *testing.T) {
	errR := errors.New("reader close failed")
	errW := errors.New("writer close failed")
	pool := db.Pool{W: openCloseFailing(t, errW), R: openCloseFailing(t, errR)}

	err := pool.Close()
	require.ErrorIs(t, err, errR, "a reader close failure must not be dropped")
	require.ErrorIs(t, err, errW)
}

func TestPool_CloseClosesAnAliasedHandleOnce(t *testing.T) {
	errW := errors.New("writer close failed")
	w := openCloseFailing(t, errW)
	require.ErrorIs(t, db.Pool{W: w, R: w}.Close(), errW)
}
