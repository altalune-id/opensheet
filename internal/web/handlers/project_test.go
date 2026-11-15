package handlers_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/web/handlers"
)

func renameAs(t *testing.T, role org.Role, lookupErr error) (code int, name string, lookups int) { //nolint:nonamedreturns // three values differ in role
	t.Helper()
	f := newFixture(t)
	owner, caller := uuid.New(), uuid.New()
	o := f.seedOrg(t, "acme", owner)
	ctx := setTenant(context.Background(), o.ID, owner)
	proj, err := f.Projects.Create(ctx, o.ID, "alpha", "Alpha")
	require.NoError(t, err)
	m, err := org.NewMembership(o.ID, caller, role)
	require.NoError(t, err)
	require.NoError(t, f.OrgStore.SaveMembership(ctx, m))
	f.OrgStore.MembershipOfErr, f.OrgStore.MembershipOfFailAfter = lookupErr, 1

	mux := http.NewServeMux()
	handlers.NewProjectHandler(f.Deps, f.Projects).Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/orgs/acme/projects/alpha/rename", "name=Renamed", session.Principal{UserID: caller, ActiveOrgID: o.ID}))

	lookups = f.OrgStore.MembershipOfCalls()
	got, err := f.Projects.ByID(setTenantProject(context.Background(), o.ID, proj.ID, owner), proj.ID)
	require.NoError(t, err)
	return rec.Code, got.Name, lookups
}

// TestProjectHandler_PostRename_RequiresManager pins that a plain member cannot rename a project by a hand-made POST, and that a failed role lookup refuses.
func TestProjectHandler_PostRename_RequiresManager(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		role      org.Role
		lookupErr error
		wantCode  int
		wantName  string
	}{
		{"admin renames", org.RoleAdmin, nil, http.StatusSeeOther, "Renamed"},
		{"member is refused", org.RoleMember, nil, http.StatusForbidden, "Alpha"},
		{"lookup fails gives 403", org.RoleAdmin, errors.New("store down"), http.StatusForbidden, "Alpha"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, name, lookups := renameAs(t, tt.role, tt.lookupErr)
			require.Equal(t, tt.wantCode, code)
			require.Equal(t, tt.wantName, name)
			require.Equal(t, 2, lookups, "the scope gate and IsManager must each look the membership up once, or the injected failure hits the wrong call")
		})
	}
}
