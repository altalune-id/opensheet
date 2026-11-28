package boot

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/invite"
	"altalune.id/opensheet/internal/testutil/fakes"
	"altalune.id/opensheet/internal/user"
)

func TestShims_SaveMembershipWritesTheGrant(t *testing.T) {
	ctx := context.Background()
	store := fakes.NewOrg()
	orgID, p1 := uuid.New(), uuid.New()

	invited, onboarded, open := uuid.New(), uuid.New(), uuid.New()
	require.NoError(t, orgStoreForInvite{store: store}.SaveMembership(ctx, &invite.MembershipRef{OrgID: orgID, UserID: invited, Role: invite.RoleMember, ProjectIDs: []uuid.UUID{p1}}))
	require.NoError(t, orgStoreForOnboard{store: store}.SaveMembership(ctx, &user.MembershipRef{OrgID: orgID, UserID: onboarded, Role: "member", ProjectIDs: []uuid.UUID{p1}}))
	require.NoError(t, orgStoreForInvite{store: store}.SaveMembership(ctx, &invite.MembershipRef{OrgID: orgID, UserID: open, Role: invite.RoleMember, AllProjects: true}))

	for _, id := range []uuid.UUID{invited, onboarded} {
		m, err := store.MembershipOf(ctx, orgID, id)
		require.NoError(t, err)
		require.False(t, m.AllProjects, "a restricted grant must survive the shim")
		require.Equal(t, []uuid.UUID{p1}, m.ProjectIDs)
	}
	m, err := store.MembershipOf(ctx, orgID, open)
	require.NoError(t, err)
	require.True(t, m.AllProjects)
}
