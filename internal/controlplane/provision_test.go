package controlplane_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	spreadsheetv1 "altalune.id/opensheet/gen/go/spreadsheet/v1"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
)

func provisionReq(projectID uuid.UUID) *connect.Request[spreadsheetv1.ProvisionRequest] {
	req := connect.NewRequest(&spreadsheetv1.ProvisionRequest{
		ProjectId:      projectID.String(),
		CredentialId:   uuid.NewString(),
		Title:          "Budget",
		IdempotencyKey: "connect-1",
		Tabs:           []*spreadsheetv1.ProvisionTab{{Title: "Q1", Slug: "q1", Headers: []string{"id", "name"}}},
	})
	withBearer(req.Header())
	return req
}

func TestProvision_NeedsSpreadsheetsWriteToo(t *testing.T) {
	tests := []struct {
		name   string
		scopes []string
		want   connect.Code
	}{
		{"sheets:admin alone", []string{authn.ScopeSheetsAdmin}, connect.CodePermissionDenied},
		{"spreadsheets:write alone", []string{authn.ScopeSpreadsheetsWrite}, connect.CodePermissionDenied},
		{"both", []string{authn.ScopeSheetsAdmin, authn.ScopeSpreadsheetsWrite}, connect.CodeUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orgID := uuid.New()
			p := session.Principal{UserID: uuid.New(), Source: session.SourceToken, ActiveOrgID: orgID, Scopes: tt.scopes}
			h := newOSHarness(t, p)
			proj := h.seedProject(orgID)

			resp, err := h.spreadsheetClient().Provision(context.Background(), provisionReq(proj.ID))
			if tt.want == connect.CodeUnknown {
				require.NoError(t, err)
				require.Equal(t, "PROVISIONED", resp.Msg.GetSpreadsheet().GetGoogleFileId())
				require.Len(t, resp.Msg.GetSheets(), 1)
				require.Equal(t, "q1", resp.Msg.GetSheets()[0].GetSlug())
				require.True(t, resp.Msg.GetSheets()[0].GetWritable())
				return
			}
			require.Equal(t, tt.want, connectCode(err), "err = %v", err)
		})
	}
}

func TestProvision_TokenInSecondOrg(t *testing.T) {
	userID := uuid.New()
	f := newMultiOrgFixture(t, userID, func(*multiOrgFixture) session.Principal { return multiOrgToken(userID) })

	resp, err := f.h.spreadsheetClient().Provision(context.Background(), provisionReq(f.secondProj.ID))
	require.NoError(t, err)
	require.Equal(t, f.secondProj.ID.String(), resp.Msg.GetSpreadsheet().GetProjectId())

	_, err = f.h.spreadsheetClient().Provision(context.Background(), provisionReq(f.foreignProj.ID))
	require.Equal(t, connect.CodeNotFound, connectCode(err), "a project outside every membership reads as absent")
}
