package controlplane_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	orgv1 "altalune.id/opensheet/gen/go/org/v1"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/session"
)

const sessionSource = session.SourceOIDC

func listRestricted(t *testing.T, p session.Principal) *orgv1.Member {
	t.Helper()
	proj, restricted := uuid.New(), uuid.New()
	h := newOSHarness(t, p)
	m, err := org.NewMembership(p.ActiveOrgID, restricted, org.RoleMember)
	require.NoError(t, err)
	require.NoError(t, h.orgs.SaveMembership(t.Context(), m))
	require.NoError(t, h.orgs.SetProjectGrant(t.Context(), p.ActiveOrgID, restricted, org.ProjectGrant{ProjectIDs: []uuid.UUID{proj}}))
	resp, err := listMembers(t, h)
	require.NoError(t, err)
	for _, x := range resp.Msg.GetMembers() {
		if x.GetUserId() == restricted.String() {
			require.False(t, x.GetAllProjects())
			return x
		}
	}
	t.Fatal("restricted member missing")
	return nil
}

func TestControlPlane_ListMembersProjectIDsNeedMembersWrite(t *testing.T) {
	signedIn := humanPrincipal(uuid.New())
	signedIn.Source = sessionSource
	require.Len(t, listRestricted(t, signedIn).GetProjectIds(), 1, "a signed-in owner sees the restricted member's projects")

	require.Empty(t, listRestricted(t, humanPrincipal(uuid.New())).GetProjectIds(), "a token never holds members:write, so it sees only that the grant is restricted")
}

func TestControlPlane_ListMembersShowsGrantIDsToManagersOnly(t *testing.T) {
	orgID, proj, restricted := uuid.New(), uuid.New(), uuid.New()
	p := humanPrincipal(orgID)
	p.Source = sessionSource
	h := newOSHarness(t, p)
	m, err := org.NewMembership(orgID, restricted, org.RoleMember)
	require.NoError(t, err)
	require.NoError(t, h.orgs.SaveMembership(t.Context(), m))
	require.NoError(t, h.orgs.SetProjectGrant(t.Context(), orgID, restricted, org.ProjectGrant{ProjectIDs: []uuid.UUID{proj}}))

	find := func(members []*orgv1.Member, id uuid.UUID) *orgv1.Member {
		for _, x := range members {
			if x.GetUserId() == id.String() {
				return x
			}
		}
		t.Fatalf("member %s missing", id)
		return nil
	}

	resp, err := listMembers(t, h)
	require.NoError(t, err)
	require.True(t, find(resp.Msg.GetMembers(), p.UserID).GetAllProjects())
	got := find(resp.Msg.GetMembers(), restricted)
	require.False(t, got.GetAllProjects())
	require.Equal(t, []string{proj.String()}, got.GetProjectIds(), "an owner sees the restricted member's projects")

	asMember(t, h, p)
	resp, err = listMembers(t, h)
	require.NoError(t, err)
	got = find(resp.Msg.GetMembers(), restricted)
	require.False(t, got.GetAllProjects())
	require.Empty(t, got.GetProjectIds(), "a member without members:write sees only that the grant is restricted")
}
