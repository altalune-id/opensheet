//go:build integration

package sheet_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/testutil/pgtest"
)

func TestPgUnitOfWork_JoinedUnitRunsAfterCommitHooksOnlyAtTheOuterCommit(t *testing.T) {
	h := pgtest.New(t)
	sqlDB := h.OpenDB(t)
	pc := tenant.NewPgConn(sqlDB)
	uow := sheet.NewUnitOfWork(db.DBConfig{Driver: db.DriverPostgres}, db.Pool{W: sqlDB, R: sqlDB}, pc)
	tc := tenant.Context{OrgID: uuid.New(), UserID: uuid.New()}

	cases := []struct {
		name     string
		outerErr error
		wantRun  int
	}{
		{name: "outer commit", wantRun: 1},
		{name: "outer rollback", outerErr: errors.New("rolled back")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ran := 0
			err := tenant.RunInTx(t.Context(), pc, tc, func(ctx context.Context) error {
				require.NoError(t, uow.Run(ctx, func(ctx context.Context) error {
					db.AfterCommit(ctx, func() { ran++ })
					return nil
				}))
				require.Zero(t, ran, "a joined unit ran the hook before the outer commit")
				return c.outerErr
			})
			require.ErrorIs(t, err, c.outerErr)
			require.Equal(t, c.wantRun, ran)
		})
	}
}
