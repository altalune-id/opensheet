package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/testutil/fakes"
	"altalune.id/opensheet/internal/todo"
	"altalune.id/opensheet/internal/web/handlers"
)

const todoBase = "/orgs/acme/projects/alpha"

type todoFixture struct {
	*handlerFixture
	Mux *http.ServeMux

	uid     uuid.UUID
	org     uuid.UUID
	project uuid.UUID
}

func newTodoFixture(t *testing.T) *todoFixture {
	t.Helper()
	f := newFixture(t)

	todos := todo.NewService(fakes.NewTodo(), discardLogger(), passthroughUnexpected(), &fakes.Queue{})

	uid := uuid.New()
	o := f.seedOrg(t, "acme", uid)
	octx := setTenant(context.Background(), o.ID, uid)
	proj, err := f.Projects.Create(octx, o.ID, "alpha", "Alpha")
	require.NoError(t, err)

	mux := http.NewServeMux()
	handlers.NewTodoHandler(f.Deps, f.Projects, todos).Register(mux)

	return &todoFixture{handlerFixture: f, Mux: mux, uid: uid, org: o.ID, project: proj.ID}
}

func (x *todoFixture) do(t *testing.T, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	p := session.Principal{UserID: x.uid, ActiveOrgID: x.org, ActiveProjectID: x.project}
	rec := httptest.NewRecorder()
	x.Mux.ServeHTTP(rec, x.authedRequest(t, method, target, body, p))
	return rec
}

func TestTodoListFragment_KeepsProjectScopedURLs(t *testing.T) {
	x := newTodoFixture(t)

	rec := x.do(t, http.MethodPost, todoBase+"/todos", "title=probe")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	require.NotContains(t, body, `action="/orgs"`, "fragment collapsed its URLs to /orgs")
	require.NotContains(t, body, `hx-post="/orgs"`, "fragment collapsed its URLs to /orgs")
	require.Contains(t, body, todoBase+"/todos/", "fragment must keep the project-scoped todo path")
}

func TestTodoRowFragment_KeepsProjectScopedURLs(t *testing.T) {
	x := newTodoFixture(t)

	create := x.do(t, http.MethodPost, todoBase+"/todos", "title=probe")
	require.Equal(t, http.StatusOK, create.Code)

	id := todoIDFrom(t, create.Body.String())
	rec := x.do(t, http.MethodPost, todoBase+"/todos/"+id+"/toggle", "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	require.NotContains(t, body, `action="/orgs"`, "row fragment collapsed its URLs to /orgs")
	require.NotContains(t, body, `hx-post="/orgs"`, "row fragment collapsed its URLs to /orgs")
	require.Contains(t, body, todoBase+"/todos/"+id, "row fragment must keep the project-scoped todo path")
}

func todoIDFrom(t *testing.T, body string) string {
	t.Helper()
	m := regexp.MustCompile(`id="todo-([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})"`).FindStringSubmatch(body)
	require.Len(t, m, 2, "no todo row in body: %s", body)
	return m[1]
}
