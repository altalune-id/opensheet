package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/user"
	"altalune.id/opensheet/internal/web/handlers"
)

var slugValue = regexp.MustCompile(`name="(?:slug|org_slug)" type="text" value="([a-z0-9-]+)"`)

func prefilledSlug(t *testing.T, body string) string {
	t.Helper()
	m := slugValue.FindStringSubmatch(body)
	require.NotNil(t, m, "form did not prefill a slug: %s", body)
	return m[1]
}

func TestOrgHandler_GetNew_PrefillsGeneratedSlug(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	mux := http.NewServeMux()
	handlers.NewOrgHandler(f.Deps, f.Orgs).Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/orgs/new", "", session.Principal{UserID: uuid.New()}))
	require.Equal(t, http.StatusOK, rec.Code)

	s := prefilledSlug(t, rec.Body.String())
	_, err := org.NewOrg(s, "Probe", uuid.New())
	assert.NoError(t, err, "prefilled slug %q must be valid", s)
}

func TestOrgHandler_PostCreate_GeneratesSlugWhenBlank(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	mux := http.NewServeMux()
	handlers.NewOrgHandler(f.Deps, f.Orgs).Register(mux)

	uid := uuid.New()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/orgs", "name=Acme&slug=", session.Principal{UserID: uid}))
	require.Equal(t, http.StatusSeeOther, rec.Code)

	orgs, err := f.Orgs.List(context.Background(), uid)
	require.NoError(t, err)
	require.Len(t, orgs, 1)
	assert.NotEmpty(t, orgs[0].Slug)
	assert.Equal(t, "/orgs/"+orgs[0].Slug+"/projects", rec.Header().Get("Location"))
}

func TestProjectHandler_GetNew_PrefillsGeneratedSlug(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	uid := uuid.New()
	o := f.seedOrg(t, "acme", uid)
	mux := http.NewServeMux()
	handlers.NewProjectHandler(f.Deps, f.Projects).Register(mux)

	rec := httptest.NewRecorder()
	p := session.Principal{UserID: uid, ActiveOrgID: o.ID}
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/orgs/acme/projects/new", "", p))
	require.Equal(t, http.StatusOK, rec.Code)

	assert.NotEmpty(t, prefilledSlug(t, rec.Body.String()))
}

func TestProjectHandler_PostCreate_GeneratesSlugWhenBlank(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	uid := uuid.New()
	o := f.seedOrg(t, "acme", uid)
	mux := http.NewServeMux()
	handlers.NewProjectHandler(f.Deps, f.Projects).Register(mux)

	rec := httptest.NewRecorder()
	p := session.Principal{UserID: uid, ActiveOrgID: o.ID}
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/orgs/acme/projects", "name=Alpha&slug=", p))
	require.Equal(t, http.StatusSeeOther, rec.Code)

	items, err := f.Projects.List(setTenant(context.Background(), o.ID, uid), o.ID)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.NotEmpty(t, items[0].Slug)
}

func TestProjectHandler_PostCreate_TakenSlugIsReported(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	uid := uuid.New()
	o := f.seedOrg(t, "acme", uid)
	_, err := f.Projects.Create(setTenant(context.Background(), o.ID, uid), o.ID, "alpha", "Alpha")
	require.NoError(t, err)

	mux := http.NewServeMux()
	handlers.NewProjectHandler(f.Deps, f.Projects).Register(mux)

	rec := httptest.NewRecorder()
	p := session.Principal{UserID: uid, ActiveOrgID: o.ID}
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/orgs/acme/projects", "name=Alpha&slug=alpha", p))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Slug is already taken.")
}

func TestSignupHandler_PostSignup_GeneratesOrgSlugWhenBlank(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.Cfg.Mode = config.ModeCloud
	ctx := context.Background()
	u, err := f.Users.Create(ctx, user.CreateRequest{Email: "alice@example.com", Name: "Alice", Source: user.SourceOIDC})
	require.NoError(t, err)

	mux := http.NewServeMux()
	handlers.NewSignupHandler(f.Deps, f.Users, f.Orgs, f.Projects).Register(mux)

	rec := httptest.NewRecorder()
	body := "org_name=Acme&org_slug=&project_name=Main&project_slug=main"
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/signup/complete", body, session.Principal{UserID: u.ID, Email: u.Email, Name: u.Name}))
	require.Equal(t, http.StatusSeeOther, rec.Code)

	orgs, err := f.Orgs.List(ctx, u.ID)
	require.NoError(t, err)
	require.Len(t, orgs, 1)
	assert.Equal(t, "/orgs/"+orgs[0].Slug+"/projects/main/overview", rec.Header().Get("Location"))
}

func TestSignupHandler_PostSignup_TakenOrgSlugIsAFieldError(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.Cfg.Mode = config.ModeCloud
	ctx := context.Background()
	owner, err := f.Users.Create(ctx, user.CreateRequest{Email: "owner@example.com", Name: "Owner", Source: user.SourceOIDC})
	require.NoError(t, err)
	_, err = f.Orgs.Create(ctx, orgCreate("acme", "Acme", owner.ID))
	require.NoError(t, err)

	u, err := f.Users.Create(ctx, user.CreateRequest{Email: "alice@example.com", Name: "Alice", Source: user.SourceOIDC})
	require.NoError(t, err)

	mux := http.NewServeMux()
	handlers.NewSignupHandler(f.Deps, f.Users, f.Orgs, f.Projects).Register(mux)

	rec := httptest.NewRecorder()
	body := "org_name=Acme&org_slug=acme&project_name=Main&project_slug=main"
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/signup/complete", body, session.Principal{UserID: u.ID, Email: u.Email, Name: u.Name}))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "This slug is already taken.")
}
