package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/apperror"
	mcpinternal "altalune.id/opensheet/internal/mcp"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tokens"
	"altalune.id/opensheet/internal/testutil/fakes"
	rootmcp "altalune.id/opensheet/mcp"
	"altalune.id/opensheet/reqid"
)

const (
	mcpAudience   = "https://app.example.com/mcp"
	otherAudience = "https://app.example.com/api"
)

func TestScopesFromContextReadsThePrincipal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		principal *session.Principal
		want      []string
	}{
		{
			name:      "api key principal",
			principal: &session.Principal{Source: session.SourceAPIKey, Scopes: []string{"sheets:read", "sheets:write"}},
			want:      []string{"sheets:read", "sheets:write"},
		},
		{
			name:      "jwt principal carries only its granted scopes",
			principal: &session.Principal{Source: session.SourceToken, Scopes: []string{"sheets:read"}},
			want:      []string{"sheets:read"},
		},
		{
			name:      "jwt principal with no scopes resolves to none",
			principal: &session.Principal{Source: session.SourceToken, UserID: uuid.New(), IsAdmin: true},
			want:      nil,
		},
		{
			name:      "oidc principal with no scopes resolves to none",
			principal: &session.Principal{Source: session.SourceOIDC, UserID: uuid.New()},
			want:      nil,
		},
		{name: "no principal at all", principal: nil, want: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			if tc.principal != nil {
				ctx = session.PrincipalInto(ctx, *tc.principal)
			}
			if got := mcpinternal.ScopesFromContext(ctx); !slices.Equal(got, tc.want) {
				t.Fatalf("ScopesFromContext = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestScopesFromContextDoesNotAliasThePrincipal(t *testing.T) {
	t.Parallel()

	p := session.Principal{Source: session.SourceAPIKey, Scopes: []string{"sheets:read"}}
	ctx := session.PrincipalInto(t.Context(), p)

	got := mcpinternal.ScopesFromContext(ctx)
	got[0] = "sheets:admin"

	if again := mcpinternal.ScopesFromContext(ctx); again[0] != "sheets:read" {
		t.Fatalf("mutating the returned slice changed the principal: %v", again)
	}
}

func TestToolCallIsScopeCheckedForEveryCredentialClass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		token func(*harness) string
		tool  string
		allow bool
	}{
		{name: "jwt with the scope calls the tool", token: (*harness).readJWT, tool: mcpinternal.ToolProjectList, allow: true},
		{name: "jwt without the scope is denied", token: (*harness).readJWT, tool: mcpinternal.ToolTodoCreate},
		{name: "api key with the scope calls the tool", token: (*harness).readKey, tool: mcpinternal.ToolProjectList, allow: true},
		{name: "api key without the scope is denied", token: (*harness).readKey, tool: mcpinternal.ToolTodoCreate},
		{name: "jwt is denied a tool that declares no scope", token: (*harness).readJWT, tool: "unscoped_tool"},
		{name: "api key is denied a tool that declares no scope", token: (*harness).readKey, tool: "unscoped_tool"},
		{name: "jwt is denied an unknown tool", token: (*harness).readJWT, tool: "nonexistent_tool"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			cs := h.connect(t, tc.token(h))

			res, err := cs.CallTool(t.Context(), &sdkmcp.CallToolParams{Name: tc.tool})
			if tc.allow {
				if err != nil {
					t.Fatalf("CallTool(%s): %v", tc.tool, err)
				}
				if res.IsError {
					t.Fatalf("CallTool(%s) reported a tool error: %+v", tc.tool, res.Content)
				}
				if !h.ran.Load() {
					t.Fatalf("CallTool(%s) succeeded but the tool handler never ran", tc.tool)
				}
				return
			}
			if err == nil && !res.IsError {
				t.Fatalf("CallTool(%s) succeeded, want denied", tc.tool)
			}
			if h.ran.Load() {
				t.Fatalf("CallTool(%s) was denied but the tool handler ran anyway", tc.tool)
			}
		})
	}
}

func TestTokenWithoutTheToolScopeGetsAStepUpChallenge(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	res := h.callTool(t, "Bearer "+h.readJWT(), mcpinternal.ToolMemberList)
	t.Cleanup(func() { _ = res.Body.Close() })

	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 insufficient_scope", res.StatusCode)
	}
	want := `Bearer resource_metadata="` + authResource(t).MetadataURL + `", error="insufficient_scope", scope="` + authn.ScopeMembersRead + `"`
	if got := res.Header.Get("WWW-Authenticate"); got != want {
		t.Fatalf("WWW-Authenticate = %q, want %q", got, want)
	}
	if got := decodePayload(t, res).Code; got != apperror.CodeForbidden {
		t.Fatalf("payload code = %q, want %q", got, apperror.CodeForbidden)
	}
	if h.ran.Load() {
		t.Fatal("the tool ran for a token without its scope")
	}
}

func TestAPIKeyWithoutTheToolScopeKeepsTheInResultDenial(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	res := h.callTool(t, "Bearer "+h.readKey(), mcpinternal.ToolTodoCreate)
	t.Cleanup(func() { _ = res.Body.Close() })

	if res.StatusCode == http.StatusForbidden || res.Header.Get("WWW-Authenticate") != "" {
		t.Fatalf("status = %d, challenge = %q; an API key cannot step up, so it keeps the in-result denial",
			res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}
	if h.ran.Load() {
		t.Fatal("the tool ran for a key without its scope")
	}
}

// SECURITY: this is the R4 S7 rule.
func TestJWTNeverSkipsTheScopeCheck(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.verifier.Mint("jwt.adminnoscopes.sig", mcpAudience)
	cs := h.connect(t, "jwt.adminnoscopes.sig")

	for _, tool := range []string{mcpinternal.ToolProjectList, mcpinternal.ToolTodoCreate} {
		res, err := cs.CallTool(t.Context(), &sdkmcp.CallToolParams{Name: tool})
		if err == nil && !res.IsError {
			t.Fatalf("CallTool(%s) succeeded for a JWT holding no scopes", tool)
		}
	}
	if h.ran.Load() {
		t.Fatal("a scope-less JWT reached a tool handler")
	}
}

func TestCredentialFailuresAreMaskedAsOneUnauthorized(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		auth string
	}{
		{name: "no credential"},
		{name: "unrecognized shape", auth: "Bearer not-a-credential"},
		{name: "unknown api key", auth: "Bearer key_0000000000000000000000000000000000000000"},
		{name: "unknown jwt", auth: "Bearer un.known.sig"},
		{name: "jwt minted for another resource", auth: "Bearer jwt.controlplane.sig"},
	}

	var bodies []string
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			res := h.post(t, tc.auth)
			t.Cleanup(func() { _ = res.Body.Close() })

			if res.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", res.StatusCode)
			}
			want := authResource(t).InvalidToken()
			if tc.auth == "" {
				want = authResource(t).Challenge()
			}
			if got := res.Header.Get("WWW-Authenticate"); got != want {
				t.Fatalf("WWW-Authenticate = %q, want %q: only the error code tells no token from a refused one", got, want)
			}
			if h.ran.Load() {
				t.Fatal("an unauthenticated request reached a tool handler")
			}
			bodies = append(bodies, withoutCorrelationIDs(t, res))
		})
	}

	for i, b := range bodies {
		if b != bodies[0] {
			t.Fatalf("body[%d] = %q differs from body[0] = %q; the 401 leaks its cause", i, b, bodies[0])
		}
	}
}

// TestUnauthorizedSpeaksTheErrorCodeVocabulary pins the 401 to the registry every other surface answers from, in the same struct an in-result tool failure carries.
func TestUnauthorizedSpeaksTheErrorCodeVocabulary(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	res := h.post(t, "")
	t.Cleanup(func() { _ = res.Body.Close() })

	payload := decodePayload(t, res)
	if payload.Code != apperror.CodeMCPUnauthenticated {
		t.Fatalf("code = %q, want the registry code %q — the 401 must not speak a vocabulary of its own",
			payload.Code, apperror.CodeMCPUnauthenticated)
	}
	if !regexp.MustCompile(`^[A-Z]{3}[0-9]{3}$`).MatchString(payload.Code) {
		t.Fatalf("code = %q, want the <DOM><NNN> shape ../../docs/errors/README.md registers", payload.Code)
	}
	if payload.Message == "" {
		t.Fatal("the 401 payload carries no message")
	}
}

func TestUnauthorizedIsNotCacheable(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	res := h.post(t, "Bearer not-a-credential")
	t.Cleanup(func() { _ = res.Body.Close() })

	if got := res.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want %q — an intermediary must never cache an auth failure", got, "no-store")
	}
}

func TestUnauthorizedCarriesTheRequestID(t *testing.T) {
	t.Parallel()

	const id = "mcp-unauthorized-probe-0123456789"

	h := newHarness(t)
	guarded := mcpinternal.Authenticate(nil, authResource(t), nil)(h.mcp)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		guarded.ServeHTTP(w, r.WithContext(reqid.WithContext(r.Context(), id)))
	}))
	t.Cleanup(ts.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL, strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })

	payload := decodePayload(t, res)
	if payload.RequestID != id {
		t.Fatalf("request_id = %q, want %q — the 401 must be correlatable to a log line", payload.RequestID, id)
	}
}

func decodePayload(t *testing.T, res *http.Response) rootmcp.ErrorPayload {
	t.Helper()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var payload rootmcp.ErrorPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("the 401 body is not an mcp.ErrorPayload: %v; body=%s", err, body)
	}
	return payload
}

func withoutCorrelationIDs(t *testing.T, res *http.Response) string {
	t.Helper()

	payload := decodePayload(t, res)
	payload.RequestID = ""
	payload.TraceID = ""
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

// SECURITY: mcp.audience is what makes this hold — the same, perfectly valid control-plane token must not open the MCP surface.
func TestTokenMintedForAnotherResourceIsRejected(t *testing.T) {
	t.Parallel()

	const raw = "jwt.controlplane.sig"

	controlPlane := fakes.NewTokenVerifier(otherAudience)
	controlPlane.Mint(raw, otherAudience, authn.ScopeSheetsRead, authn.ScopeSheetsWrite)
	if _, err := controlPlane.Verify(t.Context(), raw); err != nil {
		t.Fatalf("the token must be valid for its own audience, got %v", err)
	}

	h := newHarness(t)
	res := h.post(t, "Bearer "+raw)
	t.Cleanup(func() { _ = res.Body.Close() })

	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a token minted for %q", res.StatusCode, otherAudience)
	}
}

func TestNilAuthenticatorDeniesEveryRequest(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ts := httptest.NewServer(mcpinternal.Authenticate(nil, authResource(t), nil)(h.mcp))
	t.Cleanup(ts.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL, strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+h.readKey())
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })

	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 when no authenticator is wired", res.StatusCode)
	}
}

func TestTranslateError(t *testing.T) {
	t.Parallel()

	other := errors.New("boom")
	tests := []struct {
		name string
		err  error
		code string
	}{
		{name: "nil stays nil", err: nil},
		{name: "scope denied", err: &rootmcp.ScopeDeniedError{Tool: mcpinternal.ToolTodoCreate, Scope: "sheets:write"}, code: apperror.CodeForbidden},
		{name: "scope undeclared", err: &rootmcp.ScopeUndeclaredError{Tool: "unscoped_tool"}, code: apperror.CodeForbidden},
		{name: "insufficient scope", err: &authn.InsufficientScopeError{Scope: "sheets:write"}, code: apperror.CodeForbidden},
		{name: "unauthorized", err: &authn.UnauthorizedError{}, code: apperror.CodeUnauthenticated},
		{name: "unavailable", err: &authn.UnavailableError{Cause: errors.New("db down")}, code: apperror.CodeTokenUnavailable},
		{name: "invalid arguments", err: &rootmcp.InvalidArgumentsError{Tool: mcpinternal.ToolTodoCreate, Field: "title", Reason: "bad"}, code: apperror.CodeValidation},
		{name: "unrecognized error passes through", err: other},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := mcpinternal.TranslateError(tc.err)
			if tc.err == nil {
				if got != nil {
					t.Fatalf("TranslateError(nil) = %v, want nil", got)
				}
				return
			}
			if tc.code == "" {
				if !errors.Is(got, tc.err) {
					t.Fatalf("TranslateError(%v) = %v, want it returned unchanged", tc.err, got)
				}
				return
			}
			var app *apperror.AppError
			if !errors.As(got, &app) {
				t.Fatalf("TranslateError(%v) = %T, want *apperror.AppError", tc.err, got)
			}
			if app.Code() != tc.code {
				t.Fatalf("code = %q, want %q", app.Code(), tc.code)
			}
			if !errors.Is(got, tc.err) {
				t.Fatalf("TranslateError dropped the cause %v", tc.err)
			}
		})
	}
}

// SECURITY: a tool the operator misconfigured (no Scope) must be indistinguishable to the caller from a tool they simply lack the scope for.
func TestTranslateErrorMasksAnUndeclaredScopeAsADenial(t *testing.T) {
	t.Parallel()

	denied := mcpinternal.TranslateError(&rootmcp.ScopeDeniedError{Tool: mcpinternal.ToolTodoCreate, Scope: "sheets:write"})
	undeclared := mcpinternal.TranslateError(&rootmcp.ScopeUndeclaredError{Tool: "unscoped_tool"})

	var a, b *apperror.AppError
	if !errors.As(denied, &a) || !errors.As(undeclared, &b) {
		t.Fatalf("both must translate to *apperror.AppError, got %T and %T", denied, undeclared)
	}
	if a.Code() != b.Code() || a.Message() != b.Message() {
		t.Fatalf("undeclared (%s/%s) is distinguishable from denied (%s/%s)", b.Code(), b.Message(), a.Code(), a.Message())
	}
}

type harness struct {
	ts       *httptest.Server
	mcp      http.Handler
	verifier *fakes.TokenVerifier
	ran      *atomic.Bool
	keys     map[string]string
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	var ran atomic.Bool
	reg := rootmcp.NewRegistry()
	run := func(context.Context, json.RawMessage) (json.RawMessage, error) {
		ran.Store(true)
		return json.RawMessage(`{"ok":true}`), nil
	}
	reg.Register(rootmcp.ToolSpec{Name: mcpinternal.ToolProjectList, Scope: authn.ScopeSheetsRead, Handler: run}, nil)
	reg.Register(rootmcp.ToolSpec{Name: mcpinternal.ToolTodoCreate, Scope: authn.ScopeSheetsWrite, Mutation: true, Handler: run}, nil)
	reg.Register(rootmcp.ToolSpec{Name: "unscoped_tool", Handler: run}, nil)

	verifier := fakes.NewTokenVerifier(mcpAudience)
	verifier.Mint("jwt.reader.sig", mcpAudience, authn.ScopeSheetsRead)
	verifier.Mint("jwt.controlplane.sig", otherAudience, authn.ScopeSheetsRead, authn.ScopeSheetsWrite)

	h := &harness{
		mcp: rootmcp.NewServer(
			rootmcp.WithRegistry(reg),
			rootmcp.WithScopes(mcpinternal.ScopesFromContext),
		).Handler(),
		verifier: verifier,
		ran:      &ran,
		keys:     map[string]string{},
	}

	store := fakes.NewAPIKey()
	h.keys["reader"] = seedKey(t, store, authn.ScopeSheetsRead)
	chain := authn.Chain{
		apikey.NewAuthenticator(store, nil, apikey.Scheme{}, fakes.NewMembers()),
		tokens.NewAuthenticator(verifier, authScheme()),
	}

	h.ts = httptest.NewServer(mcpinternal.Authenticate(chain, authResource(t), nil)(h.mcp))
	t.Cleanup(h.ts.Close)
	return h
}

func (h *harness) readJWT() string { return "jwt.reader.sig" }

func (h *harness) readKey() string { return h.keys["reader"] }

func (h *harness) connect(t *testing.T, bearer string) *sdkmcp.ClientSession {
	t.Helper()

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.0"}, nil)
	cs, err := client.Connect(t.Context(), &sdkmcp.StreamableClientTransport{
		Endpoint:             h.ts.URL,
		DisableStandaloneSSE: true,
		HTTPClient:           &http.Client{Transport: bearerTransport{bearer: bearer}},
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func (h *harness) post(t *testing.T, authorization string) *http.Response {
	t.Helper()
	return h.callTool(t, authorization, mcpinternal.ToolProjectList)
}

func (h *harness) callTool(t *testing.T, authorization, tool string) *http.Response {
	t.Helper()

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `"}}`
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, h.ts.URL, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	res, err := h.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	return res
}

func seedKey(t *testing.T, store *fakes.APIKey, scopes ...string) string {
	t.Helper()

	k, plaintext, err := apikey.Scheme{}.Mint(uuid.New(), uuid.New(), "mcp-test", scopes, nil, nil, time.Now().UTC())
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	store.Seed(k)
	return plaintext
}

func authResource(t *testing.T) tokens.Resource {
	t.Helper()
	r, err := tokens.NewResource(mcpAudience)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func authScheme() authn.Scheme { return authn.Scheme{Prefix: apikey.DefaultPrefix} }

type bearerTransport struct{ bearer string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.Header.Set("Authorization", "Bearer "+b.bearer)
	return http.DefaultTransport.RoundTrip(clone)
}
