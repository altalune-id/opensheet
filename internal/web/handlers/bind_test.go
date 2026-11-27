package handlers_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/invite"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
	"altalune.id/opensheet/internal/user"
	"altalune.id/opensheet/internal/web"
	"altalune.id/opensheet/internal/web/handlers"
)

type roleCall struct{ orgID, userID uuid.UUID }

type orgRoles struct {
	orgs  *org.Service
	mu    sync.Mutex
	calls []roleCall
}

func (a *orgRoles) RoleOf(ctx context.Context, orgID, userID uuid.UUID) (role string, roleID uuid.UUID, orgName string, grant session.Grant, err error) {
	a.mu.Lock()
	a.calls = append(a.calls, roleCall{orgID: orgID, userID: userID})
	a.mu.Unlock()
	return fakes.OrgRoles{Orgs: a.orgs}.RoleOf(ctx, orgID, userID)
}

func (a *orgRoles) CustomPermissions(ctx context.Context, roleID uuid.UUID) ([]authz.Permission, error) {
	return fakes.OrgRoles{Orgs: a.orgs}.CustomPermissions(ctx, roleID)
}

func (a *orgRoles) resolved(orgID, userID uuid.UUID) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, c := range a.calls {
		if c.orgID == orgID && c.userID == userID {
			return true
		}
	}
	return false
}

func assertBoundTo(t *testing.T, r *http.Request, orgID uuid.UUID) {
	t.Helper()
	got := session.PrincipalFrom(r.Context())
	assert.Equal(t, orgID, got.HeldOrgID, "held set resolved in the path's org")
	assert.Equal(t, orgID, got.ActiveOrgID, "a person is pinned to the org it was bound in")
	assert.NotEmpty(t, got.Held)
	tc, err := tenant.From(r.Context())
	require.NoError(t, err)
	assert.Equal(t, orgID, tc.OrgID)
}

func TestRequireOrg_ReturnsBoundContext(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	uid := uuid.New()
	acme := f.seedOrg(t, "acme", uid)

	var got *http.Request
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orgs/{org}/probe", func(w http.ResponseWriter, r *http.Request) {
		sc, ok := f.Deps.RequireOrg(w, r)
		if !ok {
			return
		}
		got = handlers.OrgScopeRequest(sc)
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/orgs/acme/probe", "", session.Principal{UserID: uid}))

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, got)
	assertBoundTo(t, got, acme.ID)
	assert.Equal(t, "owner", session.PrincipalFrom(got.Context()).HeldRole)
}

func TestRequireProject_ReturnsBoundContext(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	uid := uuid.New()
	acme := f.seedOrg(t, "acme", uid)
	alpha, err := f.Projects.Create(setTenant(context.Background(), acme.ID, uid), acme.ID, "alpha", "Alpha")
	require.NoError(t, err)

	var got *http.Request
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orgs/{org}/projects/{project}/probe", func(w http.ResponseWriter, r *http.Request) {
		sc, ok := f.Deps.RequireProject(w, r)
		if !ok {
			return
		}
		got = handlers.ProjectScopeRequest(sc)
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/orgs/acme/projects/alpha/probe", "", session.Principal{UserID: uid}))

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, got)
	assertBoundTo(t, got, acme.ID)
	tc, err := tenant.From(got.Context())
	require.NoError(t, err)
	assert.Equal(t, alpha.ID, tc.ProjectID)
}

func TestRequireOrg_NonMemberIs404(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.seedOrg(t, "rival", uuid.New())

	reached := false
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orgs/{org}/probe", func(w http.ResponseWriter, r *http.Request) {
		_, reached = f.Deps.RequireOrg(w, r)
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/orgs/rival/probe", "", session.Principal{UserID: uuid.New()}))

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.False(t, reached)
}

func TestRequireProject_PersonPinnedToOtherOrg(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	uid := uuid.New()
	orgA := f.seedOrg(t, "orga", uid)
	orgB := f.seedOrg(t, "orgb", uid)
	projA, err := f.Projects.Create(setTenant(context.Background(), orgA.ID, uid), orgA.ID, "alpha", "Alpha")
	require.NoError(t, err)
	_, err = f.Projects.Create(setTenant(context.Background(), orgB.ID, uid), orgB.ID, "beta", "Beta")
	require.NoError(t, err)

	mux := http.NewServeMux()
	handlers.NewTodoHandler(f.Deps, f.Projects, f.Todos).Register(mux)
	rec := httptest.NewRecorder()
	pinned := session.Principal{UserID: uid, ActiveOrgID: orgA.ID, ActiveProjectID: projA.ID}
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/orgs/orgb/projects/beta/todos", "", pinned))

	assert.Equal(t, http.StatusOK, rec.Code, "a member reaches another of their orgs whatever the session last pinned")
}

func TestMemberOrgScopes_EachScopeBound(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	uid := uuid.New()
	f.seedOrg(t, "orga", uid)
	f.seedOrg(t, "orgb", uid)
	p := session.Principal{UserID: uid}
	r := httptest.NewRequest(http.MethodGet, "/settings/tokens", nil)

	scopes, err := f.Deps.MemberOrgScopes(r, p, "sid")
	require.NoError(t, err)
	require.Len(t, scopes, 2)
	for _, sc := range scopes {
		assertBoundTo(t, handlers.OrgScopeRequest(sc), handlers.OrgScopeOrgID(sc))
	}
}

func TestMemberOrgScopes_SkipsOrgLeftMidRequest(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	uid := uuid.New()
	stay := f.seedOrg(t, "orga", uid)
	f.seedOrg(t, "orgb", uid)
	d := f.Deps
	d.Authz = authz.NewResolver(fakes.NewRoles().Set(stay.ID, uid, "owner"))
	r := httptest.NewRequest(http.MethodGet, "/settings/tokens", nil)

	scopes, err := d.MemberOrgScopes(r, session.Principal{UserID: uid}, "sid")
	require.NoError(t, err)
	require.Len(t, scopes, 1)
	assert.Equal(t, stay.ID, handlers.OrgScopeOrgID(scopes[0]))
}

func TestMemberOrgScopes_OutageFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	uid := uuid.New()
	f.seedOrg(t, "orga", uid)
	src := fakes.NewRoles()
	src.Err = errors.New("store down")
	d := f.Deps
	d.Authz = authz.NewResolver(src)
	r := httptest.NewRequest(http.MethodGet, "/settings/tokens", nil)

	_, err := d.MemberOrgScopes(r, session.Principal{UserID: uid}, "sid")
	assert.True(t, authn.IsUnavailableError(err), "want *authn.UnavailableError, got %T: %v", err, err)
}

func TestLayoutSwitcher_ListsFromBoundContext(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	uid := uuid.New()
	acme := f.seedOrg(t, "acme", uid)
	_, err := f.Projects.Create(setTenant(context.Background(), acme.ID, uid), acme.ID, "alpha", "Alpha")
	require.NoError(t, err)
	p := session.Principal{UserID: uid}
	r := httptest.NewRequest(http.MethodGet, "/orgs/acme", nil)
	r = r.WithContext(session.PrincipalInto(r.Context(), p))

	l := f.Deps.LayoutForOrg(r, "Overview", "acme", "overview")

	require.Len(t, l.OtherProjects, 1, "the switcher lists the org's projects")
	assert.True(t, f.Roles.resolved(acme.ID, uid), "the switcher lists projects on a context bound to the pinned org")
}

func TestSignIn_DefaultProjectFromBoundList(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u, err := f.Users.Create(context.Background(), user.CreateRequest{Email: "a@b.co", Name: "A", Source: user.SourceLocal, Password: "secret12"})
	require.NoError(t, err)
	acme := f.seedOrg(t, "acme", u.ID)
	alpha, err := f.Projects.Create(setTenant(context.Background(), acme.ID, u.ID), acme.ID, "alpha", "Alpha")
	require.NoError(t, err)

	h := handlers.NewAuthHandler(f.Deps, newAuthSvc(t, f), f.Users, f.Orgs, f.Projects, nil, nil)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("email=a@b.co&password=secret12"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(rec, r)

	require.Equal(t, http.StatusSeeOther, rec.Code)
	saved := savedPrincipal(t, f, rec)
	assert.Equal(t, acme.ID, saved.ActiveOrgID)
	assert.Equal(t, alpha.ID, saved.ActiveProjectID)
	assert.Empty(t, saved.Held, "a held set is never written to the session")
	assert.True(t, f.Roles.resolved(acme.ID, u.ID), "the default project comes from a list bound to the org")
}

func TestInviteAccept_DefaultProjectFromBoundList(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	owner := uuid.New()
	acme := f.seedOrg(t, "acme", owner)
	alpha, err := f.Projects.Create(setTenant(context.Background(), acme.ID, owner), acme.ID, "alpha", "Alpha")
	require.NoError(t, err)
	joiner, err := f.Users.Create(context.Background(), user.CreateRequest{Email: "j@b.co", Name: "J", Source: user.SourceLocal, Password: "secret12"})
	require.NoError(t, err)

	const token = "invite-token"
	now := time.Now().UTC()
	require.NoError(t, f.InvStore.Save(context.Background(), &invite.Invite{
		ID: uuid.New(), OrgID: acme.ID, Email: "j@b.co", Role: invite.Role("member"),
		TokenHash: invite.HashToken(token), ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	}))
	accept := invite.NewAcceptWorkflow(f.InvStore, inviteUsers{f.UserStore}, inviteOrgs{f.OrgStore}, discardLogger(), passthroughUnexpected())
	invites := invite.NewService(f.InvStore, nil, accept, true, discardLogger(), passthroughUnexpected())

	mux := http.NewServeMux()
	handlers.NewInviteHandler(f.Deps, f.Orgs, invites).Register(mux)
	rec := httptest.NewRecorder()
	req := f.authedRequest(t, http.MethodGet, "/invites/accept?token="+url.QueryEscape(token), "", session.Principal{UserID: joiner.ID, Email: joiner.Email, Name: joiner.Name})
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusSeeOther, rec.Code, "body=%s", rec.Body.String())
	saved := savedPrincipal(t, f, nil, req)
	assert.Equal(t, acme.ID, saved.ActiveOrgID)
	assert.Equal(t, alpha.ID, saved.ActiveProjectID)
	assert.Empty(t, saved.Held, "a held set is never written to the session")
	assert.True(t, f.Roles.resolved(acme.ID, joiner.ID), "the default project comes from a list bound to the joined org")
}

func savedPrincipal(t *testing.T, f *handlerFixture, rec *httptest.ResponseRecorder, reqs ...*http.Request) session.Principal {
	t.Helper()
	var value string
	if rec != nil {
		c := cookieNamed(rec, web.SessionCookieName)
		require.NotNil(t, c)
		value = c.Value
	}
	for _, r := range reqs {
		c, err := r.Cookie(web.SessionCookieName)
		require.NoError(t, err)
		value = c.Value
	}
	sid, err := web.VerifyCookie(f.Deps.Key(web.PurposeSession), value)
	require.NoError(t, err)
	p, ok, err := f.Sessions.Load(context.Background(), sid)
	require.NoError(t, err)
	require.True(t, ok)
	return p
}

type inviteUsers struct{ users *fakes.User }

func (a inviteUsers) ByEmail(ctx context.Context, email string) (*invite.UserRef, error) {
	u, err := a.users.ByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	return &invite.UserRef{ID: u.ID, Email: u.Email, Name: u.Name}, nil
}

func (a inviteUsers) Save(ctx context.Context, u *invite.UserRef) error {
	return a.users.Save(ctx, &user.User{ID: u.ID, Email: u.Email, Name: u.Name})
}

type inviteOrgs struct{ orgs *fakes.Org }

func (a inviteOrgs) ByID(ctx context.Context, id uuid.UUID) (*invite.OrgRef, error) {
	o, err := a.orgs.ByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return &invite.OrgRef{ID: o.ID, Slug: o.Slug, Name: o.Name}, nil
}

func (a inviteOrgs) SaveMembership(ctx context.Context, m *invite.MembershipRef) error {
	return a.orgs.SaveMembership(ctx, &org.Membership{OrgID: m.OrgID, UserID: m.UserID, Role: org.Role(m.Role)})
}

func (a inviteOrgs) MembershipOf(ctx context.Context, orgID, userID uuid.UUID) (*invite.MembershipRef, error) {
	m, err := a.orgs.MembershipOf(ctx, orgID, userID)
	if org.IsMembershipMissingError(err) {
		return nil, &invite.MembershipMissingError{}
	}
	if err != nil {
		return nil, err
	}
	return &invite.MembershipRef{OrgID: m.OrgID, UserID: m.UserID, Role: invite.Role(m.Role)}, nil
}
