package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/web/handlers"
)

func renameAs(t *testing.T, role org.Role) (code int, name string) { //nolint:nonamedreturns // two values differ in role
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

	mux := http.NewServeMux()
	handlers.NewProjectHandler(f.Deps, f.Projects).Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/orgs/acme/projects/alpha/rename", "name=Renamed", session.Principal{UserID: caller, ActiveOrgID: o.ID}))

	got, err := f.Projects.ByID(setTenantProject(context.Background(), o.ID, proj.ID, owner), proj.ID)
	require.NoError(t, err)
	return rec.Code, got.Name
}

// TestProjectHandler_PostRename_RequiresManager pins that a plain member cannot rename a project by a hand-made POST.
func TestProjectHandler_PostRename_RequiresManager(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		role     org.Role
		wantCode int
		wantName string
	}{
		{"admin renames", org.RoleAdmin, http.StatusSeeOther, "Renamed"},
		{"member is refused", org.RoleMember, http.StatusForbidden, "Alpha"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, name := renameAs(t, tt.role)
			require.Equal(t, tt.wantCode, code)
			require.Equal(t, tt.wantName, name)
		})
	}
}
