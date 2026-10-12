package boot_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/boot"
	mcpinternal "altalune.id/opensheet/internal/mcp"
	"altalune.id/opensheet/internal/onboard"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/platform/tokens"
	"altalune.id/opensheet/internal/user"
	rootmcp "altalune.id/opensheet/mcp"
	"altalune.id/opensheet/reqid"
)

const (
	mcpPath           = "/mcp"
	mcpAudience       = "http://127.0.0.1/mcp"
	controlAudience   = "http://127.0.0.1/api"
	tokenSubject      = "mcp-agent-subject"
	tokenEmail        = "mcp-agent@example.com"
	mcpMetadataPath   = "/.well-known/oauth-protected-resource/mcp"
	mcpProtocolHeader = "application/json, text/event-stream"

	projectListProcedure = "/api/project.v1.ProjectService/ListProjects"
)

type tokenIssuer struct {
	url  string
	priv ed25519.PrivateKey
}

func newTokenIssuer(t *testing.T) *tokenIssuer {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)

	iss := &tokenIssuer{priv: priv}
	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	iss.url = ts.URL

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"issuer": "` + ts.URL + `",
			"authorization_endpoint": "` + ts.URL + `/authorize",
			"token_endpoint": "` + ts.URL + `/token",
			"jwks_uri": "` + ts.URL + `/jwks",
			"response_types_supported": ["code"],
			"subject_types_supported": ["public"],
			"id_token_signing_alg_values_supported": ["EdDSA"]
		}`))
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"keys":[{"kty":"OKP","crv":"Ed25519","alg":"EdDSA","use":"sig","kid":"k1","x":"` +
			base64.RawURLEncoding.EncodeToString(pub) + `"}]}`))
	})
	return iss
}

func (i *tokenIssuer) mint(t *testing.T, audience string, scopes []string) string {
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
	header, err := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "JWT", "kid": "k1"})
	require.NoError(t, err)
	payload, err := json.Marshal(claims)
	require.NoError(t, err)

	signing := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	sig := ed25519.Sign(i.priv, []byte(signing))
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

type mcpOpts struct {
	enabled        bool
	appsUI         bool
	challengeToken string
}

type mcpFixture struct {
	srv       *boot.Server
	issuer    *tokenIssuer
	log       *captureLog
	projectID string
	readKey   string
	noneKey   string
}

func newMCPFixture(t *testing.T, opts mcpOpts) *mcpFixture {
	t.Helper()

	issuer := newTokenIssuer(t)
	cfg := newSmokeCfg(t)
	cfg.Mode = config.ModeCloud
	cfg.OIDC = config.OIDCConfig{Issuer: stubIssuer(t), ClientID: "mcp-client", ClientSecret: "mcp-secret"}
	cfg.HTTP.CSP.Enabled = true
	cfg.API.Enabled = true
	cfg.API.KeyPrefix = apikey.DefaultPrefix
	cfg.Tokens = tokens.Config{Issuer: issuer.url, Audience: controlAudience}
	cfg.MCP = config.MCPConfig{
		Enabled: opts.enabled, AppsUI: opts.appsUI, Audience: mcpAudience,
		ChallengeToken: opts.challengeToken,
	}

	seed, err := boot.BootServer(context.Background(), cfg)
	require.NoError(t, err)
	seedUser, err := seed.Users.Create(context.Background(), user.CreateRequest{
		Email: "mcp-seed@example.com", Name: "MCP Seed", Source: user.SourceOIDC,
	})
	require.NoError(t, err)
	_, err = seed.Onboards.Complete(context.Background(), seedUser.ID, onboard.MethodCLIInit)
	require.NoError(t, err)
	require.NoError(t, seed.Close())

	capture := &captureLog{}
	srv, err := boot.BootServer(context.Background(), cfg, boot.WithLogger(slog.New(capture)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close() })
	require.True(t, srv.Onboarded, "the fixture must be past the onboarding gate")

	ctx := context.Background()
	owner, err := srv.Users.EnsureFromOIDC(ctx, user.Claims{
		Issuer: issuer.url, Subject: tokenSubject, Email: tokenEmail, Name: "MCP Agent",
	})
	require.NoError(t, err)
	o, err := srv.Orgs.Create(ctx, org.CreateRequest{Slug: "mcp-org", Name: "MCP Org", OwnerID: owner.ID})
	require.NoError(t, err)

	orgCtx := tenant.Into(ctx, tenant.Context{OrgID: o.ID, UserID: owner.ID})
	p, err := srv.Projects.Create(orgCtx, o.ID, "mcp-project", "MCP Project")
	require.NoError(t, err)

	projCtx := tenant.WithProject(orgCtx, p.ID)
	_, readKey, err := srv.APIKeys.Mint(projCtx, "mcp-reader", []string{authn.ScopeSheetsRead}, nil, soon())
	require.NoError(t, err)
	_, noneKey, err := srv.APIKeys.Mint(projCtx, "mcp-keys-only", []string{authn.ScopeAPIKeysRead}, nil, soon())
	require.NoError(t, err)

	return &mcpFixture{
		srv:       srv,
		issuer:    issuer,
		log:       capture,
		projectID: p.ID.String(),
		readKey:   readKey,
		noneKey:   noneKey,
	}
}

func (f *mcpFixture) call(t *testing.T, credential string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return f.post(t, mcpPath, credential, body)
}

func (f *mcpFixture) post(t *testing.T, path, credential string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", mcpProtocolHeader)
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
	rec := httptest.NewRecorder()
	f.srv.Web.ServeHTTP(rec, req)
	return rec
}

func (f *mcpFixture) get(t *testing.T, path, credential, requestID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
	if requestID != "" {
		req.Header.Set(reqid.Header, requestID)
	}
	rec := httptest.NewRecorder()
	f.srv.Web.ServeHTTP(rec, req)
	return rec
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func decodeRPC(t *testing.T, rec *httptest.ResponseRecorder) rpcResponse {
	t.Helper()
	var out rpcResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), "body=%s", rec.Body.String())
	return out
}

func scopeDeniedFor(t *testing.T, rec *httptest.ResponseRecorder, scope string) bool {
	t.Helper()
	resp := decodeRPC(t, rec)
	require.Nil(t, resp.Error, "a scope denial must answer in the result with isError, not the JSON-RPC envelope")

	var result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	require.NoError(t, json.Unmarshal(resp.Result, &result), "result=%s", string(resp.Result))
	if !result.IsError || len(result.Content) == 0 {
		return false
	}
	var payload rootmcp.ErrorPayload
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].Text), &payload), "content=%s", result.Content[0].Text)
	return payload.Code == apperror.CodeForbidden && payload.Meta["scope"] == scope
}

func listToolsBody() map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}
}

func callToolBody(name string, args map[string]any) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": args},
	}
}

// TestMCP_RejectsAControlPlaneAudienceToken is the two-verifier guard.
func TestMCP_RejectsAControlPlaneAudienceToken(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})

	controlToken := f.issuer.mint(t, controlAudience, []string{authn.ScopeSheetsRead})
	mcpToken := f.issuer.mint(t, mcpAudience, []string{authn.ScopeSheetsRead})

	t.Run("the mcp-audience token is accepted at /mcp", func(t *testing.T) {
		rec := f.call(t, mcpToken, listToolsBody())
		require.Equal(t, http.StatusOK, rec.Code,
			"an mcp-audience token must authenticate at /mcp, or the rejection below proves nothing; body=%s", rec.Body.String())
	})

	t.Run("the control-plane token is rejected at /mcp", func(t *testing.T) {
		rec := f.call(t, controlToken, listToolsBody())
		require.Equal(t, http.StatusUnauthorized, rec.Code,
			"RFC 8707: a token minted for tokens.audience was accepted at /mcp — the mcp mount is not on its own verifier; body=%s", rec.Body.String())
		require.Contains(t, rec.Header().Get("WWW-Authenticate"), "resource_metadata=",
			"a 401 from /mcp must point the host at its RFC 9728 metadata document")
	})

	t.Run("the control-plane token is accepted at /api", func(t *testing.T) {
		require.False(t, credentialRejected(f.connect(t, controlToken)),
			"the control-plane token must authenticate at /api, or the split above is not an audience split")
	})

	t.Run("the mcp token is rejected at /api", func(t *testing.T) {
		require.True(t, credentialRejected(f.connect(t, mcpToken)),
			"RFC 8707: an mcp-audience token was accepted at the control plane")
	})
}

func credentialRejected(rec *httptest.ResponseRecorder) bool {
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		return false
	}
	return rec.Code == http.StatusUnauthorized && body.Message == "unauthorized"
}

func (f *mcpFixture) connect(t *testing.T, credential string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, projectListProcedure, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("Authorization", "Bearer "+credential)
	rec := httptest.NewRecorder()
	f.srv.Web.ServeHTTP(rec, req)
	return rec
}

// TestMCP_ScopelessJWTCannotCallATool is the S7-vs-S2 guard in R4: the control plane lets a JWT principal skip its scope table because a signed-in human carries their own authority, but an MCP JWT is a delegated grant an agent holds, so every tool is scope-checked, JWT included.
func TestMCP_ScopelessJWTCannotCallATool(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})

	tests := []struct {
		name       string
		scopes     []string
		wantDenied bool
	}{
		{"no scopes at all", nil, true},
		{"an unrelated scope", []string{authn.ScopeAPIKeysRead}, true},
		{"the tool's own scope", []string{authn.ScopeSheetsRead}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := f.issuer.mint(t, mcpAudience, tt.scopes)
			rec := f.call(t, token, callToolBody(mcpinternal.ToolProjectList, map[string]any{}))
			require.Equal(t, http.StatusOK, rec.Code,
				"the JWT must authenticate, or this guard measures authentication rather than scope; body=%s", rec.Body.String())

			denied := scopeDeniedFor(t, rec, authn.ScopeSheetsRead)
			if tt.wantDenied {
				require.True(t, denied,
					"R4/S7: a JWT holding %v called project_list; an MCP JWT is scope-checked like a key; body=%s", tt.scopes, rec.Body.String())
				return
			}
			require.False(t, denied,
				"a JWT holding the tool's scope was denied it; body=%s", rec.Body.String())
		})
	}
}

// TestMCP_KeyScopeIsCheckedPerTool keeps the same rule true of the other credential class, and proves the surface reaches the shared Connect handlers rather than a second implementation.
func TestMCP_KeyScopeIsCheckedPerTool(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})

	t.Run("a key holding sheets:read lists projects", func(t *testing.T) {
		rec := f.call(t, f.readKey, callToolBody(mcpinternal.ToolProjectList, map[string]any{}))
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		require.False(t, scopeDeniedFor(t, rec, authn.ScopeSheetsRead), "body=%s", rec.Body.String())
		require.Contains(t, rec.Body.String(), `\"slug\":\"mcp-project\"`,
			"the tool must answer from the same project service /api serves; body=%s", rec.Body.String())
	})

	t.Run("a key without sheets:read is denied", func(t *testing.T) {
		rec := f.call(t, f.noneKey, callToolBody(mcpinternal.ToolProjectList, map[string]any{}))
		require.True(t, scopeDeniedFor(t, rec, authn.ScopeSheetsRead),
			"a key lacking the tool's scope called it; body=%s", rec.Body.String())
	})
}

// TestMCP_CallWithoutArgumentsDecodes pins the nil-arguments normalization on the real protojson decode path.
func TestMCP_CallWithoutArgumentsDecodes(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})

	token := f.issuer.mint(t, mcpAudience, []string{authn.ScopeSheetsRead})
	rec := f.call(t, token, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": mcpinternal.ToolProjectList},
	})
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.Contains(t, toolResult(t, rec), "projects",
		"the call did not reach the project service, so protojson was handed a nil body; body=%s", rec.Body.String())
}

func toolResult(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	resp := decodeRPC(t, rec)
	require.Nil(t, resp.Error, "the tool call was refused: %v", resp.Error)

	var result struct {
		IsError           bool           `json:"isError"`
		StructuredContent map[string]any `json:"structuredContent"`
	}
	require.NoError(t, json.Unmarshal(resp.Result, &result), "result=%s", string(resp.Result))
	require.False(t, result.IsError, "the tool reported an error: %s", string(resp.Result))
	require.NotNil(t, result.StructuredContent, "the tool result carried no structured content: %s", string(resp.Result))
	return result.StructuredContent
}

func toolPayload(t *testing.T, rec *httptest.ResponseRecorder) rootmcp.ErrorPayload {
	t.Helper()
	resp := decodeRPC(t, rec)
	require.Nil(t, resp.Error, "a tool failure must answer in the result, not the JSON-RPC envelope")

	var result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	require.NoError(t, json.Unmarshal(resp.Result, &result), "result=%s", string(resp.Result))
	require.True(t, result.IsError, "the tool call succeeded; result=%s", string(resp.Result))
	require.NotEmpty(t, result.Content, "the tool error carried no content; result=%s", string(resp.Result))

	var payload rootmcp.ErrorPayload
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].Text), &payload), "content=%s", result.Content[0].Text)
	return payload
}

// TestMCP_UnauthenticatedCallIsChallenged keeps the surface fail-closed: no credential, an unparsable one and a credential of the wrong shape all answer one masked 401 carrying the RFC 9728 challenge.
func TestMCP_UnauthenticatedCallIsChallenged(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})

	for _, credential := range []string{"", "key_not_a_real_key", "not.a.token", "opaque-credential"} {
		t.Run("credential="+credential, func(t *testing.T) {
			rec := f.call(t, credential, listToolsBody())
			require.Equal(t, http.StatusUnauthorized, rec.Code,
				"/mcp admitted an unauthenticated call; body=%s", rec.Body.String())
			require.Contains(t, rec.Header().Get("WWW-Authenticate"), mcpMetadataPath,
				"the 401 must name the protected-resource metadata document")
			require.NotContains(t, rec.Body.String(), "tools",
				"an unauthenticated caller must not learn the tool catalog; body=%s", rec.Body.String())
		})
	}
}

// TestMCP_RunsTheMCPChainNotTheConsoleChain enforces R5 on boot's own assembly: /mcp carries the edge middlewares and none of the SSR ones.
func TestMCP_RunsTheMCPChainNotTheConsoleChain(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})

	require.NotEmpty(t, f.get(t, "/login", "", "").Header().Get("Content-Security-Policy"),
		"the console chain must set CSP, or this guard proves nothing")

	tests := []struct {
		name       string
		credential string
	}{
		{"unauthenticated", ""},
		{"authenticated", f.readKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const id = "mcp-chain-probe-0123456789"
			rec := f.call(t, tt.credential, listToolsBody())
			require.Empty(t, rec.Header().Get("Content-Security-Policy"),
				"/mcp ran the console chain: CSP is an SSR concern")
			require.Empty(t, rec.Header().Values("Set-Cookie"),
				"/mcp ran the console chain: a machine surface issues no session cookie")

			echoed := f.get(t, mcpPath, tt.credential, id)
			require.Equal(t, id, echoed.Header().Get(reqid.Header),
				"/mcp lost webmw.RequestID: it answered without echoing %s", reqid.Header)
			require.True(t, f.log.logged(mcpPath, id),
				"/mcp lost webmw.RequestLog: no http.request line carrying request id %q", id)

			minted := f.get(t, mcpPath, tt.credential, "").Header().Get(reqid.Header)
			require.NotEmpty(t, minted, "/mcp lost webmw.RequestID: it minted no id of its own")
			require.True(t, f.log.logged(mcpPath, minted),
				"/mcp lost webmw.RequestLog: no http.request line carrying minted id %q", minted)
		})
	}
}

// TestMCP_DisabledLeavesThePathUnmounted keeps mcp.enabled meaningful.
func TestMCP_DisabledLeavesThePathUnmounted(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: false})

	for _, path := range []string{mcpPath, mcpPath + "/", mcpMetadataPath} {
		t.Run(path, func(t *testing.T) {
			rec := f.post(t, path, f.readKey, listToolsBody())
			require.NotEqual(t, http.StatusOK, rec.Code,
				"%s answered while mcp.enabled is false; body=%s", path, rec.Body.String())
			require.NotContains(t, rec.Body.String(), mcpinternal.ToolProjectList,
				"a disabled MCP surface published its tool catalog; body=%s", rec.Body.String())
		})
	}

	require.Nil(t, f.srv.MCP, "no MCP server may be built while mcp.enabled is false")
}

// TestMCP_ToolsCarryAScopeAndTheSharedInstances walks the registry boot actually built: no tool may reach a caller without a scope, and each must close over the same service instance /api mounts, so no verb has a second implementation.
func TestMCP_ToolsCarryAScopeAndTheSharedInstances(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})
	require.NotNil(t, f.srv.MCP, "mcp.enabled is true, so boot must have built a server")

	registry := f.srv.MCP.Registry()
	names := registry.Names()
	require.NotEmpty(t, names, "boot registered no MCP tools")

	instances := f.toolInstances()
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			spec, ok := registry.Spec(name)
			require.True(t, ok)
			require.NotEmpty(t, spec.Scope,
				"%s declares no scope; the root mcp package denies such a tool, and nothing here may default one", name)
			require.True(t, authn.Valid(spec.Scope),
				"%s declares %q, which is not in the scope catalog, so no key can ever hold it", name, spec.Scope)
			require.NotEmpty(t, spec.Description, "%s publishes no description for a host to read", name)

			want, known := instances[name]
			require.True(t, known, "%s is registered but this guard does not know which instance it must share", name)
			require.Same(t, want, registry.HandlerFor(name),
				"%s calls a service instance the Connect mount does not serve", name)
		})
	}
}

// TestMCP_ToolsShareTheConnectHandlerInstance is the identity guard the surface rests on: each tool must close over the very *controlplane.ProjectService / *controlplane.MemberService the Connect mount serves. SECURITY: a second, independently constructed service — or a tool body calling straight into a domain package past Connect — would still satisfy the behavioral test below while duplicating a verb, and a duplicated verb is a verb whose tenant scoping and authorization can diverge.
func TestMCP_ToolsShareTheConnectHandlerInstance(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})
	require.NotNil(t, f.srv.MCP, "mcp.enabled is true, so boot must have built a server")
	require.NotNil(t, f.srv.API, "the control plane must be mounted, or there is no instance to share")

	registry := f.srv.MCP.Registry()
	instances := f.toolInstances()
	type toolInstance struct {
		tool string
		want any
	}
	tests := make([]toolInstance, 0, len(instances))
	for _, tool := range slices.Sorted(maps.Keys(instances)) {
		tests = append(tests, toolInstance{tool, instances[tool]})
	}

	covered := make([]string, 0, len(tests))
	for _, tt := range tests {
		covered = append(covered, tt.tool)
	}
	require.ElementsMatch(t, covered, registry.Names(),
		"boot registers a tool this guard names no shared instance for; every tool must be covered")

	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			require.NotNil(t, tt.want,
				"the Connect mount holds no service for %s; an identity assertion between two nils proves nothing", tt.tool)
			got := registry.HandlerFor(tt.tool)
			require.NotNil(t, got, "%s is registered without the instance it calls", tt.tool)
			require.Same(t, tt.want, got,
				"%s calls a service instance the Connect mount does not serve, so the verb has two implementations", tt.tool)
			require.Equal(t, reflect.ValueOf(tt.want).Pointer(), reflect.ValueOf(got).Pointer(),
				"%s's handler and the Connect mount's handler are not the same address", tt.tool)
		})
	}
}

// TestMCP_ToolCallMatchesTheConnectCall is the behavioral complement: ProjectService.ListProjects driven over Connect and over the MCP tool must answer the same rows. NOTE: this proves equivalence only.
func TestMCP_ToolCallMatchesTheConnectCall(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})

	controlToken := f.issuer.mint(t, controlAudience, []string{authn.ScopeSheetsRead})
	mcpToken := f.issuer.mint(t, mcpAudience, []string{authn.ScopeSheetsRead})

	listed := f.connectRPC(t, controlToken, projectListProcedure, map[string]any{})
	require.Equal(t, http.StatusOK, listed.Code, "the Connect call must succeed; body=%s", listed.Body.String())
	var body struct {
		Projects []map[string]any `json:"projects"`
	}
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &body), "body=%s", listed.Body.String())

	rec := f.call(t, mcpToken, callToolBody(mcpinternal.ToolProjectList, map[string]any{}))
	require.Equal(t, http.StatusOK, rec.Code, "the tool call must succeed; body=%s", rec.Body.String())
	raw, ok := toolResult(t, rec)["projects"].([]any)
	require.True(t, ok, "the tool result carried no projects list; body=%s", rec.Body.String())
	fromTool := make([]map[string]any, 0, len(raw))
	for _, p := range raw {
		row, isMap := p.(map[string]any)
		require.True(t, isMap, "the tool emitted a non-object project: %v", p)
		fromTool = append(fromTool, row)
	}

	require.NotEmpty(t, body.Projects, "the Connect call listed no projects, so this guard compares nothing")
	require.Equal(t, body.Projects, fromTool,
		"the MCP tool and the Connect call disagree on the rows they list: connect=%v mcp=%v", body.Projects, fromTool)
}

func (f *mcpFixture) connectRPC(t *testing.T, credential, procedure string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, procedure, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("Authorization", "Bearer "+credential)
	rec := httptest.NewRecorder()
	f.srv.Web.ServeHTTP(rec, req)
	return rec
}

func (f *mcpFixture) toolInstances() map[string]any {
	return map[string]any{
		mcpinternal.ToolProjectList: f.srv.API.ProjectSvc,
		mcpinternal.ToolMemberList:  f.srv.API.MemberSvc,
	}
}
