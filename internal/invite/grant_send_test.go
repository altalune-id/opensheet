package invite_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/invite"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
)

type grantFixture struct {
	svc      *invite.Service
	store    *fakes.Invite
	members  *fakes.Org
	projects *fakes.OrgProjects
	tc       tenant.Context
	ctx      context.Context
}

func newGrantFixture(t *testing.T) *grantFixture {
	t.Helper()
	store, members, roles, projects := fakes.NewInvite(), fakes.NewOrg(), fakes.NewRoles(), fakes.NewOrgProjects()
	tc := tenant.Context{OrgID: uuid.New(), UserID: uuid.New()}
	roles.Set(tc.OrgID, tc.UserID, "admin")
	m, err := org.NewMembership(tc.OrgID, tc.UserID, org.RoleAdmin)
	require.NoError(t, err)
	require.NoError(t, members.SaveMembership(context.Background(), m))
	unf := noopUnexpected(nil)
	send := invite.NewSendWorkflow(store, nopMailer{}, "https://example.test", newTestLogger(), unf)
	accept := invite.NewAcceptWorkflow(store, newFakeUsers(), newFakeOrgs(), newTestLogger(), unf)
	svc := invite.NewService(store, send, accept, true, newTestLogger(), unf, invite.WithMembers(members), invite.WithProjects(projects))
	ctx, err := authz.NewResolver(roles).Bind(context.Background(), session.Principal{UserID: tc.UserID, Source: session.SourceOIDC}, tc)
	require.NoError(t, err)
	return &grantFixture{svc: svc, store: store, members: members, projects: projects, tc: tc, ctx: ctx}
}

func (f *grantFixture) project() uuid.UUID {
	id := uuid.New()
	f.projects.Add(f.tc.OrgID, id)
	return id
}

func (f *grantFixture) stored(t *testing.T, id uuid.UUID) org.ProjectGrant {
	t.Helper()
	inv, err := f.store.ByID(context.Background(), id)
	require.NoError(t, err)
	return org.ProjectGrant{All: inv.AllProjects, ProjectIDs: inv.ProjectIDs}
}

func TestInvite_SendStoresTheGrant(t *testing.T) {
	f := newGrantFixture(t)
	p1 := f.project()

	inv, err := f.svc.Send(f.ctx, invite.SendRequest{Email: "a@example.com", Role: invite.RoleMember})
	require.NoError(t, err)
	require.Equal(t, org.ProjectGrant{All: true}, f.stored(t, inv.ID), "no grant means every project, as before")

	inv, err = f.svc.Send(f.ctx, invite.SendRequest{Email: "b@example.com", Role: invite.RoleMember, Grant: &org.ProjectGrant{ProjectIDs: []uuid.UUID{p1}}})
	require.NoError(t, err)
	require.Equal(t, org.ProjectGrant{ProjectIDs: []uuid.UUID{p1}}, f.stored(t, inv.ID))
}

func TestInvite_SendInvalidGrant(t *testing.T) {
	f := newGrantFixture(t)
	p1 := uuid.New()

	_, err := f.svc.Send(f.ctx, invite.SendRequest{Email: "a@example.com", Role: invite.RoleMember, Grant: &org.ProjectGrant{}})
	require.True(t, org.IsProjectGrantEmptyError(err), "%T %v", err, err)
	_, err = f.svc.Send(f.ctx, invite.SendRequest{Email: "a@example.com", Role: invite.RoleMember, Grant: &org.ProjectGrant{All: true, ProjectIDs: []uuid.UUID{p1}}})
	require.True(t, org.IsProjectGrantConflictError(err), "%T %v", err, err)
	ae, ok := apperror.AsAppError(err)
	require.True(t, ok)
	require.Equal(t, apperror.CodeProjectGrantConflict, ae.Code())
}

func TestInvite_OwnerInviteMustBeAllProjects(t *testing.T) {
	f := newGrantFixture(t)
	require.NoError(t, f.members.SetProjectGrant(context.Background(), f.tc.OrgID, f.tc.UserID, org.ProjectGrant{All: true}))
	m, err := f.members.MembershipOf(context.Background(), f.tc.OrgID, f.tc.UserID)
	require.NoError(t, err)
	m.Role = org.RoleOwner
	require.NoError(t, f.members.SaveMembership(context.Background(), m))
	roles := fakes.NewRoles().Set(f.tc.OrgID, f.tc.UserID, "owner")
	ctx, err := authz.NewResolver(roles).Bind(context.Background(), session.Principal{UserID: f.tc.UserID, Source: session.SourceOIDC}, f.tc)
	require.NoError(t, err)

	_, err = f.svc.Send(ctx, invite.SendRequest{Email: "o@example.com", Role: invite.RoleOwner, Grant: &org.ProjectGrant{ProjectIDs: []uuid.UUID{uuid.New()}}})
	require.True(t, org.IsProjectGrantConflictError(err), "%T %v", err, err)
}

func TestInvite_GrantBeyondInviterIsROL012(t *testing.T) {
	f := newGrantFixture(t)
	p1, p2 := f.project(), f.project()
	require.NoError(t, f.members.SetProjectGrant(context.Background(), f.tc.OrgID, f.tc.UserID, org.ProjectGrant{ProjectIDs: []uuid.UUID{p1}}),
		"the inviter was bound with every project, then lost all but p1")

	for name, g := range map[string]*org.ProjectGrant{
		"a project the inviter lost":      {ProjectIDs: []uuid.UUID{p1, p2}},
		"every project":                   {All: true},
		"no grant, meaning every project": nil,
	} {
		_, err := f.svc.Send(f.ctx, invite.SendRequest{Email: "x@example.com", Role: invite.RoleMember, Grant: g})
		require.True(t, org.IsProjectGrantEscalationError(err), "%s: %T %v", name, err, err)
		ae, ok := apperror.AsAppError(err)
		require.True(t, ok)
		require.Equal(t, apperror.CodeProjectGrantEscalation, ae.Code(), name)
	}
	pending, err := f.store.ListPending(tenant.Into(context.Background(), f.tc), f.tc.OrgID)
	require.NoError(t, err)
	require.Empty(t, pending, "a refused invite stores nothing")

	inv, err := f.svc.Send(f.ctx, invite.SendRequest{Email: "y@example.com", Role: invite.RoleMember, Grant: &org.ProjectGrant{ProjectIDs: []uuid.UUID{p1}}})
	require.NoError(t, err)
	require.Equal(t, org.ProjectGrant{ProjectIDs: []uuid.UUID{p1}}, f.stored(t, inv.ID))
}

func TestInvite_ForeignProjectIsROL012(t *testing.T) {
	f := newGrantFixture(t)
	own, foreign := f.project(), uuid.New()
	f.projects.Add(uuid.New(), foreign)

	for name, id := range map[string]uuid.UUID{"another org's project": foreign, "a project that does not exist": uuid.New()} {
		_, err := f.svc.Send(f.ctx, invite.SendRequest{Email: "x@example.com", Role: invite.RoleMember, Grant: &org.ProjectGrant{ProjectIDs: []uuid.UUID{own, id}}})
		require.True(t, org.IsProjectGrantEscalationError(err), "%s, from an all-projects sender: %T %v", name, err, err)
	}
}

func TestInvite_SendWithoutMembersIsRefused(t *testing.T) {
	f := newGrantFixture(t)
	unf := noopUnexpected(nil)
	send := invite.NewSendWorkflow(f.store, nopMailer{}, "https://example.test", newTestLogger(), unf)
	accept := invite.NewAcceptWorkflow(f.store, newFakeUsers(), newFakeOrgs(), newTestLogger(), unf)
	svc := invite.NewService(f.store, send, accept, true, newTestLogger(), unf)

	_, err := svc.Send(f.ctx, invite.SendRequest{Email: "x@example.com", Role: invite.RoleMember})
	require.Error(t, err, "without the members port the sender's grant is unknown")
	pending, err := f.store.ListPending(tenant.Into(context.Background(), f.tc), f.tc.OrgID)
	require.NoError(t, err)
	require.Empty(t, pending)
}

func TestInvite_RestrictedAdminCannotInvite(t *testing.T) {
	f := newGrantFixture(t)
	roles := fakes.NewRoles().Set(f.tc.OrgID, f.tc.UserID, "admin").Restrict(f.tc.OrgID, f.tc.UserID, f.project())
	ctx, err := authz.NewResolver(roles).Bind(context.Background(), session.Principal{UserID: f.tc.UserID, Source: session.SourceOIDC}, f.tc)
	require.NoError(t, err)

	_, err = f.svc.Send(ctx, invite.SendRequest{Email: "x@example.com", Role: invite.RoleMember, Grant: &org.ProjectGrant{All: true}})
	require.True(t, authz.IsDeniedError(err), "a restricted admin holds members:write in no project at all: %T %v", err, err)
}
