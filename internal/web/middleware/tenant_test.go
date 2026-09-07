package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/internal/web/middleware"
)

func TestTenant_ScopesFromTheActivePrincipal(t *testing.T) {
	orgID, projectID, userID := uuid.New(), uuid.New(), uuid.New()
	var got tenant.Context
	var scoped bool

	h := middleware.Tenant(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		tc, err := tenant.From(r.Context())
		scoped = err == nil
		got = tc
	}))

	req := httptest.NewRequest(http.MethodGet, "/todos", nil)
	req = req.WithContext(session.PrincipalInto(req.Context(), session.Principal{
		UserID: userID, ActiveOrgID: orgID, ActiveProjectID: projectID,
	}))
	h.ServeHTTP(httptest.NewRecorder(), req)

	require.True(t, scoped, "a handler must see a tenant scope without wrapping the context itself")
	require.Equal(t, orgID, got.OrgID)
	require.Equal(t, userID, got.UserID)
	require.Equal(t, projectID, got.ProjectID)
}

func TestTenant_PassesThroughWithoutAnActiveOrg(t *testing.T) {
	cases := []struct {
		name string
		p    *session.Principal
	}{
		{"no principal at all", nil},
		{"signed in but no active org", &session.Principal{UserID: uuid.New()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var scoped bool
			h := middleware.Tenant(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				_, err := tenant.From(r.Context())
				scoped = err == nil
			}))

			req := httptest.NewRequest(http.MethodGet, "/onboard", nil)
			if tc.p != nil {
				req = req.WithContext(session.PrincipalInto(req.Context(), *tc.p))
			}
			h.ServeHTTP(httptest.NewRecorder(), req)

			require.False(t, scoped, "onboarding and signup run before any org exists and must stay unscoped")
		})
	}
}

func TestTenant_DoesNotOverrideAnExistingScope(t *testing.T) {
	activeOrg, otherOrg := uuid.New(), uuid.New()
	var got uuid.UUID

	h := middleware.Tenant(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		inner := tenant.Into(r.Context(), tenant.Context{OrgID: otherOrg})
		tc, _ := tenant.From(inner)
		got = tc.OrgID
	}))

	req := httptest.NewRequest(http.MethodGet, "/orgs/other", nil)
	req = req.WithContext(session.PrincipalInto(req.Context(), session.Principal{
		UserID: uuid.New(), ActiveOrgID: activeOrg,
	}))
	h.ServeHTTP(httptest.NewRecorder(), req)

	require.Equal(t, otherOrg, got, "explicit handler scoping must override the middleware default")
}
