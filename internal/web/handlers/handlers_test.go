package handlers_test

import (
	"context"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/template/gen/go/apperror/v1"
	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/auth"
	"altalune.id/template/internal/i18n"
	"altalune.id/template/internal/invite"
	"altalune.id/template/internal/onboard"
	"altalune.id/template/internal/org"
	"altalune.id/template/internal/platform/capabilities"
	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/internal/project"
	"altalune.id/template/internal/testutil/fakes"
	"altalune.id/template/internal/todo"
	"altalune.id/template/internal/user"
	"altalune.id/template/internal/web"
	"altalune.id/template/internal/web/handlers"
	"altalune.id/template/mailer"
)

func discardLogger() *slog.Logger   { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func discardStdLogger() *log.Logger { return log.New(io.Discard, "", 0) }

func passthroughUnexpected() apperror.UnexpectedFunc {
	return func(_ context.Context, _ string, cause error, _ ...any) *apperror.AppError {
		return apperror.New("altempl.unexpected", "unexpected", codes.Internal,
			&apperrorv1.ErrorDetail{Code: "altempl.unexpected"}).WithCause(cause)
	}
}

type handlerFixture struct {
	Deps      handlers.Deps
	Cfg       *config.Config
	Sessions  session.Store
	Users     *user.Service
	UserStore *fakes.User
	Orgs      *org.Service
	OrgStore  *fakes.Org
	Projects  *project.Service
	ProjStore *fakes.Project
	Todos     *todo.Service
	TodoStore *fakes.Todo
	Onboards  *onboard.Service
	OnboStore *fakes.Onboard
	Invites   *invite.Service
	InvStore  *fakes.Invite
}

func newFixture(t *testing.T) *handlerFixture {
	t.Helper()
	cfg := &config.Config{}
	cfg.HTTP.BasePath = ""
	cfg.HTTP.StateSecret = "0123456789abcdef0123456789abcdef"
	cfg.HTTP.BaseURL = "http://localhost"
	caps := capabilities.Capabilities{OrgCreation: true, LocalIdentity: true}
	sessions := session.NewMemoryStore()

	userStore := fakes.NewUser()
	users := user.NewService(userStore, user.GenesisConfig{}, discardLogger(), passthroughUnexpected())

	orgStore := fakes.NewOrg()
	orgs := org.NewService(orgStore, caps, discardLogger(), passthroughUnexpected())

	projStore := fakes.NewProject()
	projects := project.NewService(projStore, discardLogger(), passthroughUnexpected())

	todoStore := fakes.NewTodo()
	todos := todo.NewService(todoStore, discardLogger(), passthroughUnexpected())

	onbStore := fakes.NewOnboard()
	onboards := onboard.NewService(onbStore, discardLogger(), passthroughUnexpected())

	invStore := fakes.NewInvite()
	invites := invite.NewService(invStore, nil, nil, true, discardLogger(), passthroughUnexpected())

	deps := handlers.Deps{
		Cfg:      cfg,
		Caps:     caps,
		Sessions: sessions,
		Logger:   discardStdLogger(),
		Orgs:     orgs,
		Projects: projects,
	}
	return &handlerFixture{
		Deps: deps, Cfg: cfg, Sessions: sessions,
		Users: users, UserStore: userStore,
		Orgs: orgs, OrgStore: orgStore,
		Projects: projects, ProjStore: projStore,
		Todos: todos, TodoStore: todoStore,
		Onboards: onboards, OnboStore: onbStore,
		Invites: invites, InvStore: invStore,
	}
}

func (f *handlerFixture) seedSession(t *testing.T, p session.Principal) string {
	t.Helper()
	sid, err := web.NewSID()
	require.NoError(t, err)
	require.NoError(t, f.Sessions.Save(context.Background(), sid, p, time.Now().Add(web.SessionTTL)))
	return web.SignCookie([]byte(f.Cfg.HTTP.StateSecret), sid)
}

func (f *handlerFixture) authedRequest(t *testing.T, method, target string, body string, p session.Principal) *http.Request {
	t.Helper()
	cookie := f.seedSession(t, p)
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	r.AddCookie(&http.Cookie{Name: web.SessionCookieName, Value: cookie})
	r = r.WithContext(session.PrincipalInto(r.Context(), p))
	return r
}

func TestSanitizeReturnTo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"empty", "", ""},
		{"root allowed", "/", "/"},
		{"absolute allowed", "/dashboard", "/dashboard"},
		{"scheme relative rejected", "//evil.com", ""},
		{"windows slash trick rejected", "/\\evil.com", ""},
		{"no slash rejected", "foo", ""},
		{"backslash normalized", "/foo\\bar", "/foo/bar"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, handlers.SanitizeReturnTo(tc.raw))
		})
	}
}

func TestResolveReturnTo(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "/dashboard", handlers.ResolveReturnTo("", "/dashboard"))
	assert.Equal(t, "/", handlers.ResolveReturnTo("", ""))
	assert.Equal(t, "/", handlers.ResolveReturnTo("", "//evil.com"))
	assert.Equal(t, "/app/dashboard", handlers.ResolveReturnTo("/app", "/dashboard"))
	assert.Equal(t, "/app/", handlers.ResolveReturnTo("/app", "//evil"))
}

func TestHome_GetHome_RedirectsUnauth(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	h := handlers.NewHomeHandler(f.Deps, f.Orgs, f.Projects)
	mux := http.NewServeMux()
	h.Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/login", rec.Header().Get("Location"))
}

// TestHome_GetRoot_RedirectsToTheLastUsedOrg covers the new root: it names no org, so it forwards to one.
func TestHome_GetRoot_RedirectsToTheLastUsedOrg(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	u, err := f.Users.Create(ctx, user.CreateRequest{Email: "root@b.co", Name: "A", Source: user.SourceLocal})
	require.NoError(t, err)
	o, err := f.Orgs.Create(ctx, org.CreateRequest{Slug: "acme", Name: "Acme", OwnerID: u.ID})
	require.NoError(t, err)

	h := handlers.NewHomeHandler(f.Deps, f.Orgs, f.Projects)
	mux := http.NewServeMux()
	h.Register(mux)

	p := session.Principal{UserID: u.ID, ActiveOrgID: o.ID, IssuedAt: time.Now().UTC()}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/", "", p))

	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/orgs/acme", rec.Header().Get("Location"))
}

func TestHome_GetOverview_RendersForAuthedUser(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	u, err := f.Users.Create(ctx, user.CreateRequest{Email: "a@b.co", Name: "A", Source: user.SourceLocal})
	require.NoError(t, err)
	o, err := f.Orgs.Create(ctx, org.CreateRequest{Slug: "acme", Name: "Acme", OwnerID: u.ID})
	require.NoError(t, err)

	h := handlers.NewHomeHandler(f.Deps, f.Orgs, f.Projects)
	mux := http.NewServeMux()
	h.Register(mux)

	p := session.Principal{UserID: u.ID, ActiveOrgID: o.ID, IssuedAt: time.Now().UTC()}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/orgs/acme", "", p))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Acme")
}

func TestOrgHandler_GetList_Unauth(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	h := handlers.NewOrgHandler(f.Deps, f.Orgs)
	mux := http.NewServeMux()
	h.Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/orgs", nil))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/login", rec.Header().Get("Location"))
}

func TestOrgHandler_GetList_Authed(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	u, err := f.Users.Create(ctx, user.CreateRequest{Email: "a@b.co", Name: "A", Source: user.SourceLocal})
	require.NoError(t, err)
	o, err := f.Orgs.Create(ctx, org.CreateRequest{Slug: "acme", Name: "Acme", OwnerID: u.ID})
	require.NoError(t, err)

	h := handlers.NewOrgHandler(f.Deps, f.Orgs)
	mux := http.NewServeMux()
	h.Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/orgs", "", session.Principal{UserID: u.ID, ActiveOrgID: o.ID}))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Acme")
}

func TestOrgHandler_GetNew_ForbiddenWhenCapabilityDisabled(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.Deps.Caps = capabilities.Capabilities{}
	h := handlers.NewOrgHandler(f.Deps, f.Orgs)
	mux := http.NewServeMux()
	h.Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/orgs/new", "", session.Principal{UserID: uuid.New()}))
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestOrgHandler_GetNew_OK(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	h := handlers.NewOrgHandler(f.Deps, f.Orgs)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/orgs/new", "", session.Principal{UserID: uuid.New()}))
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestOrgHandler_PostCreate_HappyPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	h := handlers.NewOrgHandler(f.Deps, f.Orgs)
	mux := http.NewServeMux()
	h.Register(mux)

	uid := uuid.New()
	rec := httptest.NewRecorder()
	body := "slug=acme&name=Acme"
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/orgs", body, session.Principal{UserID: uid}))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/orgs/acme/projects", rec.Header().Get("Location"))
}

func TestOrgHandler_PostCreate_DuplicateSlug(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	uid := uuid.New()
	_, err := f.Orgs.Create(ctx, org.CreateRequest{Slug: "acme", Name: "Acme", OwnerID: uid})
	require.NoError(t, err)

	h := handlers.NewOrgHandler(f.Deps, f.Orgs)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/orgs", "slug=acme&name=Other", session.Principal{UserID: uid}))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "already taken")
}

func TestOrgHandler_GetShow_MissingOrg(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	h := handlers.NewOrgHandler(f.Deps, f.Orgs)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/orgs/ghost", "", session.Principal{UserID: uuid.New()}))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestOrgHandler_PostRename_HappyPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	uid := uuid.New()
	_, err := f.Orgs.Create(ctx, org.CreateRequest{Slug: "acme", Name: "Acme", OwnerID: uid})
	require.NoError(t, err)

	h := handlers.NewOrgHandler(f.Deps, f.Orgs)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/orgs/acme/rename", "name=New+Name", session.Principal{UserID: uid}))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
}

func TestOrgHandler_PostRename_MissingOrg(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	h := handlers.NewOrgHandler(f.Deps, f.Orgs)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/orgs/ghost/rename", "name=New", session.Principal{UserID: uuid.New()}))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestProjectHandler_GetList_UnauthRedirect(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	h := handlers.NewProjectHandler(f.Deps, f.Projects)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/orgs/acme/projects", nil))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
}

// TestProjectHandler_GetNew_UnknownOrgIs404 replaces the old active-org precondition: the org now comes
// from the path, so an org the caller does not belong to is indistinguishable from one that does not exist.
func TestProjectHandler_GetNew_UnknownOrgIs404(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	h := handlers.NewProjectHandler(f.Deps, f.Projects)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/orgs/ghost/projects/new", "", session.Principal{UserID: uuid.New()}))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestProjectHandler_PostCreate_HappyPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	uid := uuid.New()
	o := f.seedOrg(t, "acme", uid)
	h := handlers.NewProjectHandler(f.Deps, f.Projects)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/orgs/acme/projects", "slug=alpha&name=Alpha", session.Principal{UserID: uid, ActiveOrgID: o.ID}))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/orgs/acme/projects/alpha/overview", rec.Header().Get("Location"))
}

func TestProjectHandler_PostCreate_DuplicateSlug(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	uid := uuid.New()
	o := f.seedOrg(t, "acme", uid)
	ctx := context.Background()
	_, err := f.Projects.Create(setTenant(ctx, o.ID, uid), o.ID, "alpha", "Alpha")
	require.NoError(t, err)

	h := handlers.NewProjectHandler(f.Deps, f.Projects)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/orgs/acme/projects", "slug=alpha&name=Alpha2", session.Principal{UserID: uid, ActiveOrgID: o.ID}))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "already taken")
}

func TestProjectHandler_PostRename_MissingProject(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	h := handlers.NewProjectHandler(f.Deps, f.Projects)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/orgs/ghost/projects/ghost/rename", "name=New", session.Principal{UserID: uuid.New(), ActiveOrgID: uuid.New()}))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestTodoHandler_UnauthRedirects(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	h := handlers.NewTodoHandler(f.Deps, f.Projects, f.Todos)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/orgs/acme/projects/alpha/todos", nil))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
}

func TestTodoHandler_GetTodos_MissingProject(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	h := handlers.NewTodoHandler(f.Deps, f.Projects, f.Todos)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/orgs/ghost/projects/ghost/todos", "", session.Principal{UserID: uuid.New(), ActiveOrgID: uuid.New()}))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestTodoHandler_HappyPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	uid := uuid.New()
	o := f.seedOrg(t, "acme", uid)
	ctx := setTenant(context.Background(), o.ID, uid)
	proj, err := f.Projects.Create(ctx, o.ID, "alpha", "Alpha")
	require.NoError(t, err)

	h := handlers.NewTodoHandler(f.Deps, f.Projects, f.Todos)
	mux := http.NewServeMux()
	h.Register(mux)

	p := session.Principal{UserID: uid, ActiveOrgID: o.ID, ActiveProjectID: proj.ID}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/orgs/acme/projects/alpha/todos", "title=milk", p))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "milk")

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/orgs/acme/projects/alpha/todos", "", p))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "milk")

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/orgs/acme/projects/alpha/overview", "", p))
	assert.Equal(t, http.StatusOK, rec.Code)

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/orgs/acme/projects/alpha/todos/clear", "", p))
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestTodoHandler_ToggleAndDelete(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	uid := uuid.New()
	o := f.seedOrg(t, "acme", uid)
	ctx := setTenant(context.Background(), o.ID, uid)
	proj, err := f.Projects.Create(ctx, o.ID, "alpha", "Alpha")
	require.NoError(t, err)
	tctx := setTenantProject(ctx, o.ID, proj.ID, uid)
	td, err := f.Todos.Create(tctx, "milk")
	require.NoError(t, err)

	h := handlers.NewTodoHandler(f.Deps, f.Projects, f.Todos)
	mux := http.NewServeMux()
	h.Register(mux)

	p := session.Principal{UserID: uid, ActiveOrgID: o.ID, ActiveProjectID: proj.ID}
	base := "/orgs/acme/projects/alpha/todos/"

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, base+td.ID.String()+"/toggle", "", p))
	assert.Equal(t, http.StatusOK, rec.Code)

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodDelete, base+td.ID.String(), "", p))
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestTodoHandler_ToggleBadID(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	uid := uuid.New()
	o := f.seedOrg(t, "acme", uid)
	_, err := f.Projects.Create(setTenant(context.Background(), o.ID, uid), o.ID, "alpha", "Alpha")
	require.NoError(t, err)
	h := handlers.NewTodoHandler(f.Deps, f.Projects, f.Todos)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/orgs/acme/projects/alpha/todos/not-a-uuid/toggle", "", session.Principal{UserID: uid, ActiveOrgID: o.ID}))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTodoHandler_ToggleNotFound(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	uid := uuid.New()
	o := f.seedOrg(t, "acme", uid)
	_, err := f.Projects.Create(setTenant(context.Background(), o.ID, uid), o.ID, "alpha", "Alpha")
	require.NoError(t, err)
	h := handlers.NewTodoHandler(f.Deps, f.Projects, f.Todos)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/orgs/acme/projects/alpha/todos/"+uuid.New().String()+"/toggle", "", session.Principal{UserID: uid, ActiveOrgID: o.ID}))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestInviteHandler_GetAccept_MissingToken(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	h := handlers.NewInviteHandler(f.Deps, f.Orgs, f.Invites)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/invites/accept", nil))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestInviteHandler_GetAccept_UnauthStoresCookieAndRedirects(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	h := handlers.NewInviteHandler(f.Deps, f.Orgs, f.Invites)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/invites/accept?token=abc", nil))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/login", rec.Header().Get("Location"))
	assert.Contains(t, rec.Header().Get("Set-Cookie"), web.InviteCookieName)
}

func TestInviteHandler_GetList_UnauthRedirects(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	h := handlers.NewInviteHandler(f.Deps, f.Orgs, f.Invites)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/orgs/acme/invites", nil))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
}

func TestInviteHandler_GetList_MissingOrg(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	h := handlers.NewInviteHandler(f.Deps, f.Orgs, f.Invites)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/orgs/ghost/invites", "", session.Principal{UserID: uuid.New()}))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestInviteHandler_PostSend_UnauthRedirects(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	h := handlers.NewInviteHandler(f.Deps, f.Orgs, f.Invites)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/orgs/acme/invites", nil))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
}

func TestInviteHandler_PostRevoke_MissingOrg(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	h := handlers.NewInviteHandler(f.Deps, f.Orgs, f.Invites)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/orgs/ghost/invites/"+uuid.New().String()+"/revoke", "", session.Principal{UserID: uuid.New()}))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestOnboardHandler_Register_MountsRoutes(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	req := &atomicBoolWrapper{}
	req.b.Store(true)
	h := handlers.NewOnboardHandler(f.Deps, f.Users, f.Orgs, f.Projects, f.Onboards, &req.b, "")
	mux := http.NewServeMux()
	h.Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/onboard", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "First-time setup")
}

func TestOnboardHandler_GetOnboard_RedirectsWhenNotRequired(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	req := &atomicBoolWrapper{}
	h := handlers.NewOnboardHandler(f.Deps, f.Users, f.Orgs, f.Projects, f.Onboards, &req.b, "")
	mux := http.NewServeMux()
	h.Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/onboard", nil))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
}

func TestOnboardHandler_PostLocal_MissingFields(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	req := &atomicBoolWrapper{}
	req.b.Store(true)
	h := handlers.NewOnboardHandler(f.Deps, f.Users, f.Orgs, f.Projects, f.Onboards, &req.b, "")
	mux := http.NewServeMux()
	h.Register(mux)

	body := "email=&name=&password=&org_slug=&org_name="
	r := httptest.NewRequest(http.MethodPost, "/onboard/local", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Enter your email")
}

func TestOnboardHandler_PostLocal_HappyPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	req := &atomicBoolWrapper{}
	req.b.Store(true)
	h := handlers.NewOnboardHandler(f.Deps, f.Users, f.Orgs, f.Projects, f.Onboards, &req.b, "")
	mux := http.NewServeMux()
	h.Register(mux)

	body := "email=admin@example.com&name=Admin&password=secret12&org_slug=acme&org_name=Acme"
	r := httptest.NewRequest(http.MethodPost, "/onboard/local", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/", rec.Header().Get("Location"))
	assert.False(t, req.b.Load(), "required flag should be flipped off")
}

func TestOnboardHandler_GetOIDCStart_RedirectsToOIDC(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	req := &atomicBoolWrapper{}
	req.b.Store(true)
	h := handlers.NewOnboardHandler(f.Deps, f.Users, f.Orgs, f.Projects, f.Onboards, &req.b, "")
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/onboard/oidc", nil))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Contains(t, rec.Header().Get("Location"), "/login/oidc")
}

func TestOnboardHandler_GetOIDCComplete_RedirectsUnauth(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	req := &atomicBoolWrapper{}
	req.b.Store(true)
	h := handlers.NewOnboardHandler(f.Deps, f.Users, f.Orgs, f.Projects, f.Onboards, &req.b, "")
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/onboard/complete", nil))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/login", rec.Header().Get("Location"))
}

func TestOnboardHandler_GetOIDCComplete_RendersFinalizeForm(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	u, err := f.Users.Create(ctx, user.CreateRequest{Email: "a@b.co", Name: "A", Source: user.SourceOIDC})
	require.NoError(t, err)
	req := &atomicBoolWrapper{}
	req.b.Store(true)

	h := handlers.NewOnboardHandler(f.Deps, f.Users, f.Orgs, f.Projects, f.Onboards, &req.b, "")
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/onboard/complete", "", session.Principal{UserID: u.ID, Email: u.Email}))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, req.b.Load(), "GET must not close onboarding")
	assert.Contains(t, rec.Body.String(), u.Email, "form should prefill signed-in email")
}

func TestOnboardHandler_PostOIDCComplete_PromotesAndClears(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	u, err := f.Users.Create(ctx, user.CreateRequest{Email: "a@b.co", Name: "A", Source: user.SourceOIDC})
	require.NoError(t, err)
	req := &atomicBoolWrapper{}
	req.b.Store(true)

	h := handlers.NewOnboardHandler(f.Deps, f.Users, f.Orgs, f.Projects, f.Onboards, &req.b, "")
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	body := "org_slug=acme&org_name=Acme&project_slug=default&project_name=Default+Project"
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/onboard/complete", body, session.Principal{UserID: u.ID, Email: u.Email}))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.False(t, req.b.Load(), "POST must close onboarding")

	after, err := f.UserStore.ByID(ctx, u.ID)
	require.NoError(t, err)
	assert.True(t, after.IsAdmin, "the onboarding user must actually be promoted, not just redirected")
}

func TestOnboardingHandler_GetOnboarding_Unauth(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	h := handlers.NewOnboardingHandler(f.Deps, f.Users)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/onboarding", nil))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
}

func TestOnboardingHandler_GetOnboarding_OK(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	u, err := f.Users.Create(ctx, user.CreateRequest{Email: "a@b.co", Name: "A", Source: user.SourceLocal})
	require.NoError(t, err)

	h := handlers.NewOnboardingHandler(f.Deps, f.Users)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/onboarding", "", session.Principal{UserID: u.ID, Email: u.Email, Name: u.Name}))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Welcome")
}

func TestOnboardingHandler_PostOnboarding_ValidationErrors(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	u, err := f.Users.Create(ctx, user.CreateRequest{Email: "a@b.co", Name: "A", Source: user.SourceLocal})
	require.NoError(t, err)

	h := handlers.NewOnboardingHandler(f.Deps, f.Users)
	mux := http.NewServeMux()
	h.Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/onboarding", "display_name=", session.Principal{UserID: u.ID, Email: u.Email, Name: u.Name}))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Display name is required")

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/onboarding", "display_name=X&accept_terms=", session.Principal{UserID: u.ID, Email: u.Email, Name: u.Name}))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "accept the Terms")
}

func TestOnboardingHandler_PostOnboarding_HappyPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	u, err := f.Users.Create(ctx, user.CreateRequest{Email: "a@b.co", Name: "A", Source: user.SourceLocal})
	require.NoError(t, err)

	h := handlers.NewOnboardingHandler(f.Deps, f.Users)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/onboarding", "display_name=Alice&accept_terms=1", session.Principal{UserID: u.ID, Email: u.Email, Name: u.Name}))
	assert.Equal(t, http.StatusSeeOther, rec.Code)

	got, err := f.UserStore.ByID(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, "Alice", got.Name)
	assert.NotNil(t, got.TermsAcceptedAt)
}

func TestRequireOnboarded_PassthroughUnauth(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	mw := handlers.RequireOnboarded(f.Deps, f.Users)
	called := false
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { called = true }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	assert.True(t, called)
}

func TestRequireOnboarded_RedirectsWhenTermsMissing(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	u, err := f.Users.Create(ctx, user.CreateRequest{Email: "a@b.co", Name: "A", Source: user.SourceLocal})
	require.NoError(t, err)

	mw := handlers.RequireOnboarded(f.Deps, f.Users)
	called := false
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { called = true }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/x", "", session.Principal{UserID: u.ID}))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.False(t, called)
}

func TestRequireOnboarded_AllowsWhenTermsAccepted(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	u, err := f.Users.Create(ctx, user.CreateRequest{Email: "a@b.co", Name: "A", Source: user.SourceLocal})
	require.NoError(t, err)
	require.NoError(t, f.Users.AcceptTerms(ctx, u.ID))

	mw := handlers.RequireOnboarded(f.Deps, f.Users)
	called := false
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { called = true }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/x", "", session.Principal{UserID: u.ID}))
	assert.True(t, called)
}

func TestRequireOnboarded_SkipsAllowedPaths(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	u, err := f.Users.Create(ctx, user.CreateRequest{Email: "a@b.co", Name: "A", Source: user.SourceLocal})
	require.NoError(t, err)

	mw := handlers.RequireOnboarded(f.Deps, f.Users)
	called := false
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { called = true }))
	for _, path := range []string{"/onboarding", "/logout", "/x/static/y"} {
		called = false
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, path, "", session.Principal{UserID: u.ID}))
		assert.True(t, called, "path=%s", path)
	}
}

func TestDeps_ClearSession_RemovesRow(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	sid := f.seedSession(t, session.Principal{UserID: uuid.New()})
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.AddCookie(&http.Cookie{Name: web.SessionCookieName, Value: sid})
	f.Deps.ClearSession(rec, r)
	assert.Contains(t, rec.Header().Get("Set-Cookie"), web.SessionCookieName)
}

func TestDeps_UpdateSession(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	uid := uuid.New()
	p := session.Principal{UserID: uid}
	sid, err := web.NewSID()
	require.NoError(t, err)
	require.NoError(t, f.Sessions.Save(context.Background(), sid, p, time.Now().Add(web.SessionTTL)))

	p.Name = "updated"
	require.NoError(t, f.Deps.UpdateSession(httptest.NewRequest(http.MethodGet, "/", nil), sid, p))

	got, ok, err := f.Sessions.Load(context.Background(), sid)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "updated", got.Name)
}

func TestAuthHandler_GetLogin_UnauthedRenders(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	authSvc := newAuthSvc(t, f)
	h := handlers.NewAuthHandler(f.Deps, authSvc, f.Users, f.Orgs, f.Projects, nil, nil)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Sign in")
}

func TestAuthHandler_GetLogin_AuthedRedirects(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	authSvc := newAuthSvc(t, f)
	h := handlers.NewAuthHandler(f.Deps, authSvc, f.Users, f.Orgs, f.Projects, nil, nil)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/login", "", session.Principal{UserID: uuid.New()}))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
}

func TestAuthHandler_GetAdminLogin(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	authSvc := newAuthSvc(t, f)
	h := handlers.NewAuthHandler(f.Deps, authSvc, f.Users, f.Orgs, f.Projects, nil, nil)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin-login", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestAuthHandler_PostLogin_BadCreds(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	authSvc := newAuthSvc(t, f)
	h := handlers.NewAuthHandler(f.Deps, authSvc, f.Users, f.Orgs, f.Projects, nil, nil)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("email=x@y.z&password=nope"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(rec, r)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Invalid credentials")
}

func TestAuthHandler_PostLogin_HappyPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	u, err := f.Users.Create(ctx, user.CreateRequest{Email: "a@b.co", Name: "A", Source: user.SourceLocal, Password: "secret12"})
	require.NoError(t, err)
	_ = u

	authSvc := newAuthSvc(t, f)
	h := handlers.NewAuthHandler(f.Deps, authSvc, f.Users, f.Orgs, f.Projects, nil, nil)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("email=a@b.co&password=secret12"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(rec, r)
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Contains(t, rec.Header().Get("Set-Cookie"), web.SessionCookieName)
}

func TestAuthHandler_PostLogout_ClearsCookie(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	authSvc := newAuthSvc(t, f)
	h := handlers.NewAuthHandler(f.Deps, authSvc, f.Users, f.Orgs, f.Projects, nil, nil)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/logout", "", session.Principal{UserID: uuid.New()}))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Contains(t, rec.Header().Get("Set-Cookie"), web.SessionCookieName)
}

func newAuthSvc(t *testing.T, f *handlerFixture) *auth.Service {
	t.Helper()
	users := &authUserStore{store: f.UserStore}
	genesis := auth.Genesis{Email: "root@example.com", PasswordHash: "$2a$10$abcdefghijklmnopqrstuv"}
	local := auth.NewLocalLogin(users, genesis, discardLogger(), passthroughUnexpected(),
		auth.WithLocalNotFound(user.IsNotFoundError),
	)
	return auth.NewService(local, nil, discardLogger(), passthroughUnexpected())
}

type authUserStore struct{ store *fakes.User }

func (a *authUserStore) ByEmail(ctx context.Context, email string) (*auth.UserRef, error) {
	u, err := a.store.ByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	return &auth.UserRef{ID: u.ID, Email: u.Email, Name: u.Name, Source: u.Source, IsAdmin: u.IsAdmin, Locale: u.Locale, PasswordHash: u.PasswordHash}, nil
}

// NOTE: mirrors the write-time rule both real stores apply (internal/user/pgwriter.go, internal/user/sqlite.go); fakes.User does not.
func (a *authUserStore) Save(ctx context.Context, u *auth.UserRef) error {
	isAdmin := u.IsAdmin || u.Source == user.SourceGenesis || u.Source == user.SourceLocal
	return a.store.Save(ctx, &user.User{ID: u.ID, Email: u.Email, Name: u.Name, Source: u.Source, IsAdmin: isAdmin, PasswordHash: u.PasswordHash, Locale: u.Locale})
}

func TestLocaleHandler_NoI18nSkipsRegister(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	h := handlers.NewLocaleHandler(f.Deps, f.Users)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/locale", strings.NewReader("locale=en-US")))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestLocaleHandler_Register_MountsRoute(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.Deps.I18n = i18n.NewEmbeddedBundle(i18n.EnUS)
	h := handlers.NewLocaleHandler(f.Deps, f.Users)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/locale", strings.NewReader("locale=en-US&redirect=/dashboard"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(rec, r)
	assert.Equal(t, http.StatusSeeOther, rec.Code)
}

func TestLayoutForOrg_PinsSlug(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	uid := uuid.New()
	o, err := f.Orgs.Create(ctx, org.CreateRequest{Slug: "acme", Name: "Acme", OwnerID: uid})
	require.NoError(t, err)

	f.Deps.Projects = f.Projects
	r := f.authedRequest(t, http.MethodGet, "/orgs/acme/members", "", session.Principal{UserID: uid, ActiveOrgID: o.ID})

	l := f.Deps.LayoutForOrg(r, "T", "acme", "members")
	require.NotNil(t, l.ActiveOrg)
	assert.Equal(t, "acme", l.ActiveOrg.Slug)
}

func TestInviteHandler_PostSend_HappyPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	uid := uuid.New()
	o, err := f.Orgs.Create(ctx, org.CreateRequest{Slug: "acme", Name: "Acme", OwnerID: uid})
	require.NoError(t, err)
	_ = o

	sendWorkflow := invite.NewSendWorkflow(f.InvStore, nopMailer{}, "http://localhost", discardLogger(), passthroughUnexpected())
	invites := invite.NewService(f.InvStore, sendWorkflow, nil, true, discardLogger(), passthroughUnexpected())

	h := handlers.NewInviteHandler(f.Deps, f.Orgs, invites)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/orgs/acme/invites", "email=b@c.co&role=admin", session.Principal{UserID: uid}))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
}

func TestInviteHandler_PostSend_BadRole(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	uid := uuid.New()
	_, err := f.Orgs.Create(ctx, org.CreateRequest{Slug: "acme", Name: "Acme", OwnerID: uid})
	require.NoError(t, err)

	h := handlers.NewInviteHandler(f.Deps, f.Orgs, f.Invites)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/orgs/acme/invites", "email=b@c.co&role=chief", session.Principal{UserID: uid}))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

type nopMailer struct{}

func (nopMailer) Send(_ context.Context, _ mailer.Message) error { return nil }

func TestOrgHandler_GetShow_HappyPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	uid := uuid.New()
	o, err := f.Orgs.Create(ctx, org.CreateRequest{Slug: "acme", Name: "Acme", OwnerID: uid})
	require.NoError(t, err)
	_ = o

	h := handlers.NewOrgHandler(f.Deps, f.Orgs)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/orgs/acme/members", "", session.Principal{UserID: uid}))
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestInviteHandler_PostSend_MissingOrg(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	h := handlers.NewInviteHandler(f.Deps, f.Orgs, f.Invites)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/orgs/ghost/invites", "email=b@c.co&role=admin", session.Principal{UserID: uuid.New()}))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestLayoutForOrg_UnknownSlug(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	r := f.authedRequest(t, http.MethodGet, "/orgs/ghost", "", session.Principal{UserID: uuid.New()})
	l := f.Deps.LayoutForOrg(r, "T", "ghost", "members")
	assert.Nil(t, l.ActiveOrg)
}

type atomicBoolWrapper struct {
	b atomic.Bool
}

// seedOrg creates an org and its owner membership, so path-scoped handlers can resolve it.
func (f *handlerFixture) seedOrg(t *testing.T, slug string, owner uuid.UUID) *org.Org {
	t.Helper()
	o, err := f.Orgs.Create(context.Background(), org.CreateRequest{Slug: slug, Name: strings.ToUpper(slug), OwnerID: owner})
	require.NoError(t, err)
	return o
}

func setTenant(ctx context.Context, orgID, userID uuid.UUID) context.Context {
	return tenant.Into(ctx, tenant.Context{OrgID: orgID, UserID: userID})
}

func setTenantProject(ctx context.Context, orgID, projectID, userID uuid.UUID) context.Context {
	return tenant.Into(ctx, tenant.Context{OrgID: orgID, ProjectID: projectID, UserID: userID})
}
