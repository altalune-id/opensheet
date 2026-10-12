package boot_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	mcpinternal "altalune.id/opensheet/internal/mcp"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/user"
)

func (i *tokenIssuer) mintWith(t *testing.T, audience string, scopes []string, extra map[string]any) string {
	t.Helper()
	now := time.Now()
	claims := map[string]any{
		"iss":   i.url,
		"sub":   tokenSubject,
		"aud":   audience,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
		"email": tokenEmail,
		"scope": strings.Join(scopes, " "),
	}
	for k, v := range extra {
		claims[k] = v
	}
	header, err := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "JWT", "kid": "k1"})
	require.NoError(t, err)
	payload, err := json.Marshal(claims)
	require.NoError(t, err)

	signing := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	return signing + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(i.priv, []byte(signing)))
}

type foreignTenant struct {
	orgID     string
	projectID string
}

func seedForeignTenant(t *testing.T, f *mcpFixture) foreignTenant {
	t.Helper()

	ctx := t.Context()
	outsider, err := f.srv.Users.Create(ctx, user.CreateRequest{
		Email: "outsider@example.com", Name: "Outsider", Source: user.SourceOIDC,
	})
	require.NoError(t, err)
	o, err := f.srv.Orgs.Create(ctx, org.CreateRequest{Slug: "foreign-org", Name: "Foreign Org", OwnerID: outsider.ID})
	require.NoError(t, err)

	orgCtx := tenant.Into(ctx, tenant.Context{OrgID: o.ID, UserID: outsider.ID})
	p, err := f.srv.Projects.Create(orgCtx, o.ID, "foreign-project", "Foreign Project")
	require.NoError(t, err)

	return foreignTenant{orgID: o.ID.String(), projectID: p.ID.String()}
}

// TestMCP_JWTResolvesItsTenantFromMembership drives the S7 mount with a Bearer JWT end to end. SECURITY: the token's org_id is a hint.
func TestMCP_JWTResolvesItsTenantFromMembership(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})
	foreign := seedForeignTenant(t, f)

	ownOrg, err := f.srv.Orgs.BySlug(t.Context(), "mcp-org")
	require.NoError(t, err)

	read := []string{authn.ScopeSheetsRead}

	t.Run("a token with no org_id resolves the subject's only membership", func(t *testing.T) {
		token := f.issuer.mintWith(t, mcpAudience, read, nil)
		rec := f.call(t, token, callToolBody(mcpinternal.ToolProjectList, map[string]any{}))

		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		require.NotContains(t, rec.Body.String(), "Tenant context names no org",
			"a verified JWT reached a tenant-scoped tool with no org: membership never resolved; body=%s", rec.Body.String())
		require.Contains(t, rec.Body.String(), `\"slug\":\"mcp-project\"`,
			"the JWT call must read the org's own projects; body=%s", rec.Body.String())
	})

	t.Run("a token whose org_id names the subject's own org is honored", func(t *testing.T) {
		token := f.issuer.mintWith(t, mcpAudience, read, map[string]any{"org_id": ownOrg.ID.String()})
		rec := f.call(t, token, callToolBody(mcpinternal.ToolProjectList, map[string]any{}))

		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		require.Contains(t, rec.Body.String(), `\"slug\":\"mcp-project\"`,
			"an org_id the subject is a member of must be honored; body=%s", rec.Body.String())
	})

	t.Run("a token whose org_id names an org the subject is not a member of is refused", func(t *testing.T) {
		token := f.issuer.mintWith(t, mcpAudience, read, map[string]any{"org_id": foreign.orgID})

		rec := f.call(t, token, callToolBody(mcpinternal.ToolProjectList, map[string]any{}))

		require.Equal(t, http.StatusUnauthorized, rec.Code,
			"a token asserting org_id=%s was admitted; an issuer must not be able to name a tenant its subject has no membership in; body=%s",
			foreign.orgID, rec.Body.String())
		require.NotContains(t, rec.Body.String(), "foreign-project",
			"the refused token read the claimed org's data; body=%s", rec.Body.String())
		require.Contains(t, rec.Header().Get("WWW-Authenticate"), mcpMetadataPath,
			"the 401 must carry the RFC 9728 challenge like every other MCP refusal")
	})

	t.Run("an org_id that is not a uuid is refused", func(t *testing.T) {
		token := f.issuer.mintWith(t, mcpAudience, read, map[string]any{"org_id": "mcp-org"})
		rec := f.call(t, token, listToolsBody())

		require.Equal(t, http.StatusUnauthorized, rec.Code,
			"an org_id that names no uuid must be refused rather than silently ignored; body=%s", rec.Body.String())
	})
}
