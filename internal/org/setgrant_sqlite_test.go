package org_test

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"altalune.id/opensheet/internal/apperror"

	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/capabilities"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/schema"
)

type projectIDsOf struct{ store project.Store }

func (p projectIDsOf) ProjectIDs(ctx context.Context, orgID uuid.UUID) ([]uuid.UUID, error) {
	list, err := p.store.List(tenant.WithOrg(ctx, orgID), orgID)
	if err != nil {
		return nil, err
	}
	out := make([]uuid.UUID, 0, len(list))
	for _, pr := range list {
		out = append(out, pr.ID)
	}
	return out, nil
}

func TestOrg_SetProjectGrant_SQLiteForeignProjectIsROL012(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "g.db")+"?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	conf := config.Defaults()
	require.NoError(t, schema.MigrateUp(context.Background(), sqlDB, conf))
	prefix := conf.DB.TablePrefix
	cfg, pool := db.DBConfig{Driver: db.DriverSQLite, TablePrefix: prefix}, db.Pool{W: sqlDB, R: sqlDB}
	store := org.NewStore(cfg, pool, nil)
	ctx := context.Background()
	owner, member := seedUser(t, sqlDB, prefix), seedUser(t, sqlDB, prefix)
	a, err := org.NewOrg("org-a", "A", owner)
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, a))
	b, err := org.NewOrg("org-b", "B", owner)
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, b))
	for _, m := range []struct {
		user uuid.UUID
		role org.Role
	}{{owner, org.RoleOwner}, {member, org.RoleMember}} {
		ms, err := org.NewMembership(a.ID, m.user, m.role)
		require.NoError(t, err)
		require.NoError(t, store.SaveMembership(ctx, ms))
	}
	own, foreign := seedProject(t, sqlDB, prefix, a.ID, owner), seedProject(t, sqlDB, prefix, b.ID, owner)
	projects := project.NewStore(cfg, pool, nil)
	unexpected := func(_ context.Context, msg string, cause error, _ ...any) *apperror.AppError {
		return apperror.New("opensheet.unexpected", msg, codes.Internal).WithCause(cause)
	}
	svc := org.NewService(store, capabilities.Capabilities{}, slog.New(slog.NewTextHandler(io.Discard, nil)), unexpected,
		tenant.NewUnitOfWork(cfg, pool, nil), org.WithProjects(projectIDsOf{store: projects}))
	as := boundAs(t, a.ID, owner, org.RoleOwner)

	err = svc.SetProjectGrant(as, a.ID, member, org.ProjectGrant{ProjectIDs: []uuid.UUID{own, foreign}})
	require.True(t, org.IsProjectGrantEscalationError(err), "%T %v", err, err)
	require.NoError(t, svc.SetProjectGrant(as, a.ID, member, org.ProjectGrant{ProjectIDs: []uuid.UUID{own}}))
	got, err := store.MembershipOf(ctx, a.ID, member)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{own}, got.ProjectIDs)

	require.NoError(t, svc.SetRole(as, a.ID, member, org.RoleOwner, uuid.Nil), "promoting a restricted member to owner must not trip the owner CHECK")
	got, err = store.MembershipOf(ctx, a.ID, member)
	require.NoError(t, err)
	require.Equal(t, org.RoleOwner, got.Role)
	require.True(t, got.AllProjects)
	require.Empty(t, got.ProjectIDs)
}
