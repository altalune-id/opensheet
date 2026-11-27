package controlplane_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/testutil/fakes"
)

func resolverFor(orgs *org.Service) *authz.Resolver {
	return authz.NewResolver(fakes.OrgRoles{Orgs: orgs})
}

// NOTE: the harness's person is an owner of its active org, so Bind admits it the way a real member is admitted.
func seedActiveMember(t *testing.T, orgs *fakes.Org, p session.Principal) {
	t.Helper()
	if p.UserID == uuid.Nil || p.ActiveOrgID == uuid.Nil {
		return
	}
	ctx := context.Background()
	if _, err := orgs.ByID(ctx, p.ActiveOrgID); err != nil {
		o, err := org.NewOrg("org-"+p.ActiveOrgID.String()[:8], "Org", p.UserID)
		require.NoError(t, err)
		o.ID = p.ActiveOrgID
		require.NoError(t, orgs.Save(ctx, o))
	}
	m, err := org.NewMembership(p.ActiveOrgID, p.UserID, org.RoleOwner)
	require.NoError(t, err)
	require.NoError(t, orgs.SaveMembership(ctx, m))
}
