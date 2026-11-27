package controlplane

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authv1 "altalune.id/opensheet/gen/go/auth/v1"
	projectv1 "altalune.id/opensheet/gen/go/project/v1"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/testutil/fakes"
)

type orgDir struct{ orgs []*org.Org }

func (d orgDir) List(context.Context, uuid.UUID) ([]*org.Org, error) { return d.orgs, nil }

func (d orgDir) ByID(_ context.Context, id uuid.UUID) (*org.Org, error) {
	for _, o := range d.orgs {
		if o.ID == id {
			return o, nil
		}
	}
	return nil, &org.NotFoundError{}
}

type bindFixture struct {
	roles    *fakes.Roles
	rz       *authz.Resolver
	projects *project.Service
	orgs     orgDir
	user     uuid.UUID
	orgA     *org.Org
	orgB     *org.Org
	projA    *project.Project
	projB    *project.Project
}

func newBindFixture(t *testing.T) *bindFixture {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reporter := apperror.NewReporter(log, false)
	store := fakes.NewProject()
	f := &bindFixture{
		roles:    fakes.NewRoles(),
		projects: project.NewService(store, log, reporter.Unexpected),
		user:     uuid.New(),
		orgA:     &org.Org{ID: uuid.New(), Slug: "orga", Name: "A"},
		orgB:     &org.Org{ID: uuid.New(), Slug: "orgb", Name: "B"},
	}
	f.rz = authz.NewResolver(f.roles)
	f.orgs = orgDir{orgs: []*org.Org{f.orgA, f.orgB}}
	f.roles.Set(f.orgA.ID, f.user, "member").Set(f.orgB.ID, f.user, "member")
	for _, o := range []*org.Org{f.orgA, f.orgB} {
		p, err := project.New(o.ID, o.Slug+"-project", o.Name)
		require.NoError(t, err)
		require.NoError(t, store.Save(context.Background(), p))
		if o == f.orgA {
			f.projA = p
		} else {
			f.projB = p
		}
	}
	return f
}

func boundOrg(ctx context.Context) uuid.UUID { return session.PrincipalFrom(ctx).HeldOrgID }

func TestControlPlane_PrincipalBinds(t *testing.T) {
	f := newBindFixture(t)
	person := session.Principal{UserID: f.user, Source: session.SourceToken, ActiveOrgID: f.orgA.ID, Scopes: authn.AllScopes()}
	key := session.Principal{KeyID: uuid.New(), Source: session.SourceAPIKey, ActiveOrgID: f.orgA.ID, AllProjects: true, Scopes: authn.AllScopes()}

	for name, p := range map[string]session.Principal{"person": person, "key": key} {
		t.Run(name, func(t *testing.T) {
			ctx := session.PrincipalInto(context.Background(), p)

			pctx, got, err := principal(ctx, f.rz)
			require.NoError(t, err)
			assert.Equal(t, f.orgA.ID, boundOrg(pctx))
			assert.Equal(t, f.orgA.ID, got.HeldOrgID)
			assert.NotEmpty(t, got.Held)

			sctx, proj, err := scopeToProject(ctx, f.rz, f.projects, f.orgs, f.projA.ID.String())
			require.NoError(t, err)
			assert.Equal(t, f.projA.ID, proj.ID)
			assert.Equal(t, f.orgA.ID, boundOrg(sctx))
			tc, err := tenant.From(sctx)
			require.NoError(t, err)
			assert.Equal(t, f.projA.ID, tc.ProjectID)
		})
	}
}

func TestControlPlane_PrincipalRefusesNonMember(t *testing.T) {
	f := newBindFixture(t)
	ctx := session.PrincipalInto(context.Background(), session.Principal{UserID: uuid.New(), Source: session.SourceToken, ActiveOrgID: f.orgA.ID})

	_, _, err := principal(ctx, f.rz)
	assert.True(t, authn.IsUnauthorizedError(err), "got %v", err)
}

func TestControlPlane_BindStoreFailureIsUnavailable(t *testing.T) {
	f := newBindFixture(t)
	f.roles.Err = errors.New("db down")
	ctx := session.PrincipalInto(context.Background(), session.Principal{UserID: f.user, Source: session.SourceToken, ActiveOrgID: f.orgA.ID})

	_, _, err := principal(ctx, f.rz)
	assert.True(t, authn.IsUnavailableError(err), "got %v", err)
}

func TestControlPlane_RepinnedOrgIsRebound(t *testing.T) {
	f := newBindFixture(t)
	ctx := session.PrincipalInto(context.Background(), session.Principal{UserID: f.user, Source: session.SourceToken, ActiveOrgID: f.orgA.ID, Scopes: authn.AllScopes()})

	rctx, p, proj, err := resolveProject(ctx, f.rz, f.projects, f.orgs, f.projB.ID.String())
	require.NoError(t, err)
	assert.Equal(t, f.projB.ID, proj.ID)
	assert.Equal(t, f.orgB.ID, boundOrg(rctx), "the re-pinned org is bound before any reach check")
	assert.Equal(t, f.orgB.ID, p.HeldOrgID)

	sctx, _, err := scopeToProject(ctx, f.rz, f.projects, f.orgs, f.projB.ID.String())
	require.NoError(t, err)
	assert.Equal(t, f.orgB.ID, boundOrg(sctx))
}

func TestControlPlane_TwoOrgTokenProjectRPCAndListProjects(t *testing.T) {
	f := newBindFixture(t)
	token := session.Principal{UserID: f.user, Source: session.SourceToken, Scopes: authn.AllScopes()}
	ctx := session.PrincipalInto(context.Background(), token)

	sctx, proj, err := scopeToProject(ctx, f.rz, f.projects, f.orgs, f.projB.ID.String())
	require.NoError(t, err)
	assert.Equal(t, f.projB.ID, proj.ID)
	assert.Equal(t, f.orgB.ID, boundOrg(sctx), "a project RPC binds to the project's org")

	resp, err := NewProjectService(f.rz, f.projects, f.orgs).ListProjects(ctx, connect.NewRequest(&projectv1.ListProjectsRequest{}))
	require.NoError(t, err)
	got := map[string]string{}
	for _, p := range resp.Msg.GetProjects() {
		got[p.GetId()] = p.GetOrgSlug()
	}
	assert.Equal(t, map[string]string{f.projA.ID.String(): "orga", f.projB.ID.String(): "orgb"}, got)
}

func TestControlPlane_ListProjectsBindsEachOrg(t *testing.T) {
	f := newBindFixture(t)
	f.roles.Set(f.orgB.ID, f.user, "")
	ctx := session.PrincipalInto(context.Background(), session.Principal{UserID: f.user, Source: session.SourceToken, Scopes: authn.AllScopes()})

	resp, err := NewProjectService(f.rz, f.projects, f.orgs).ListProjects(ctx, connect.NewRequest(&projectv1.ListProjectsRequest{}))
	require.NoError(t, err, "an org the person left mid-request is skipped, not fatal")
	var got []string
	for _, pb := range resp.Msg.GetProjects() {
		got = append(got, pb.GetId())
	}
	assert.Equal(t, []string{f.projA.ID.String()}, got, "an org listed for the person is bound before its projects are read")
}

func TestControlPlane_ListProjectsOutageFails(t *testing.T) {
	f := newBindFixture(t)
	f.roles.Err = errors.New("store down")
	ctx := session.PrincipalInto(context.Background(), session.Principal{UserID: f.user, Source: session.SourceToken, Scopes: authn.AllScopes()})

	_, err := NewProjectService(f.rz, f.projects, f.orgs).ListProjects(ctx, connect.NewRequest(&projectv1.ListProjectsRequest{}))
	assert.True(t, authn.IsUnavailableError(err), "want *authn.UnavailableError, got %T: %v", err, err)
}

func TestWhoami_MemberGetsIdentity(t *testing.T) {
	f := newBindFixture(t)
	ctx := session.PrincipalInto(context.Background(), session.Principal{UserID: f.user, Email: "m@example.com", Source: session.SourceToken, ActiveOrgID: f.orgA.ID})

	resp, err := NewAuthService(f.rz, nil).Whoami(ctx, connect.NewRequest(&authv1.WhoamiRequest{}))
	require.NoError(t, err)
	assert.Equal(t, f.user.String(), resp.Msg.GetUserId())
	assert.Equal(t, f.orgA.ID.String(), resp.Msg.GetActiveOrgId())
}

func TestWhoami_RemovedOwnerPersonalTokenRefused(t *testing.T) {
	f := newBindFixture(t)
	removed := uuid.New()
	f.roles.Set(f.orgA.ID, removed, "owner").Set(f.orgA.ID, removed, "")
	p := session.Principal{UserID: removed, Email: "gone@example.com", Source: session.SourceToken, ActiveOrgID: f.orgA.ID, Scopes: authn.AllScopes()}
	ctx := session.PrincipalInto(context.Background(), p)

	_, err := NewAuthService(f.rz, nil).Whoami(ctx, connect.NewRequest(&authv1.WhoamiRequest{}))
	assert.True(t, authn.IsUnauthorizedError(err), "a removed owner's token gets no identity; got %v", err)
}
