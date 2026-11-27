package boot_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apikey"
	mcpinternal "altalune.id/opensheet/internal/mcp"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/user"
)

func memberListRefused(t *testing.T, rec *httptest.ResponseRecorder) bool {
	t.Helper()
	if rec.Code != http.StatusOK {
		return true
	}
	resp := decodeRPC(t, rec)
	if resp.Error != nil {
		return true
	}
	var result struct {
		IsError bool `json:"isError"`
	}
	require.NoError(t, json.Unmarshal(resp.Result, &result), "result=%s", string(resp.Result))
	return result.IsError
}

// SECURITY: the membership row is deleted below the org service, so no revoke hook runs and Bind is the only thing standing between a removed creator's credential and the org.
func TestMCP_RemovedCreatorsCredentialsAreRefused(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})
	ctx := context.Background()
	o, err := f.srv.Orgs.BySlug(ctx, "mcp-org")
	require.NoError(t, err)
	creator, err := f.srv.Users.Create(ctx, user.CreateRequest{Email: "creator@example.com", Name: "Creator", Source: user.SourceOIDC})
	require.NoError(t, err)
	_, err = f.srv.Orgs.AddMember(tenant.WithOrg(ctx, o.ID), o.ID, creator.ID, org.RoleOwner)
	require.NoError(t, err)

	projectID, err := uuid.Parse(f.projectID)
	require.NoError(t, err)
	creatorCtx := tenant.Into(ctx, tenant.Context{OrgID: o.ID, ProjectID: projectID, UserID: creator.ID})
	scopes := []string{authn.ScopeMembersRead}
	_, key, err := f.srv.APIKeys.MintOrg(creatorCtx, "creator-key", scopes, apikey.ProjectGrant{All: true}, soon())
	require.NoError(t, err)
	_, personal, err := f.srv.APIKeys.MintPersonal(creatorCtx, "creator-token", scopes, apikey.ProjectGrant{All: true}, soon())
	require.NoError(t, err)

	creds := map[string]string{"a key the creator minted": key, "the creator's personal token": personal}
	for name, cred := range creds {
		require.False(t, memberListRefused(t, f.call(t, cred, callToolBody(mcpinternal.ToolMemberList, map[string]any{}))),
			"%s must work while its creator is a member", name)
	}

	store := org.NewStore(f.srv.Cfg.DB, f.srv.Platform.Pool, f.srv.Platform.PgConn)
	require.NoError(t, store.RemoveMember(tenant.WithOrg(ctx, o.ID), o.ID, creator.ID))

	for name, cred := range creds {
		t.Run(name, func(t *testing.T) {
			rec := f.call(t, cred, callToolBody(mcpinternal.ToolMemberList, map[string]any{}))
			require.True(t, memberListRefused(t, rec), "member_list answered for a removed creator; body=%s", rec.Body.String())
		})
	}
}
