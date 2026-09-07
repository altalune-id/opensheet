package user_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/internal/testutil/fakes"
	"altalune.id/template/internal/user"
)

// scopeSpyProjects records the tenant scope ListByOrg is called with. The postgres store derives
// its transaction from that scope, so an unscoped call fails at runtime with tenant: missing context.
type scopeSpyProjects struct {
	*fakeProjects
	sawOrgID uuid.UUID
	scoped   bool
}

func (s *scopeSpyProjects) ListByOrg(ctx context.Context, orgID uuid.UUID) ([]*user.ProjectRef, error) {
	if tc, err := tenant.From(ctx); err == nil {
		s.scoped = true
		s.sawOrgID = tc.OrgID
	}
	return s.fakeProjects.ListByOrg(ctx, orgID)
}

func TestOnboard_ScopesProjectReadsToTheJoinedOrg(t *testing.T) {
	t.Parallel()
	orgs := newFakeOrgs()
	projects := &scopeSpyProjects{fakeProjects: newFakeProjects()}
	invites := newFakeInvites()
	users := fakes.NewUser()

	singleton := &user.OrgRef{ID: uuid.New(), Slug: "primary", Name: "Primary", OwnerID: uuid.New(), CreatedAt: time.Now().UTC()}
	require.NoError(t, orgs.Save(context.Background(), singleton))
	require.NoError(t, invites.Save(context.Background(), &user.InviteRef{
		ID: uuid.New(), OrgID: singleton.ID, Email: "alice@example.com", Role: "member",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}))

	policy := user.Policy{Mode: user.PolicyModeSelfhosted, SingletonOrgSlug: "primary"}
	wf := user.NewOnboardWorkflow(users, orgs, projects, invites, policy, newTestLogger(), noopUnexpected())

	// A user logging in carries no tenant scope — the workflow discovers the org and must bind to it.
	_, err := wf.Onboard(context.Background(), uuid.New(), "alice@example.com")
	require.NoError(t, err)

	require.True(t, projects.scoped, "project reads must carry a tenant scope or the postgres store fails with tenant: missing context")
	require.Equal(t, singleton.ID, projects.sawOrgID, "the scope must name the org being joined")
}
