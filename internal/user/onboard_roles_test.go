package user_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/testutil/fakes"
	"altalune.id/opensheet/internal/user"
)

type txKey struct{}

type txInvites struct {
	*fakeInvites
	pending []*user.InviteRef
}

func (i *txInvites) Save(ctx context.Context, inv *user.InviteRef) error {
	if ctx.Value(txKey{}) != nil {
		cp := *inv
		i.pending = append(i.pending, &cp)
		return nil
	}
	return i.fakeInvites.Save(ctx, inv)
}

func (i *txInvites) uow(ctx context.Context, fn func(ctx context.Context) error) error {
	i.pending = nil
	if err := fn(context.WithValue(ctx, txKey{}, true)); err != nil {
		i.pending = nil
		return err
	}
	for _, inv := range i.pending {
		if err := i.fakeInvites.Save(ctx, inv); err != nil {
			return err
		}
	}
	return nil
}

type staleOrgs struct{ *joinsLate }

func (staleOrgs) ListForUser(context.Context, uuid.UUID) ([]*user.OrgRef, error) { return nil, nil }

type joinsLate struct {
	*fakeOrgs
	misses int
}

func (o *joinsLate) MembershipOf(ctx context.Context, orgID, userID uuid.UUID) (*user.MembershipRef, error) {
	if o.misses > 0 {
		o.misses--
		return nil, &user.MembershipMissingError{}
	}
	return o.fakeOrgs.MembershipOf(ctx, orgID, userID)
}

type onboardCase struct {
	mode         string
	org          *user.OrgRef
	staleLookups int
}

func onboardCases() []onboardCase {
	now := time.Now().UTC()
	return []onboardCase{
		{user.PolicyModeSelfhosted, &user.OrgRef{ID: uuid.New(), Slug: "primary", Name: "Primary", OwnerID: uuid.New(), CreatedAt: now, System: true}, 1},
		{user.PolicyModeCloud, &user.OrgRef{ID: uuid.New(), Slug: "acme", Name: "Acme", OwnerID: uuid.New(), CreatedAt: now}, 0},
	}
}

func seedInvite(t *testing.T, orgs *fakeOrgs, invites *fakeInvites, o *user.OrgRef, role string, roleID uuid.UUID) uuid.UUID {
	t.Helper()
	require.NoError(t, orgs.Save(t.Context(), o))
	id := uuid.New()
	require.NoError(t, invites.Save(t.Context(), &user.InviteRef{
		ID: id, OrgID: o.ID, Email: "alice@example.com", Role: role, RoleID: roleID,
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}))
	return id
}

func TestOnboard_CustomRoleInviteKeepsRoleID(t *testing.T) {
	for _, c := range onboardCases() {
		t.Run(c.mode, func(t *testing.T) {
			orgs, invites := newFakeOrgs(), newFakeInvites()
			roleID := uuid.New()
			seedInvite(t, orgs, invites, c.org, "custom", roleID)
			wf := user.NewOnboardWorkflow(fakes.NewUser(), orgs, newFakeProjects(), invites, user.Policy{Mode: c.mode}, newTestLogger(), noopUnexpected())

			userID := uuid.New()
			_, err := wf.Onboard(t.Context(), userID, "alice@example.com")
			require.NoError(t, err)
			m, err := orgs.MembershipOf(t.Context(), c.org.ID, userID)
			require.NoError(t, err)
			require.Equal(t, "custom", m.Role)
			require.Equal(t, roleID, m.RoleID)
		})
	}
}

func TestOnboard_MembershipSaveFailureLeavesInviteUnused(t *testing.T) {
	for _, c := range onboardCases() {
		t.Run(c.mode, func(t *testing.T) {
			orgs, invites := newFakeOrgs(), &txInvites{fakeInvites: newFakeInvites()}
			inviteID := seedInvite(t, orgs, invites.fakeInvites, c.org, "member", uuid.Nil)
			orgs.SaveMembershipErr = errors.New("disk full")
			wf := user.NewOnboardWorkflow(fakes.NewUser(), orgs, newFakeProjects(), invites, user.Policy{Mode: c.mode}, newTestLogger(), noopUnexpected(),
				user.WithOnboardUnitOfWork(invites.uow))

			_, err := wf.Onboard(t.Context(), uuid.New(), "alice@example.com")
			require.Error(t, err)
			require.Nil(t, invites.byID[inviteID].AcceptedAt, "a failed membership save must leave the invite unused")
		})
	}
}

func TestOnboard_ExistingOwnerKeepsRoleAndUsesInvite(t *testing.T) {
	for _, c := range onboardCases() {
		t.Run(c.mode, func(t *testing.T) {
			base, invites := newFakeOrgs(), newFakeInvites()
			inviteID := seedInvite(t, base, invites, c.org, "member", uuid.Nil)
			userID := uuid.New()
			require.NoError(t, base.SaveMembership(t.Context(), &user.MembershipRef{OrgID: c.org.ID, UserID: userID, Role: "owner"}))
			saves := base.membershipSaves
			orgs := staleOrgs{&joinsLate{fakeOrgs: base, misses: c.staleLookups}}
			wf := user.NewOnboardWorkflow(fakes.NewUser(), orgs, newFakeProjects(), invites, user.Policy{Mode: c.mode}, newTestLogger(), noopUnexpected())

			res, err := wf.Onboard(t.Context(), userID, "alice@example.com")
			require.NoError(t, err)
			require.Equal(t, c.org.ID, res.OrgID)
			m, err := base.MembershipOf(t.Context(), c.org.ID, userID)
			require.NoError(t, err)
			require.Equal(t, "owner", m.Role)
			require.Equal(t, saves, base.membershipSaves, "an existing membership is never rewritten")
			require.NotNil(t, invites.byID[inviteID].AcceptedAt, "the invite is still marked used")
		})
	}
}
