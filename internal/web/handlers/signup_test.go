package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/org"
	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/user"
	"altalune.id/template/internal/web/handlers"
)

func orgCreate(slug, name string, owner uuid.UUID) org.CreateRequest {
	return org.CreateRequest{Slug: slug, Name: name, OwnerID: owner}
}

func TestSignupHandler_GetSignup_RedirectsUnauth(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.Cfg.Mode = config.ModeCloud
	h := handlers.NewSignupHandler(f.Deps, f.Users, f.Orgs, f.Projects)
	mux := http.NewServeMux()
	h.Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/signup/complete", nil))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/login", rec.Header().Get("Location"))
}

func TestSignupHandler_GetSignup_NotFoundOutsideCloud(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.Cfg.Mode = config.ModeSelfhosted
	h := handlers.NewSignupHandler(f.Deps, f.Users, f.Orgs, f.Projects)
	mux := http.NewServeMux()
	h.Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/signup/complete", "", session.Principal{UserID: uuid.New()}))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSignupHandler_GetSignup_RedirectsWhenAlreadyHasMembership(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.Cfg.Mode = config.ModeCloud
	ctx := context.Background()
	u, err := f.Users.Create(ctx, user.CreateRequest{Email: "alice@example.com", Name: "Alice", Source: user.SourceOIDC})
	require.NoError(t, err)
	_, err = f.Orgs.Create(ctx, orgCreate("acme", "Acme", u.ID))
	require.NoError(t, err)

	h := handlers.NewSignupHandler(f.Deps, f.Users, f.Orgs, f.Projects)
	mux := http.NewServeMux()
	h.Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/signup/complete", "", session.Principal{UserID: u.ID, Email: u.Email, Name: u.Name}))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/", rec.Header().Get("Location"))
}

func TestSignupHandler_GetSignup_Renders(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.Cfg.Mode = config.ModeCloud
	ctx := context.Background()
	u, err := f.Users.Create(ctx, user.CreateRequest{Email: "alice@example.com", Name: "Alice", Source: user.SourceOIDC})
	require.NoError(t, err)

	h := handlers.NewSignupHandler(f.Deps, f.Users, f.Orgs, f.Projects)
	mux := http.NewServeMux()
	h.Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/signup/complete", "", session.Principal{UserID: u.ID, Email: u.Email, Name: u.Name}))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `action="/signup/complete"`)
	assert.Contains(t, rec.Body.String(), `name="org_slug"`)
}

func TestSignupHandler_PostSignup_MissingOrgFieldsRerenders(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.Cfg.Mode = config.ModeCloud
	ctx := context.Background()
	u, err := f.Users.Create(ctx, user.CreateRequest{Email: "alice@example.com", Name: "Alice", Source: user.SourceOIDC})
	require.NoError(t, err)

	h := handlers.NewSignupHandler(f.Deps, f.Users, f.Orgs, f.Projects)
	mux := http.NewServeMux()
	h.Register(mux)

	rec := httptest.NewRecorder()
	body := "org_name=&org_slug="
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/signup/complete", body, session.Principal{UserID: u.ID, Email: u.Email, Name: u.Name}))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Enter an organization")
}

func TestSignupHandler_PostSignup_HappyPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.Cfg.Mode = config.ModeCloud
	ctx := context.Background()
	u, err := f.Users.Create(ctx, user.CreateRequest{Email: "alice@example.com", Name: "Alice", Source: user.SourceOIDC})
	require.NoError(t, err)

	h := handlers.NewSignupHandler(f.Deps, f.Users, f.Orgs, f.Projects)
	mux := http.NewServeMux()
	h.Register(mux)

	rec := httptest.NewRecorder()
	body := "org_name=Acme&org_slug=acme&project_name=Main&project_slug=main"
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/signup/complete", body, session.Principal{UserID: u.ID, Email: u.Email, Name: u.Name}))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/orgs/acme/projects/main/overview", rec.Header().Get("Location"))

	o, err := f.Orgs.BySlug(ctx, "acme")
	require.NoError(t, err)
	assert.Equal(t, u.ID, o.OwnerID)
}
