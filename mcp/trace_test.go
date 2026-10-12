package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"altalune.id/opensheet/mcp"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func traceServer(t *testing.T, sink *syncBuffer, handler mcp.ToolHandler) *mcp.Server {
	t.Helper()

	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolSpec{Name: "blog_list", Scope: "posts:read", Handler: handler}, nil)
	return mcp.NewServer(
		mcp.WithRegistry(reg),
		mcp.WithScopes(scopesFromContext),
		mcp.WithErrorMapper(testMapper),
		mcp.WithLogger(slog.New(slog.NewTextHandler(sink, &slog.HandlerOptions{Level: slog.LevelInfo}))),
	)
}

func TestTraceLogsTheToolNameAtInfo(t *testing.T) {
	var sink syncBuffer
	session := connect(t, traceServer(t, &sink, okHandler), "posts:read")

	if _, err := session.ListTools(t.Context(), &sdkmcp.ListToolsParams{}); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if _, err := session.CallTool(t.Context(), &sdkmcp.CallToolParams{Name: "blog_list"}); err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	out := sink.String()
	if strings.Contains(out, "tools/list") {
		t.Errorf("protocol chatter reached Info; only initialize and tools/call belong there:\n%s", out)
	}
	if !strings.Contains(out, "tool=blog_list") {
		t.Errorf("tools/call did not log the tool name at Info; the endpoint is one path, so nothing else records which tool ran:\n%s", out)
	}
}

// TestTraceLogsTheClientAtInitialize drives initialize over raw JSON-RPC: an SDK client on the current protocol sends server/discover instead, but deployed hosts still initialize.
func TestTraceLogsTheClientAtInitialize(t *testing.T) {
	var sink syncBuffer
	ts := serve(t, traceServer(t, &sink, okHandler), "posts:read")
	rpc(t, ts, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"9.9.9"}}}`)

	out := sink.String()
	for _, want := range []string{"method=initialize", "client=probe", "client_version=9.9.9", "capabilities="} {
		if !strings.Contains(out, want) {
			t.Errorf("the initialize log is missing %q:\n%s", want, out)
		}
	}
}

// TestTraceLogsTheToolNameBeforeDispatch pins the ordering that makes the audit record survive a handler that never returns.
func TestTraceLogsTheToolNameBeforeDispatch(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var sink syncBuffer
	srv := traceServer(t, &sink, func(context.Context, json.RawMessage) (json.RawMessage, error) {
		close(entered)
		<-release
		return json.RawMessage(`{"ok":true}`), nil
	})
	session := connect(t, srv, "posts:read")

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = session.CallTool(t.Context(), &sdkmcp.CallToolParams{Name: "blog_list"})
	}()

	<-entered
	out := sink.String()
	if !strings.Contains(out, "tool=blog_list") {
		t.Errorf("the tool name was not logged before dispatch; a handler that panics or hangs would leave no audit record:\n%s", out)
	}
	if strings.Contains(out, "outcome=") {
		t.Errorf("an outcome was logged before the handler returned:\n%s", out)
	}
	close(release)
	<-done
}

// TestAPanickingToolHandlerIsContainedAndAudited runs in-process on purpose: before the recover the SDK's unrecovered jsonrpc2.Async goroutine took the test binary down and this had to fork a child.
func TestAPanickingToolHandlerIsContainedAndAudited(t *testing.T) {
	tests := []struct {
		name    string
		handler mcp.ToolHandler
		wantLog string
	}{
		{
			name:    "string panic",
			handler: func(context.Context, json.RawMessage) (json.RawMessage, error) { panic("handler blew up") },
			wantLog: "handler blew up",
		},
		{
			name: "index out of range",
			handler: func(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
				rows := make([]string, 0, 4)
				return json.RawMessage(rows[len(args)]), nil
			},
			wantLog: "index out of range",
		},
		{
			name: "bad type assertion",
			handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
				var v any = "not an int"
				_ = v.(int)
				return nil, nil
			},
			wantLog: "interface conversion",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var sink syncBuffer
			session := connect(t, traceServer(t, &sink, tc.handler), "posts:read")

			res, err := session.CallTool(t.Context(), &sdkmcp.CallToolParams{Name: "blog_list"})
			if err != nil {
				t.Fatalf("CallTool returned a transport error, so the panic was not converted to a result: %v", err)
			}
			if !res.IsError {
				t.Fatal("a panicking handler answered a success result")
			}

			text := textOf(t, res)
			var payload mcp.ErrorPayload
			if decErr := json.Unmarshal([]byte(text), &payload); decErr != nil {
				t.Fatalf("the panic result is not a well-formed ErrorPayload: %v (%s)", decErr, text)
			}
			if payload.Code != mcp.DefaultUnmappedCode {
				t.Errorf("payload.Code = %q, want the unmapped %q", payload.Code, mcp.DefaultUnmappedCode)
			}
			if payload.Message != "unexpected error" {
				t.Errorf("payload.Message = %q, want the generic unmapped message", payload.Message)
			}

			wire, mErr := json.Marshal(res)
			if mErr != nil {
				t.Fatalf("marshal result: %v", mErr)
			}
			for _, leak := range []string{tc.wantLog, "goroutine ", "altalune.id/opensheet/mcp"} {
				if strings.Contains(string(wire), leak) {
					t.Errorf("the caller-visible result leaks %q; an agent-reachable surface must not receive the panic value or a stack:\n%s", leak, wire)
				}
			}

			out := sink.String()
			if !strings.Contains(out, tc.wantLog) {
				t.Errorf("the panic cause was not logged server-side, want %q:\n%s", tc.wantLog, out)
			}
			if !strings.Contains(out, "tool=blog_list") {
				t.Errorf("the audit line lost the tool name:\n%s", out)
			}
			if !strings.Contains(out, "outcome=panic") {
				t.Errorf("the audit line did not record outcome=panic, so a contained crash is indistinguishable from a tool error:\n%s", out)
			}
		})
	}
}

func TestTraceRecordsTheOutcomeAfterDispatch(t *testing.T) {
	tests := []struct {
		name    string
		handler mcp.ToolHandler
		want    string
	}{
		{"success", okHandler, "outcome=ok"},
		{"mapped failure", func(context.Context, json.RawMessage) (json.RawMessage, error) { return nil, errDecode }, "outcome=error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var sink syncBuffer
			session := connect(t, traceServer(t, &sink, tc.handler), "posts:read")
			if _, err := session.CallTool(t.Context(), &sdkmcp.CallToolParams{Name: "blog_list"}); err != nil {
				t.Fatalf("CallTool: %v", err)
			}
			if out := sink.String(); !strings.Contains(out, tc.want) {
				t.Errorf("the outcome was not recorded after dispatch, want %q:\n%s", tc.want, out)
			}
		})
	}
}
