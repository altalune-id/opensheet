package boot_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	mcpinternal "altalune.id/opensheet/internal/mcp"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/role"
	"altalune.id/opensheet/internal/user"
)

func TestMCP_MemberListFollowsTheCallersRole(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})
	ctx := t.Context()

	agent, err := f.srv.Users.EnsureFromOIDC(ctx, user.Claims{Issuer: f.issuer.url, Subject: tokenSubject, Email: tokenEmail, EmailVerified: true, Name: "MCP Agent"})
	require.NoError(t, err)
	o, err := f.srv.Orgs.BySlug(ctx, "mcp-org")
	require.NoError(t, err)
	owner, err := f.srv.Users.Create(ctx, user.CreateRequest{Email: "second-owner@example.com", Name: "Second Owner", Source: user.SourceOIDC})
	require.NoError(t, err)
	_, err = f.srv.Orgs.AddMember(bindAs(t, f.srv, tenant.Context{OrgID: o.ID, UserID: agent.ID}), o.ID, owner.ID, org.RoleOwner)
	require.NoError(t, err)
	ownerCtx := bindAs(t, f.srv, tenant.Context{OrgID: o.ID, UserID: owner.ID})

	roles := role.NewStore(f.srv.Cfg.DB, f.srv.Platform.Pool, f.srv.Platform.PgConn)
	reader, err := role.New(o.ID, "Sheet reader", "", []authz.Permission{authz.SheetsRead}, time.Now())
	require.NoError(t, err)
	require.NoError(t, roles.Save(ownerCtx, reader, 0))
	viewer, err := role.New(o.ID, "Member viewer", "", []authz.Permission{authz.SheetsRead, authz.MembersRead}, time.Now())
	require.NoError(t, err)
	require.NoError(t, roles.Save(ownerCtx, viewer, 0))
	token := f.issuer.mint(t, mcpAudience, []string{authn.ScopeMembersRead})
	call := func() bool {
		return memberListRefused(t, f.call(t, token, callToolBody(mcpinternal.ToolMemberList, map[string]any{})))
	}

	require.False(t, call(), "a built-in owner lists members")

	require.NoError(t, f.srv.Orgs.SetRole(ownerCtx, o.ID, agent.ID, org.RoleCustom, reader.ID))
	require.True(t, call(), "a custom role without members:read must not list members")

	require.NoError(t, f.srv.Orgs.SetRole(ownerCtx, o.ID, agent.ID, org.RoleCustom, viewer.ID))
	require.False(t, call(), "a custom role with members:read lists members")
}
