package boot_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/user"
)

func consoleProjectPages() []string {
	return []string{"/overview", "/credentials", "/spreadsheets", "/sheets", "/apikeys"}
}

func TestConsole_EveryProjectNavTargetIsMounted(t *testing.T) {
	srv, _ := newScopeProbeServer(t, config.ModeCloud)
	owner, err := srv.Users.Create(context.Background(), user.CreateRequest{
		Email: "nav-owner@example.com", Name: "Nav Owner", Source: user.SourceOIDC,
	})
	require.NoError(t, err)
	o, err := srv.Orgs.Create(context.Background(), org.CreateRequest{Slug: "nav-org", Name: "Nav Org", OwnerID: owner.ID})
	require.NoError(t, err)
	orgCtx := bindAs(t, srv, tenant.Context{OrgID: o.ID, UserID: owner.ID})
	_, err = srv.Projects.Create(orgCtx, o.ID, "nav-project", "Nav Project")
	require.NoError(t, err)
	cookie := probeCookie(t, srv, session.Principal{UserID: owner.ID, ActiveOrgID: o.ID})

	for _, page := range consoleProjectPages() {
		t.Run(page, func(t *testing.T) {
			pattern := "GET /orgs/{org}/projects/{project}" + page
			assert.True(t, slices.Contains(srv.Routes, pattern), "%q is a console redirect or nav target but no handler registers it", pattern)

			req := httptest.NewRequest(http.MethodGet, "/orgs/nav-org/projects/nav-project"+page, nil)
			req.AddCookie(cookie)
			rec := httptest.NewRecorder()
			srv.Web.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusOK, rec.Code, "GET %s\nbody=%s", page, rec.Body.String())
		})
	}
}

func TestConsole_RolesPagesMounted(t *testing.T) {
	srv, _ := newScopeProbeServer(t, config.ModeCloud)
	owner, err := srv.Users.Create(context.Background(), user.CreateRequest{
		Email: "roles-owner@example.com", Name: "Roles Owner", Source: user.SourceOIDC,
	})
	require.NoError(t, err)
	o, err := srv.Orgs.Create(context.Background(), org.CreateRequest{Slug: "roles-org", Name: "Roles Org", OwnerID: owner.ID})
	require.NoError(t, err)
	cookie := probeCookie(t, srv, session.Principal{UserID: owner.ID, ActiveOrgID: o.ID})

	for _, pattern := range []string{
		"GET /orgs/{org}/roles", "GET /orgs/{org}/roles/new", "POST /orgs/{org}/roles",
		"GET /orgs/{org}/roles/{id}", "POST /orgs/{org}/roles/{id}", "POST /orgs/{org}/roles/{id}/delete",
	} {
		assert.True(t, slices.Contains(srv.Routes, pattern), "%q is not mounted", pattern)
	}
	for _, page := range []string{"/orgs/roles-org/roles", "/orgs/roles-org/roles/new"} {
		req := httptest.NewRequest(http.MethodGet, page, nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		srv.Web.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code, "GET %s\nbody=%s", page, rec.Body.String())
	}
}
