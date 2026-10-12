package controlplane_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	projectv1 "altalune.id/opensheet/gen/go/project/v1"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/project"
)

type discoveryFixture struct {
	h            *harness
	orgID        uuid.UUID
	foreignOrgID uuid.UUID
	own          *project.Project
	sibling      *project.Project
	foreign      *project.Project
}

func newDiscoveryFixture(t *testing.T) *discoveryFixture {
	t.Helper()
	ctx := context.Background()

	orgID, foreignOrgID := uuid.New(), uuid.New()
	own, err := project.New(orgID, "own", "Own Project")
	require.NoError(t, err)
	sibling, err := project.New(orgID, "sibling", "Sibling Project")
	require.NoError(t, err)
	foreign, err := project.New(foreignOrgID, "foreign", "Foreign Project")
	require.NoError(t, err)

	p := session.Principal{UserID: uuid.New(), Email: "agent@example.com", ActiveOrgID: orgID, ActiveProjectID: own.ID}
	h := newHarness(t, p)

	f := &discoveryFixture{
		h: h, orgID: orgID, foreignOrgID: foreignOrgID,
		own: own, sibling: sibling, foreign: foreign,
	}
	for _, proj := range []*project.Project{own, sibling, foreign} {
		require.NoError(t, h.projs.Save(ctx, proj))
	}
	return f
}

func (f *discoveryFixture) listProjects(t *testing.T) (*projectv1.ListProjectsResponse, error) {
	t.Helper()
	req := connect.NewRequest(&projectv1.ListProjectsRequest{})
	withBearer(req.Header())
	resp, err := f.h.projectClient().ListProjects(context.Background(), req)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// TestProject_ListProjects_HidesOtherOrgs is the tenant guard the discovery tool rests on. SECURITY: project_list is the one tool that hands a caller project UUIDs, so a row belonging to another org leaking here is a UUID the caller can then pass to every other tool.
func TestProject_ListProjects_HidesOtherOrgs(t *testing.T) {
	f := newDiscoveryFixture(t)

	stored, err := f.h.projs.ByID(context.Background(), f.foreign.ID)
	require.NoError(t, err, "the foreign project must be in the store, or this guard hides nothing")
	require.Equal(t, f.foreignOrgID, stored.OrgID)

	msg, err := f.listProjects(t)
	require.NoError(t, err)

	ids := make([]string, 0, len(msg.GetProjects()))
	slugs := make([]string, 0, len(msg.GetProjects()))
	for _, p := range msg.GetProjects() {
		ids = append(ids, p.GetId())
		slugs = append(slugs, p.GetSlug())
		require.Equal(t, f.orgID.String(), p.GetOrgId(),
			"project_list returned a project owned by org %s while the principal is in %s", p.GetOrgId(), f.orgID)
	}

	require.ElementsMatch(t, []string{"own", "sibling"}, slugs,
		"project_list must return exactly the caller's own org's projects; got %v", slugs)
	require.NotContains(t, ids, f.foreign.ID.String(),
		"project_list leaked the foreign org's project UUID, which is the key to every other tool")
	require.NotContains(t, slugs, "foreign",
		"project_list leaked a foreign org's project")
}
