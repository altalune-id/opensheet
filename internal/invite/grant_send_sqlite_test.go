package invite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/invite"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/testutil/fakes"
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

func TestInvite_SQLiteForeignProjectIsROL012(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "i.db")+"?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	conf := config.Defaults()
	require.NoError(t, schema.MigrateUp(context.Background(), sqlDB, conf))
	cfg, pool := db.DBConfig{Driver: db.DriverSQLite, TablePrefix: conf.DB.TablePrefix}, db.Pool{W: sqlDB, R: sqlDB}

	sender, orgA := seedTenant(t, sqlDB, conf.DB.TablePrefix)
	_, orgB := seedTenant(t, sqlDB, conf.DB.TablePrefix)
	members := org.NewStore(cfg, pool, nil)
	m, err := org.NewMembership(orgA, sender, org.RoleAdmin)
	require.NoError(t, err)
	require.NoError(t, members.SaveMembership(context.Background(), m))
	own, foreign := seedInviteProject(t, sqlDB, orgA, sender), seedInviteProject(t, sqlDB, orgB, sender)

	store := invite.NewStore(cfg, pool, nil)
	unf := noopUnexpected(nil)
	send := invite.NewSendWorkflow(store, nopMailer{}, "https://example.test", newTestLogger(), unf)
	accept := invite.NewAcceptWorkflow(store, newFakeUsers(), newFakeOrgs(), newTestLogger(), unf)
	svc := invite.NewService(store, send, accept, true, newTestLogger(), unf,
		invite.WithMembers(members), invite.WithProjects(projectIDsOf{store: project.NewStore(cfg, pool, nil)}))
	tc := tenant.Context{OrgID: orgA, UserID: sender}
	ctx, err := authz.NewResolver(fakes.NewRoles().Set(orgA, sender, "admin")).Bind(context.Background(), session.Principal{UserID: sender, Source: session.SourceOIDC}, tc)
	require.NoError(t, err)

	_, err = svc.Send(ctx, invite.SendRequest{Email: "x@example.com", Role: invite.RoleMember, Grant: &org.ProjectGrant{ProjectIDs: []uuid.UUID{own, foreign}}})
	require.True(t, org.IsProjectGrantEscalationError(err), "%T %v", err, err)

	inv, err := svc.Send(ctx, invite.SendRequest{Email: "y@example.com", Role: invite.RoleMember, Grant: &org.ProjectGrant{ProjectIDs: []uuid.UUID{own}}})
	require.NoError(t, err)
	got, err := store.ByID(tenant.Into(context.Background(), tc), inv.ID)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{own}, got.ProjectIDs)
}
