package org_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/capabilities"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/schema"
)

func TestSQLite_SetRoleAndRemoveMemberDoNotWaitOnTheirOwnUnitOfWork(t *testing.T) {
	ctx := context.Background()
	cfg := config.Defaults()
	dbCfg := db.DBConfig{Driver: db.DriverSQLite, DSN: filepath.Join(t.TempDir(), "org.db"), TablePrefix: cfg.DB.TablePrefix}
	pool, err := db.OpenPool(ctx, dbCfg, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Close() })
	require.NoError(t, schema.MigrateUp(ctx, pool.W, cfg))

	store := org.NewStore(dbCfg, pool, nil)
	unexpected := func(_ context.Context, msg string, cause error, _ ...any) *apperror.AppError {
		return apperror.New("opensheet.unexpected", msg, codes.Internal, &apperrorv1.ErrorDetail{Code: "opensheet.unexpected"}).WithCause(cause)
	}
	svc := org.NewService(store, capabilities.Capabilities{}, slog.New(slog.NewTextHandler(io.Discard, nil)), unexpected, tenant.NewUnitOfWork(dbCfg, pool, nil))

	owner, member, other := seedUser(t, pool.W, cfg.DB.TablePrefix), seedUser(t, pool.W, cfg.DB.TablePrefix), seedUser(t, pool.W, cfg.DB.TablePrefix)
	o, err := org.NewOrg("uow", "UoW", owner)
	require.NoError(t, err)
	scoped := tenant.WithOrg(ctx, o.ID)
	require.NoError(t, store.Save(scoped, o))
	for id, role := range map[uuid.UUID]org.Role{owner: org.RoleOwner, member: org.RoleMember, other: org.RoleMember} {
		m, err := org.NewMembership(o.ID, id, role)
		require.NoError(t, err)
		require.NoError(t, store.SaveMembership(scoped, m))
	}
	asOwner := boundAs(t, o.ID, owner, org.RoleOwner)

	start := time.Now()
	require.NoError(t, svc.SetRole(asOwner, o.ID, member, org.RoleAdmin, uuid.Nil))
	require.NoError(t, svc.RemoveMember(asOwner, o.ID, other))
	require.Less(t, time.Since(start), 2*time.Second, "a store call outside the transaction waits out busy_timeout")

	m, err := store.MembershipOf(scoped, o.ID, member)
	require.NoError(t, err)
	require.Equal(t, org.RoleAdmin, m.Role)
	_, err = store.MembershipOf(scoped, o.ID, other)
	require.True(t, org.IsMembershipMissingError(err), "got %v", err)
}
