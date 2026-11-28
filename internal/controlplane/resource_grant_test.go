package controlplane_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	todov1 "altalune.id/opensheet/gen/go/todo/v1"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/todo"
)

// SECURITY: a todo's own deny is forbidden, so only the member-grant guard turns a same-org todo outside the grant into NotFound.
func TestControlPlane_RestrictedMemberResourceOutsideGrantIsNotFound(t *testing.T) {
	orgID, granted, other := uuid.New(), uuid.New(), uuid.New()
	p := session.Principal{UserID: uuid.New(), Email: "a@b", ActiveOrgID: orgID}
	h := newHarness(t, p)
	ctx := context.Background()
	m, err := h.orgs.MembershipOf(ctx, orgID, p.UserID)
	require.NoError(t, err)
	m.Role = org.RoleAdmin
	require.NoError(t, h.orgs.SaveMembership(ctx, m))
	require.NoError(t, h.orgs.SetProjectGrant(ctx, orgID, p.UserID, org.ProjectGrant{ProjectIDs: []uuid.UUID{granted}}))

	td, err := todo.New(orgID, other, "outside the grant")
	require.NoError(t, err)
	require.NoError(t, h.todos.Save(ctx, td))

	req := connect.NewRequest(&todov1.ToggleRequest{TodoId: td.ID.String()})
	withBearer(req.Header())
	_, err = h.authClient().Toggle(ctx, req)
	require.Error(t, err)
	require.Equal(t, connect.CodeNotFound, connectCode(err), "err=%v", err)
}
