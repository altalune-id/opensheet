package invite_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/invite"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
)

func boundAs(t *testing.T, tc tenant.Context, role string) context.Context {
	t.Helper()
	roles := fakes.NewRoles().Set(tc.OrgID, tc.UserID, role)
	ctx, err := authz.NewResolver(roles).Bind(context.Background(), session.Principal{UserID: tc.UserID, Source: session.SourceOIDC}, tc)
	require.NoError(t, err)
	return ctx
}

func TestInvite_SendRevokeListPendingNeedMembersWrite(t *testing.T) {
	t.Parallel()
	store := fakes.NewInvite()
	svc, _ := newService(t, store)
	tc := tenant.Context{OrgID: uuid.New(), UserID: uuid.New()}
	inv := seedInvite(t, store, tc.OrgID)
	member := boundAs(t, tc, "member")

	requireORG011 := func(err error) {
		t.Helper()
		require.True(t, authz.IsDeniedError(err), "want *DeniedError, got %T: %v", err, err)
		ae, ok := apperror.AsAppError(err)
		require.True(t, ok)
		require.Equal(t, apperror.CodeOrgManagerRequired, ae.Code())
	}

	_, err := svc.ListPending(member)
	requireORG011(err)
	_, err = svc.Send(member, invite.SendRequest{Email: "bob@example.com", Role: invite.RoleMember})
	requireORG011(err)
	requireORG011(svc.Revoke(member, inv.ID))
	require.Equal(t, 1, store.Len(), "a refused call writes nothing")
}

func TestInvite_AdminCannotInviteOwner(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t, fakes.NewInvite())
	tc := tenant.Context{OrgID: uuid.New(), UserID: uuid.New()}
	admin := boundAs(t, tc, "admin")
	owner := boundAs(t, tc, "owner")

	_, err := svc.Send(admin, invite.SendRequest{Email: "eve@example.com", Role: invite.RoleOwner})
	require.True(t, authz.IsEscalationError(err), "want *EscalationError, got %T: %v", err, err)
	ae, ok := apperror.AsAppError(err)
	require.True(t, ok)
	require.Equal(t, apperror.CodeRoleEscalation, ae.Code())

	_, err = svc.Send(owner, invite.SendRequest{Email: "olly@example.com", Role: invite.RoleOwner})
	require.NoError(t, err)
	_, err = svc.Send(admin, invite.SendRequest{Email: "ada@example.com", Role: invite.RoleAdmin})
	require.NoError(t, err)
	_, err = svc.Send(admin, invite.SendRequest{Email: "mo@example.com", Role: invite.RoleMember})
	require.NoError(t, err)
}
