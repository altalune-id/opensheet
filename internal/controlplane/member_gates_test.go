package controlplane_test

import (
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	orgv1 "altalune.id/opensheet/gen/go/org/v1"
	sheetv1 "altalune.id/opensheet/gen/go/sheet/v1"
	spreadsheetv1 "altalune.id/opensheet/gen/go/spreadsheet/v1"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/role"
)

func asMember(t *testing.T, h *osHarness, p session.Principal) {
	t.Helper()
	m, err := org.NewMembership(p.ActiveOrgID, p.UserID, org.RoleMember)
	require.NoError(t, err)
	require.NoError(t, h.orgs.SaveMembership(t.Context(), m))
}

func requireCode(t *testing.T, err error, want string) {
	t.Helper()
	var cerr *connect.Error
	require.True(t, errors.As(err, &cerr), "err = %v", err)
	require.Equal(t, connect.CodePermissionDenied, cerr.Code(), "err = %v", err)
	require.Len(t, cerr.Details(), 1)
	msg, dErr := cerr.Details()[0].Value()
	require.NoError(t, dErr)
	ed, ok := msg.(*apperrorv1.ErrorDetail)
	require.True(t, ok)
	require.Equal(t, want, ed.Code)
}

func TestControlPlane_MemberUpdateWritableIsROL001(t *testing.T) {
	orgID := uuid.New()
	p := humanPrincipal(orgID)
	h := newOSHarness(t, p)
	asMember(t, h, p)
	proj := h.seedProject(orgID)
	sh := h.seedSheet(orgID, proj.ID, "prices")
	sp := h.seedSpreadsheet(orgID, proj.ID)
	on := true

	sheetReq := connect.NewRequest(&sheetv1.UpdateRequest{ProjectId: proj.ID.String(), SheetId: sh.ID.String(), Writable: &on})
	withBearer(sheetReq.Header())
	_, err := h.sheetClient().Update(t.Context(), sheetReq)
	requireCode(t, err, apperror.CodePermissionDenied)

	sprdReq := connect.NewRequest(&spreadsheetv1.UpdateRequest{ProjectId: proj.ID.String(), SpreadsheetId: sp.ID.String(), Writable: &on})
	withBearer(sprdReq.Header())
	_, err = h.spreadsheetClient().Update(t.Context(), sprdReq)
	requireCode(t, err, apperror.CodePermissionDenied)

	stored, err := h.sheets.ByID(t.Context(), sh.ID)
	require.NoError(t, err)
	require.False(t, stored.Writable, "a refused update must not turn writes on")
}

func asCustom(t *testing.T, h *osHarness, p session.Principal, perms ...authz.Permission) {
	t.Helper()
	r, err := role.New(p.ActiveOrgID, "Custom "+uuid.NewString()[:8], "", perms, time.Now())
	require.NoError(t, err)
	require.NoError(t, h.roles.Save(t.Context(), r, 0))
	require.NoError(t, h.orgs.SaveMembership(t.Context(), &org.Membership{OrgID: p.ActiveOrgID, UserID: p.UserID, Role: org.RoleCustom, RoleID: r.ID}))
}

func listMembers(t *testing.T, h *osHarness) (*connect.Response[orgv1.ListMembersResponse], error) {
	t.Helper()
	req := connect.NewRequest(&orgv1.ListMembersRequest{})
	withBearer(req.Header())
	return h.memberClient().ListMembers(t.Context(), req)
}

func TestControlPlane_ListMembersNeedsMembersRead(t *testing.T) {
	orgID := uuid.New()
	p := humanPrincipal(orgID)
	h := newOSHarness(t, p)

	asCustom(t, h, p, authz.SheetsRead)
	_, err := listMembers(t, h)
	requireCode(t, err, apperror.CodePermissionDenied)

	asCustom(t, h, p, authz.SheetsRead, authz.MembersRead)
	resp, err := listMembers(t, h)
	require.NoError(t, err)
	require.Len(t, resp.Msg.GetMembers(), 1)

	asMember(t, h, p)
	_, err = listMembers(t, h)
	require.NoError(t, err, "a built-in member keeps members:read")
}
