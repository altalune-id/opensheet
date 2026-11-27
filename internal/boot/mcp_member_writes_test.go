package boot_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	sheetv1 "altalune.id/opensheet/gen/go/sheet/v1"
	"altalune.id/opensheet/gen/go/sheet/v1/sheetv1connect"
	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/user"
)

func seedSheetRow(t *testing.T, f *mcpFixture, orgID, projectID, userID uuid.UUID) uuid.UUID {
	t.Helper()
	w := f.srv.Platform.Pool.W
	prefix := f.srv.Cfg.DB.TablePrefix
	now := time.Now().UTC().Format(time.RFC3339Nano)
	credID, sprdID, sheetID := uuid.New(), uuid.New(), uuid.New()
	_, err := w.ExecContext(t.Context(), "INSERT INTO "+prefix+"credentials (id, org_id, project_id, name, kind, authorized_by_user_id, sealed, created_at, updated_at) VALUES (?, ?, ?, 'seed', 'google_oauth', ?, x'00', ?, ?)",
		credID.String(), orgID.String(), projectID.String(), userID.String(), now, now)
	require.NoError(t, err)
	_, err = w.ExecContext(t.Context(), "INSERT INTO "+prefix+"spreadsheets (id, org_id, project_id, credential_id, google_file_id, created_at, updated_at) VALUES (?, ?, ?, ?, 'file-1', ?, ?)",
		sprdID.String(), orgID.String(), projectID.String(), credID.String(), now, now)
	require.NoError(t, err)
	_, err = w.ExecContext(t.Context(), "INSERT INTO "+prefix+"sheets (id, org_id, project_id, spreadsheet_id, tab, slug, created_at, updated_at) VALUES (?, ?, ?, ?, 'Q1', 'prices', ?, ?)",
		sheetID.String(), orgID.String(), projectID.String(), sprdID.String(), now, now)
	require.NoError(t, err)
	return sheetID
}

// TestMCP_MemberSheetUpdateWritableIsROL001 drives the control plane on the real wiring: a member's token may edit a sheet but not turn its writes on.
func TestMCP_MemberSheetUpdateWritableIsROL001(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})
	ctx := t.Context()
	o, err := f.srv.Orgs.BySlug(ctx, "mcp-org")
	require.NoError(t, err)
	projectID := uuid.MustParse(f.projectID)
	member, err := f.srv.Users.Create(ctx, user.CreateRequest{Email: "writes-member@example.com", Name: "Writes Member", Source: user.SourceOIDC})
	require.NoError(t, err)
	_, err = f.srv.Orgs.AddMember(tenant.WithOrg(ctx, o.ID), o.ID, member.ID, org.RoleMember)
	require.NoError(t, err)
	sheetID := seedSheetRow(t, f, o.ID, projectID, member.ID)

	memberCtx := bindAs(t, f.srv, tenant.Context{OrgID: o.ID, UserID: member.ID})
	_, token, err := f.srv.APIKeys.MintPersonal(memberCtx, "writes", []string{authn.ScopeSheetsAdmin}, apikey.ProjectGrant{All: true}, soon())
	require.NoError(t, err)

	web := httptest.NewServer(f.srv.Web)
	t.Cleanup(web.Close)
	client := sheetv1connect.NewSheetServiceClient(http.DefaultClient, web.URL+"/api")
	off := false
	edit := connect.NewRequest(&sheetv1.UpdateRequest{ProjectId: f.projectID, SheetId: sheetID.String(), Writable: &off})
	edit.Header().Set("Authorization", "Bearer "+token)
	_, err = client.Update(ctx, edit)
	require.NoError(t, err, "the member's token reaches Update and may edit the sheet")

	on := true
	req := connect.NewRequest(&sheetv1.UpdateRequest{ProjectId: f.projectID, SheetId: sheetID.String(), Writable: &on})
	req.Header().Set("Authorization", "Bearer "+token)
	_, err = client.Update(ctx, req)

	cerr, ok := errors.AsType[*connect.Error](err)
	require.True(t, ok, "want a connect error, got %v", err)
	require.Equal(t, connect.CodePermissionDenied, cerr.Code(), "err = %v", err)
	require.NotEmpty(t, cerr.Details())
	msg, dErr := cerr.Details()[0].Value()
	require.NoError(t, dErr)
	detail, ok := msg.(*apperrorv1.ErrorDetail)
	require.True(t, ok)
	require.Equal(t, apperror.CodePermissionDenied, detail.GetCode())

	stored, err := f.srv.Sheets.ByID(tenant.Into(ctx, tenant.Context{OrgID: o.ID, ProjectID: projectID}), sheetID)
	require.NoError(t, err)
	require.False(t, stored.Writable, "a refused update must not turn writes on")
}
