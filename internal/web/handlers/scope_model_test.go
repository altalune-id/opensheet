package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/web/handlers"
)

type scopeModelFixture struct {
	fixture *handlerFixture
	mux     *http.ServeMux
	userID  uuid.UUID
	alpha   *project.Project
	gamma   *project.Project
	beta    *project.Project
}

func newScopeModelFixture(t *testing.T) scopeModelFixture {
	t.Helper()
	f := newFixture(t)
	uid := uuid.New()

	acme := f.seedOrg(t, "acme", uid)
	acmeCtx := setTenant(context.Background(), acme.ID, uid)
	alpha, err := f.Projects.Create(acmeCtx, acme.ID, "alpha", "Alpha")
	require.NoError(t, err)
	gamma, err := f.Projects.Create(acmeCtx, acme.ID, "gamma", "Gamma")
	require.NoError(t, err)

	outsider := uuid.New()
	rival := f.seedOrg(t, "rival", outsider)
	beta, err := f.Projects.Create(setTenant(context.Background(), rival.ID, outsider), rival.ID, "beta", "Beta")
	require.NoError(t, err)

	mux := http.NewServeMux()
	handlers.NewTodoHandler(f.Deps, f.Projects, f.Todos).Register(mux)

	return scopeModelFixture{fixture: f, mux: mux, userID: uid, alpha: alpha, gamma: gamma, beta: beta}
}

func (s scopeModelFixture) get(t *testing.T, target string, p session.Principal) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, s.fixture.authedRequest(t, http.MethodGet, target, "", p))
	return rec
}

// TestRequireProject_RefusesAProjectOutsideTheCallersOrg is the cross-org guard: neither the rival org's path nor its project slug borrowed into the caller's own path may resolve.
func TestRequireProject_RefusesAProjectOutsideTheCallersOrg(t *testing.T) {
	t.Parallel()
	s := newScopeModelFixture(t)
	p := session.Principal{UserID: s.userID, ActiveProjectID: s.alpha.ID}

	cases := []struct {
		name   string
		target string
	}{
		{name: "rival org path", target: "/orgs/rival/projects/beta/todos"},
		{name: "rival project slug under the caller's org", target: "/orgs/acme/projects/beta/todos"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			rec := s.get(t, tt.target, p)
			assert.Equal(t, http.StatusNotFound, rec.Code)
			assert.NotContains(t, rec.Body.String(), s.beta.ID.String())
		})
	}
}

// TestRequireProject_AdmitsAnySiblingProjectInTheCallersOrg pins the settled model: membership is org-level, so any project in an org the caller belongs to is reachable regardless of which project the session last pinned.
func TestRequireProject_AdmitsAnySiblingProjectInTheCallersOrg(t *testing.T) {
	t.Parallel()
	s := newScopeModelFixture(t)

	cases := []struct {
		name   string
		active uuid.UUID
		target string
	}{
		{name: "the pinned project", active: s.alpha.ID, target: "/orgs/acme/projects/alpha/todos"},
		{name: "a sibling the session never pinned", active: s.alpha.ID, target: "/orgs/acme/projects/gamma/todos"},
		{name: "a sibling with no pinned project at all", active: uuid.Nil, target: "/orgs/acme/projects/gamma/todos"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			rec := s.get(t, tt.target, session.Principal{UserID: s.userID, ActiveProjectID: tt.active})
			assert.Equal(t, http.StatusOK, rec.Code)
		})
	}
}
