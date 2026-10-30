package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"altalune.id/opensheet/mcp"
)

const (
	codeForbidden = "GEN003"
	codeDecode    = "GEN004"
)

func TestWithScopesIsAppliedAsAnOption(t *testing.T) {
	scopes := func(context.Context) []string { return []string{"posts:read"} }
	srv := mcp.NewServer(mcp.WithScopes(scopes))
	if srv == nil {
		t.Fatal("NewServer returned nil")
	}
}

func TestServerSDKIsBuiltOnce(t *testing.T) {
	srv := mcp.NewServer()
	first := srv.SDK()
	if srv.SDK() != first {
		t.Fatal("SDK() returned a different server on the second call")
	}
}

func TestServerListsRegisteredTools(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolSpec{
		Name:        "blog_list",
		Description: "list posts",
		Scope:       "posts:read",
		UI:          "ui://blog/list",
		Handler:     okHandler,
	}, nil)
	reg.Register(mcp.ToolSpec{Name: "blog_publish", Scope: "posts:write", Mutation: true, Handler: okHandler}, nil)

	srv := mcp.NewServer(
		mcp.WithRegistry(reg),
		mcp.WithScopes(scopesFromContext),
		mcp.WithUI(true),
	)
	srv.AddUIResource(testUIResource())
	session := connect(t, srv)

	res, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) != 2 {
		t.Fatalf("ListTools returned %d tools, want 2", len(res.Tools))
	}

	first := res.Tools[0]
	if first.Name != "blog_list" || first.Description != "list posts" {
		t.Fatalf("tool[0] = %+v, want blog_list/list posts", first)
	}
	if !first.Annotations.ReadOnlyHint {
		t.Error("blog_list is not a mutation, want ReadOnlyHint true")
	}
	if res.Tools[1].Annotations.ReadOnlyHint {
		t.Error("blog_publish is a mutation, want ReadOnlyHint false")
	}
}

// TestUIMetaIsTheFixedMCPAppsShape pins the one _meta key MCP Apps defines and the object under it.
func TestUIMetaIsTheFixedMCPAppsShape(t *testing.T) {
	tool := uiTool(t, mcp.WithUI(true))

	raw, err := json.Marshal(map[string]any(tool.Meta))
	if err != nil {
		t.Fatalf("marshal _meta: %v", err)
	}
	if got, want := string(raw), `{"ui":{"resourceUri":"ui://blog/list"}}`; got != want {
		t.Fatalf("_meta = %s, want %s", got, want)
	}
}

func TestUIMetaIsAbsentWithoutWithUI(t *testing.T) {
	if meta := uiTool(t).Meta; meta != nil {
		t.Fatalf("_meta = %v, want none when WithUI is not set", meta)
	}
}

func TestServerCallsToolWhenScopeIsGranted(t *testing.T) {
	args := make(chan json.RawMessage, 1)
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolSpec{
		Name:  "blog_list",
		Scope: "posts:read",
		Handler: func(_ context.Context, req json.RawMessage) (json.RawMessage, error) {
			args <- req
			return json.RawMessage(`{"count":2}`), nil
		},
	}, nil)

	session := connect(t, mcp.NewServer(mcp.WithRegistry(reg), mcp.WithScopes(scopesFromContext)), "posts:read")

	res, err := session.CallTool(t.Context(), &sdkmcp.CallToolParams{
		Name:      "blog_list",
		Arguments: map[string]any{"projectId": "p1"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("CallTool reported a tool error: %+v", res.Content)
	}

	var got map[string]any
	if err := json.Unmarshal(<-args, &got); err != nil {
		t.Fatalf("handler arguments were not valid JSON: %v", err)
	}
	if got["projectId"] != "p1" {
		t.Fatalf("handler arguments = %v, want projectId p1", got)
	}

	if text := textOf(t, res); text != `{"count":2}` {
		t.Fatalf("content[0] = %q, want the handler's raw JSON result", text)
	}
	if res.StructuredContent == nil {
		t.Fatal("StructuredContent is nil, want the handler result decoded")
	}
}

// TestCallWithoutArgumentsDeliversAnEmptyObject pins the normalization of a tools/call that omits "arguments".
func TestCallWithoutArgumentsDeliversAnEmptyObject(t *testing.T) {
	args := make(chan json.RawMessage, 1)
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolSpec{
		Name:  "blog_list",
		Scope: "posts:read",
		Handler: func(_ context.Context, req json.RawMessage) (json.RawMessage, error) {
			args <- req
			return json.RawMessage(`{}`), nil
		},
	}, nil)

	ts := serve(t, mcp.NewServer(mcp.WithRegistry(reg), mcp.WithScopes(scopesFromContext)), "posts:read")
	rpc(t, ts, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"blog_list"}}`)

	if got := string(<-args); got != "{}" {
		t.Fatalf("handler arguments = %q, want %q", got, "{}")
	}
}

// TestScopeDenialIsAToolResultNotAJSONRPCError pins a denial into the result with isError and a mapped code.
func TestScopeDenialIsAToolResultNotAJSONRPCError(t *testing.T) {
	var called atomic.Bool
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolSpec{
		Name:  "blog_publish",
		Scope: "posts:write",
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			called.Store(true)
			return nil, nil
		},
	}, nil)

	srv := mcp.NewServer(mcp.WithRegistry(reg), mcp.WithScopes(scopesFromContext), mcp.WithErrorMapper(testMapper))
	ts := serve(t, srv, "posts:read")
	body := rpc(t, ts, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"blog_publish","arguments":{}}}`)

	var envelope struct {
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode envelope: %v (body=%s)", err, body)
	}
	if envelope.Error != nil {
		t.Fatalf("scope denial answered in the JSON-RPC envelope with code %d, want it in the result; body=%s",
			envelope.Error.Code, body)
	}
	if !envelope.Result.IsError {
		t.Fatalf("result isError = false, want true; body=%s", body)
	}
	if called.Load() {
		t.Fatal("tool handler ran without the required scope")
	}

	var payload mcp.ErrorPayload
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &payload); err != nil {
		t.Fatalf("denial content is not an ErrorPayload: %v (%s)", err, envelope.Result.Content[0].Text)
	}
	if payload.Code != codeForbidden {
		t.Fatalf("payload.Code = %q, want the mapped %q an agent can branch on", payload.Code, codeForbidden)
	}
	if payload.Meta["scope"] != "posts:write" || payload.Meta["tool"] != "blog_publish" {
		t.Fatalf("payload.Meta = %v, want the tool and the scope it needs", payload.Meta)
	}
}

func TestServerDeniesToolWhenNoScopesFuncIsWired(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolSpec{Name: "blog_list", Scope: "posts:read", Handler: okHandler}, nil)

	session := connect(t, mcp.NewServer(mcp.WithRegistry(reg)))

	res, err := session.CallTool(t.Context(), &sdkmcp.CallToolParams{Name: "blog_list"})
	if err != nil {
		t.Fatalf("CallTool returned a protocol error, want a tool error result: %v", err)
	}
	if !res.IsError {
		t.Fatal("CallTool succeeded on a server with no ScopesFunc")
	}
}

func TestServerDeniesToolRegisteredWithoutAScope(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolSpec{Name: "blog_list", Handler: okHandler}, nil)

	session := connect(t, mcp.NewServer(mcp.WithRegistry(reg), mcp.WithScopes(scopesFromContext)), "posts:read")

	res, err := session.CallTool(t.Context(), &sdkmcp.CallToolParams{Name: "blog_list"})
	if err != nil {
		t.Fatalf("CallTool returned a protocol error, want a tool error result: %v", err)
	}
	if !res.IsError {
		t.Fatal("CallTool succeeded on a tool that declares no scope")
	}
}

// TestUnmappedFailureIsNotLeakedToTheCaller pins that the raw cause reaches the log and never the caller.
func TestUnmappedFailureIsNotLeakedToTheCaller(t *testing.T) {
	const secret = "proto: (line 1:2): unknown field \"internal_column_name\""

	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolSpec{
		Name:    "blog_list",
		Scope:   "posts:read",
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) { return nil, errors.New(secret) },
	}, nil)

	var logged bytes.Buffer
	session := connect(t, mcp.NewServer(
		mcp.WithRegistry(reg),
		mcp.WithScopes(scopesFromContext),
		mcp.WithErrorMapper(testMapper),
		mcp.WithLogger(slog.New(slog.NewTextHandler(&logged, nil))),
	), "posts:read")

	res, err := session.CallTool(t.Context(), &sdkmcp.CallToolParams{Name: "blog_list"})
	if err != nil {
		t.Fatalf("CallTool returned a protocol error, want a tool error result: %v", err)
	}
	if !res.IsError {
		t.Fatal("CallTool result IsError = false, want true")
	}

	text := textOf(t, res)
	if strings.Contains(text, "internal_column_name") || strings.Contains(text, secret) {
		t.Fatalf("the tool error leaked the raw Go error to the caller: %s", text)
	}
	var payload mcp.ErrorPayload
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("tool error content is not an ErrorPayload: %v (%s)", err, text)
	}
	if payload.Code != mcp.DefaultUnmappedCode {
		t.Fatalf("payload.Code = %q, want %q", payload.Code, mcp.DefaultUnmappedCode)
	}
	if !strings.Contains(logged.String(), "internal_column_name") {
		t.Fatalf("the cause was not logged server-side: %s", logged.String())
	}
	if !strings.Contains(logged.String(), "blog_list") {
		t.Fatalf("the log does not name the tool that failed: %s", logged.String())
	}
}

func TestMappedFailureAnswersTheMappedPayload(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolSpec{
		Name:    "blog_list",
		Scope:   "posts:read",
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) { return nil, errDecode },
	}, nil)

	session := connect(t, mcp.NewServer(
		mcp.WithRegistry(reg),
		mcp.WithScopes(scopesFromContext),
		mcp.WithErrorMapper(testMapper),
	), "posts:read")

	res, err := session.CallTool(t.Context(), &sdkmcp.CallToolParams{Name: "blog_list"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	var payload mcp.ErrorPayload
	if err := json.Unmarshal([]byte(textOf(t, res)), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Code != codeDecode || payload.Message != "invalid arguments" {
		t.Fatalf("payload = %+v, want the mapper's code and message", payload)
	}
}

// TestRegisteringAfterBuildPanics pins that a tool registered after the build fails loudly rather than vanishing.
func TestRegisteringAfterBuildPanics(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolSpec{Name: "blog_list", Scope: "posts:read", Handler: okHandler}, nil)
	srv := mcp.NewServer(mcp.WithRegistry(reg), mcp.WithScopes(scopesFromContext))
	_ = srv.Handler()

	defer func() {
		got, ok := recover().(string)
		if !ok {
			t.Fatal("a tool registered after the server was built was accepted, and is silently absent from the catalog")
		}
		if got != "mcp: tool blog_publish registered after the server was built" {
			t.Fatalf("panic = %q, want the late-registration message", got)
		}
	}()
	reg.Register(mcp.ToolSpec{Name: "blog_publish", Scope: "posts:write", Handler: okHandler}, nil)
}

func TestScopeDeniedError(t *testing.T) {
	err := error(&mcp.ScopeDeniedError{Tool: "blog_publish", Scope: "posts:write"})
	if !mcp.IsScopeDeniedError(err) {
		t.Fatal("IsScopeDeniedError = false, want true")
	}
	if mcp.IsScopeDeniedError(errors.New("other")) {
		t.Fatal("IsScopeDeniedError(other) = true, want false")
	}
	if mcp.IsScopeUndeclaredError(err) {
		t.Fatal("IsScopeUndeclaredError(ScopeDeniedError) = true, want false")
	}
}

func TestScopeUndeclaredError(t *testing.T) {
	err := error(&mcp.ScopeUndeclaredError{Tool: "blog_list"})
	if !mcp.IsScopeUndeclaredError(err) {
		t.Fatal("IsScopeUndeclaredError = false, want true")
	}
	if mcp.IsScopeUndeclaredError(errors.New("other")) {
		t.Fatal("IsScopeUndeclaredError(other) = true, want false")
	}
}

var errDecode = errors.New("decode arguments")

func testMapper(_ context.Context, err error) mcp.ErrorPayload {
	var denied *mcp.ScopeDeniedError
	if errors.As(err, &denied) {
		return mcp.ErrorPayload{
			Code:    codeForbidden,
			Message: "credential lacks the required scope",
			Meta:    map[string]string{"tool": denied.Tool, "scope": denied.Scope},
		}
	}
	if errors.Is(err, errDecode) {
		return mcp.ErrorPayload{Code: codeDecode, Message: "invalid arguments"}
	}
	return mcp.ErrorPayload{}
}

func testUIResource() mcp.UIResource {
	return mcp.UIResource{URI: "ui://blog/list", Name: "Blog posts", Body: testUIBody}
}

func okHandler(context.Context, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"ok":true}`), nil
}

func uiTool(t *testing.T, opts ...mcp.Option) *sdkmcp.Tool {
	t.Helper()

	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolSpec{Name: "blog_list", Scope: "posts:read", UI: "ui://blog/list", Handler: okHandler}, nil)

	srv := mcp.NewServer(append(opts, mcp.WithRegistry(reg), mcp.WithScopes(scopesFromContext))...)
	srv.AddUIResource(testUIResource())

	res, err := connect(t, srv).ListTools(t.Context(), &sdkmcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) != 1 {
		t.Fatalf("ListTools returned %d tools, want 1", len(res.Tools))
	}
	return res.Tools[0]
}

func textOf(t *testing.T, res *sdkmcp.CallToolResult) string {
	t.Helper()

	if len(res.Content) == 0 {
		t.Fatal("result carries no content")
	}
	text, ok := res.Content[0].(*sdkmcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want *TextContent", res.Content[0])
	}
	return text.Text
}

type scopeKey struct{}

func scopesFromContext(ctx context.Context) []string {
	scopes, _ := ctx.Value(scopeKey{}).([]string)
	return scopes
}

func serve(t *testing.T, srv *mcp.Server, scopes ...string) *httptest.Server {
	t.Helper()

	handler := srv.Handler()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), scopeKey{}, scopes)))
	}))
	t.Cleanup(ts.Close)
	return ts
}

func rpc(t *testing.T, ts *httptest.Server, body string) []byte {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	out, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return out
}

func connect(t *testing.T, srv *mcp.Server, scopes ...string) *sdkmcp.ClientSession {
	t.Helper()

	ts := serve(t, srv, scopes...)
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "v0.0.0"}, nil)
	session, err := client.Connect(t.Context(), &sdkmcp.StreamableClientTransport{
		Endpoint:             ts.URL,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestHandlerServesAProxiedHostOverLoopback(t *testing.T) {
	ts := httptest.NewServer(mcp.NewServer(mcp.WithAllowedHosts("app.example.com")).Handler())
	t.Cleanup(ts.Close)

	body := `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "app.example.com"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %q; a same-host reverse proxy must reach the handler", resp.StatusCode, got)
	}
}
