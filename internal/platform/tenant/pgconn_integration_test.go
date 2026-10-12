//go:build integration

package tenant_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	pdb "altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/pgtest"
)

func TestTenantRunInTx_AppliesSetConfig(t *testing.T) {
	h := pgtest.New(t)
	sqlDB := h.OpenDB(t)

	pc := tenant.NewPgConn(sqlDB)
	orgID := uuid.New()
	tc := tenant.Context{OrgID: orgID, UserID: uuid.New()}

	var got string
	err := tenant.RunInTx(t.Context(), pc, tc, func(ctx context.Context) error {
		tx, ok := pdb.CurrentTx(ctx)
		require.True(t, ok, "expected tx to be enrolled in ctx")
		return tx.QueryRowContext(ctx, "SELECT current_setting('app.current_org_id', true)").Scan(&got)
	})
	require.NoError(t, err)
	require.Equal(t, orgID.String(), got)
}

func TestNewUnitOfWork_Postgres_AppliesSetConfig(t *testing.T) {
	h := pgtest.New(t)
	sqlDB := h.OpenDB(t)

	pc := tenant.NewPgConn(sqlDB)
	pool := pdb.Pool{W: sqlDB, R: sqlDB}
	uow := tenant.NewUnitOfWork(pdb.DBConfig{Driver: pdb.DriverPostgres}, pool, pc)

	orgID := uuid.New()
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: orgID, UserID: uuid.New()})

	var got string
	err := uow(ctx, func(ctx context.Context) error {
		tx, ok := pdb.CurrentTx(ctx)
		require.True(t, ok, "expected tx to be enrolled in ctx")
		return tx.QueryRowContext(ctx, "SELECT current_setting('app.current_org_id', true)").Scan(&got)
	})
	require.NoError(t, err)
	require.Equal(t, orgID.String(), got)
}

func TestNewUnitOfWork_Postgres_NoTenantReturnsTenantFromError(t *testing.T) {
	h := pgtest.New(t)
	sqlDB := h.OpenDB(t)

	pc := tenant.NewPgConn(sqlDB)
	pool := pdb.Pool{W: sqlDB, R: sqlDB}
	uow := tenant.NewUnitOfWork(pdb.DBConfig{Driver: pdb.DriverPostgres}, pool, pc)

	err := uow(t.Context(), func(context.Context) error {
		t.Fatal("fn must not run without tenant scope")
		return nil
	})
	require.True(t, tenant.IsMissingError(err))
}
