package invite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/invite"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
)

type txKey struct{}

type txOrgs struct {
	*fakeOrgs
	pending []*invite.MembershipRef
}

func (o *txOrgs) SaveMembership(ctx context.Context, m *invite.MembershipRef) error {
	if ctx.Value(txKey{}) != nil {
		cp := *m
		o.pending = append(o.pending, &cp)
		return nil
	}
	return o.fakeOrgs.SaveMembership(ctx, m)
}

func (o *txOrgs) uow(ctx context.Context, fn func(ctx context.Context) error) error {
	o.pending = nil
	if err := fn(context.WithValue(ctx, txKey{}, true)); err != nil {
		o.pending = nil
		return err
	}
	for _, m := range o.pending {
		if err := o.fakeOrgs.SaveMembership(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

func seedCustom(t *testing.T, store invite.Store, orgs *fakeOrgs, orgID, roleID uuid.UUID, email string) string {
	t.Helper()
	raw := "raw-" + uuid.NewString()
	inv, err := invite.New(invite.NewParams{OrgID: orgID, Email: email, Role: invite.RoleCustom, RoleID: roleID, TTL: time.Hour, Token: raw})
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), inv))
	orgs.byID[orgID] = &invite.OrgRef{ID: orgID, Slug: "acme", Name: "Acme"}
	return raw
}

func TestInvite_AcceptCopiesRoleID(t *testing.T) {
	store, orgs := fakes.NewInvite(), &txOrgs{fakeOrgs: newFakeOrgs()}
	orgID, roleID := uuid.New(), uuid.New()
	raw := seedCustom(t, store, orgs.fakeOrgs, orgID, roleID, "cara@example.com")

	unex := 0
	wf := invite.NewAcceptWorkflow(store, newFakeUsers(), orgs, newTestLogger(), noopUnexpected(&unex), invite.WithUnitOfWork(orgs.uow))
	res, err := wf.Execute(context.Background(), invite.AcceptRequest{Token: raw, Email: "cara@example.com"})
	require.NoError(t, err)
	m, err := orgs.MembershipOf(context.Background(), orgID, res.User.ID)
	require.NoError(t, err)
	require.Equal(t, invite.RoleCustom, m.Role)
	require.Equal(t, roleID, m.RoleID)
}

func TestInvite_AcceptIsOneUnitOfWork(t *testing.T) {
	store, orgs := fakes.NewInvite(), &txOrgs{fakeOrgs: newFakeOrgs()}
	orgID := uuid.New()
	raw := seedCustom(t, store, orgs.fakeOrgs, orgID, uuid.New(), "dan@example.com")
	store.SaveErr = errors.New("disk full")

	unex := 0
	users := newFakeUsers()
	wf := invite.NewAcceptWorkflow(store, users, orgs, newTestLogger(), noopUnexpected(&unex), invite.WithUnitOfWork(orgs.uow))
	_, err := wf.Execute(context.Background(), invite.AcceptRequest{Token: raw, Email: "dan@example.com"})
	require.Error(t, err)
	usr, err := users.ByEmail(context.Background(), "dan@example.com")
	require.NoError(t, err)
	_, err = orgs.MembershipOf(context.Background(), orgID, usr.ID)
	require.Error(t, err, "a failed invite save must leave no membership")
}

func newCustomService(t *testing.T, store invite.Store, roles *fakes.Roles) *invite.Service {
	t.Helper()
	unex := 0
	unf := noopUnexpected(&unex)
	send := invite.NewSendWorkflow(store, nopMailer{}, "https://example.test", newTestLogger(), unf)
	accept := invite.NewAcceptWorkflow(store, newFakeUsers(), newFakeOrgs(), newTestLogger(), unf)
	return invite.NewService(store, send, accept, true, newTestLogger(), unf, invite.WithPermissions(authz.NewResolver(roles)), invite.WithMembers(fakes.UnrestrictedMembers{}))
}

func TestInvite_SendCustomRole(t *testing.T) {
	store, roles := fakes.NewInvite(), fakes.NewRoles()
	tc := tenant.Context{OrgID: uuid.New(), UserID: uuid.New()}
	roles.Set(tc.OrgID, tc.UserID, "admin")
	reader, writer := uuid.New(), uuid.New()
	roles.SetCustom(uuid.New(), uuid.New(), reader, authz.SheetsRead)
	roles.SetCustom(uuid.New(), uuid.New(), writer, authz.SheetsRead, authz.RolesWrite)
	svc := newCustomService(t, store, roles)
	admin, err := authz.NewResolver(roles).Bind(context.Background(), session.Principal{UserID: tc.UserID, Source: session.SourceOIDC}, tc)
	require.NoError(t, err)

	inv, err := svc.Send(admin, invite.SendRequest{Email: "rita@example.com", Role: invite.RoleCustom, RoleID: reader})
	require.NoError(t, err)
	stored, err := store.ByID(context.Background(), inv.ID)
	require.NoError(t, err)
	require.Equal(t, reader, stored.RoleID)

	_, err = svc.Send(admin, invite.SendRequest{Email: "wes@example.com", Role: invite.RoleCustom, RoleID: writer})
	require.True(t, authz.IsEscalationError(err), "got %T: %v", err, err)
	ae, ok := apperror.AsAppError(err)
	require.True(t, ok)
	require.Equal(t, apperror.CodeRoleEscalation, ae.Code())

	for _, req := range []invite.SendRequest{
		{Email: "a@example.com", Role: invite.RoleCustom},
		{Email: "b@example.com", Role: invite.RoleMember, RoleID: reader},
		{Email: "c@example.com", Role: invite.RoleCustom, RoleID: uuid.New()},
	} {
		_, err := svc.Send(admin, req)
		require.True(t, invite.IsInvalidRoleError(err), "%+v: got %T: %v", req, err, err)
	}
	require.Equal(t, 1, store.Len())
}

func TestInvite_AcceptByExistingOwnerKeepsMembership(t *testing.T) {
	store, orgs := fakes.NewInvite(), &txOrgs{fakeOrgs: newFakeOrgs()}
	orgID := uuid.New()
	inv, raw := seedAcceptable(t, store, orgs.fakeOrgs, orgID, "owner@example.com", time.Hour)
	users := newFakeUsers()
	owner := &invite.UserRef{ID: uuid.New(), Email: "owner@example.com", Name: "Owner"}
	require.NoError(t, users.Save(context.Background(), owner))
	require.NoError(t, orgs.fakeOrgs.SaveMembership(context.Background(), &invite.MembershipRef{OrgID: orgID, UserID: owner.ID, Role: invite.RoleOwner}))

	unex := 0
	wf := invite.NewAcceptWorkflow(store, users, orgs, newTestLogger(), noopUnexpected(&unex), invite.WithUnitOfWork(orgs.uow))
	res, err := wf.Execute(context.Background(), invite.AcceptRequest{Token: raw, Email: "owner@example.com"})
	require.NoError(t, err)
	require.Equal(t, invite.RoleOwner, res.Membership.Role)
	m, err := orgs.MembershipOf(context.Background(), orgID, owner.ID)
	require.NoError(t, err)
	require.Equal(t, invite.RoleOwner, m.Role, "the last owner stays owner")
	stored, err := store.ByID(seedCtx(orgID), inv.ID)
	require.NoError(t, err)
	require.NotNil(t, stored.UsedAt, "the invite is marked used")
}

func TestInvite_AcceptCopiesGrantAndRoleID(t *testing.T) {
	store, orgs := fakes.NewInvite(), &txOrgs{fakeOrgs: newFakeOrgs()}
	orgID, roleID, p1 := uuid.New(), uuid.New(), uuid.New()
	raw := "raw-" + uuid.NewString()
	inv, err := invite.New(invite.NewParams{OrgID: orgID, Email: "gil@example.com", Role: invite.RoleCustom, RoleID: roleID, TTL: time.Hour, Token: raw})
	require.NoError(t, err)
	inv.AllProjects, inv.ProjectIDs = false, []uuid.UUID{p1}
	require.NoError(t, store.Save(context.Background(), inv))
	orgs.byID[orgID] = &invite.OrgRef{ID: orgID, Slug: "acme", Name: "Acme"}

	wf := invite.NewAcceptWorkflow(store, newFakeUsers(), orgs, newTestLogger(), noopUnexpected(nil), invite.WithUnitOfWork(orgs.uow))
	res, err := wf.Execute(context.Background(), invite.AcceptRequest{Token: raw, Email: "gil@example.com"})
	require.NoError(t, err)
	m, err := orgs.MembershipOf(context.Background(), orgID, res.User.ID)
	require.NoError(t, err)
	require.Equal(t, roleID, m.RoleID)
	require.False(t, m.AllProjects, "the accepted membership keeps the invite's restriction")
	require.Equal(t, []uuid.UUID{p1}, m.ProjectIDs)
}
