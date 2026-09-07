//go:build integration

package tenant_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	pdb "altalune.id/template/internal/platform/db"
	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/internal/testutil/pgtest"
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
