package handlers_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
)

func TestGoogleConnectHandler_CallbackRefusesAProjectOutsideTheGrant(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	member := session.Principal{UserID: uuid.New(), ActiveOrgID: f.Org.ID, ActiveProjectID: f.Project.ID}
	ownerCtx := setTenant(context.Background(), f.Org.ID, f.Principal.UserID)
	_, err := f.Orgs.AddMember(ownerCtx, f.Org.ID, member.UserID, org.RoleMember)
	require.NoError(t, err)
	raw, err := f.Connect.Start(fakes.BindRole(context.Background(), tenant.Context{OrgID: f.Org.ID, ProjectID: f.Project.ID, UserID: member.UserID}, "member"), f.Org.ID, f.Project.ID, member.UserID, f.path("/credentials"))
	require.NoError(t, err)
	u, err := url.Parse(raw)
	require.NoError(t, err)
	require.NoError(t, f.OrgStore.SetProjectGrant(ownerCtx, f.Org.ID, member.UserID, org.ProjectGrant{ProjectIDs: []uuid.UUID{uuid.New()}}))

	rec := f.callbackAs(t, member, u.Query().Get("state"))
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	f.assertNoCredentialIn(t, f.Org.ID, f.Project.ID)
}
