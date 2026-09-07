package tenant_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"altalune.id/template/internal/platform/db"
	"altalune.id/template/internal/platform/tenant"
)

type fakeOrgReader struct {
	ids []uuid.UUID
	err error
}

func (f fakeOrgReader) OrgIDs(context.Context) ([]uuid.UUID, error) { return f.ids, f.err }

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTenantsFixture(t *testing.T, ids []uuid.UUID) *tenant.Enumerator {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	_, err = sqlDB.ExecContext(t.Context(),
		`CREATE TABLE t_orgs (id TEXT PRIMARY KEY, created_at TEXT NOT NULL)`)
	require.NoError(t, err)
	for i, id := range ids {
		_, err = sqlDB.ExecContext(t.Context(),
			`INSERT INTO t_orgs (id, created_at) VALUES (?, ?)`,
			id.String(), time.Unix(int64(i), 0).UTC().Format(time.RFC3339Nano))
		require.NoError(t, err)
	}

	pool := db.Pool{W: sqlDB, R: sqlDB}
	return tenant.NewEnumerator(tenant.NewOrgReader(pool, db.DriverSQLite, "", "t_"), discardLogger())
}

func TestEnumerator_Each_BindsTenantContextInOrder(t *testing.T) {
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	tn := newTenantsFixture(t, ids)

	var seen []uuid.UUID
	err := tn.Each(t.Context(), func(ctx context.Context, tenantID string) error {
		tc, tErr := tenant.From(ctx)
		require.NoError(t, tErr, "Each must bind a tenant Context")
		require.Equal(t, tc.OrgID.String(), tenantID)
		require.Equal(t, uuid.Nil, tc.ProjectID)
		require.Equal(t, uuid.Nil, tc.UserID)
		seen = append(seen, tc.OrgID)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, ids, seen, "enumeration must follow created_at order")
}

func TestEnumerator_Each_NoTenantsIsNotAnError(t *testing.T) {
	tn := newTenantsFixture(t, nil)
	var calls int
	require.NoError(t, tn.Each(t.Context(), func(context.Context, string) error {
		calls++
		return nil
	}))
	require.Zero(t, calls)
}

func TestEnumerator_Each_PropagatesCallbackError(t *testing.T) {
	tn := newTenantsFixture(t, []uuid.UUID{uuid.New(), uuid.New()})
	var calls int
	err := tn.Each(t.Context(), func(context.Context, string) error {
		calls++
		return context.Canceled
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, calls, "Each aborts on the first callback error")
}

func TestEnumerator_Each_PropagatesReaderError(t *testing.T) {
	want := errors.New("reader down")
	tn := tenant.NewEnumerator(fakeOrgReader{err: want}, discardLogger())

	var calls int
	err := tn.Each(t.Context(), func(context.Context, string) error {
		calls++
		return nil
	})
	require.ErrorIs(t, err, want)
	require.Zero(t, calls, "a failed enumeration must not run any tenant callback")
}
