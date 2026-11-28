package project_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/testutil/fakes"
)

func idsOf(list []*project.Project) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(list))
	for _, p := range list {
		out = append(out, p.ID)
	}
	return out
}

func TestProject_ListShowsGrantOnly(t *testing.T) {
	orgID, owner, member := uuid.New(), uuid.New(), uuid.New()
	svc, _ := newTestService(t)
	web, err := svc.Create(tenantCtx(orgID, owner), orgID, "web", "Web")
	require.NoError(t, err)
	api, err := svc.Create(tenantCtx(orgID, owner), orgID, "api", "API")
	require.NoError(t, err)

	roles := fakes.NewRoles().Set(orgID, member, "member").Restrict(orgID, member, web.ID)
	restricted, err := authz.NewResolver(roles).Bind(context.Background(), session.Principal{UserID: member, Source: session.SourceOIDC}, tenant.Context{OrgID: orgID, UserID: member})
	require.NoError(t, err)
	got, err := svc.List(restricted, orgID)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{web.ID}, idsOf(got), "a restricted member lists only granted projects")

	got, err = svc.List(tenantCtx(orgID, owner), orgID)
	require.NoError(t, err)
	require.ElementsMatch(t, []uuid.UUID{web.ID, api.ID}, idsOf(got))

	got, err = svc.List(authz.AsSystem(tenant.Into(context.Background(), tenant.Context{OrgID: orgID})), orgID)
	require.NoError(t, err)
	require.ElementsMatch(t, []uuid.UUID{web.ID, api.ID}, idsOf(got), "the system lists every project")

	got, err = svc.List(tenant.Into(context.Background(), tenant.Context{OrgID: orgID, UserID: owner}), orgID)
	require.NoError(t, err)
	require.Empty(t, got, "a caller Bind never saw lists nothing")
}

func TestProject_NewProjectReachedByUnrestrictedOnly(t *testing.T) {
	orgID, owner, member := uuid.New(), uuid.New(), uuid.New()
	svc, _ := newTestService(t)
	web, err := svc.Create(tenantCtx(orgID, owner), orgID, "web", "Web")
	require.NoError(t, err)
	roles := fakes.NewRoles().Set(orgID, member, "member").Restrict(orgID, member, web.ID)
	bind := func() context.Context {
		ctx, err := authz.NewResolver(roles).Bind(context.Background(), session.Principal{UserID: member, Source: session.SourceOIDC}, tenant.Context{OrgID: orgID, UserID: member})
		require.NoError(t, err)
		return ctx
	}

	later, err := svc.Create(tenantCtx(orgID, owner), orgID, "later", "Later")
	require.NoError(t, err)
	got, err := svc.List(bind(), orgID)
	require.NoError(t, err)
	require.NotContains(t, idsOf(got), later.ID, "a new project is not added to a restricted grant")
	got, err = svc.List(tenantCtx(orgID, owner), orgID)
	require.NoError(t, err)
	require.Contains(t, idsOf(got), later.ID)
}
