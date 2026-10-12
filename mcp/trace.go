package mcp

import (
	"context"
	"encoding/json"
	"runtime/debug"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	methodInitialize = "initialize"
	methodToolsCall  = "tools/call"
)

const (
	outcomeOK    = "ok"
	outcomeError = "error"
	outcomePanic = "panic"
)

func (s *Server) trace(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
	return func(ctx context.Context, method string, req sdkmcp.Request) (sdkmcp.Result, error) {
		if method != methodToolsCall {
			s.traceOther(ctx, method, req)
			return next(ctx, method, req)
		}
		return s.traceToolCall(ctx, next, method, req)
	}
}

// NOTE: /mcp is one path, so this is the only per-tool usage record; the name is logged before dispatch and the outcome from a defer, so a panicking handler still leaves a complete pair.
// SECURITY: the SDK runs tools/call on an unrecovered jsonrpc2.Async goroutine, so without this recover one tool panic kills the process for every tenant; the cause is logged, never returned.
func (s *Server) traceToolCall(ctx context.Context, next sdkmcp.MethodHandler, method string, req sdkmcp.Request) (res sdkmcp.Result, err error) {
	tool := toolNameOf(req)
	s.logger.InfoContext(ctx, "mcp: tool call", "method", method, "tool", tool)
	outcome := outcomePanic
	defer func() {
		if cause := recover(); cause != nil {
			s.logger.ErrorContext(ctx, "mcp: tool handler panicked", "tool", tool, "panic", cause, "stack", string(debug.Stack()))
			res, err = s.unmappedResult(), nil
		}
		s.logger.InfoContext(ctx, "mcp: tool result", "method", method, "tool", tool, "outcome", outcome)
	}()
	res, err = next(ctx, method, req)
	outcome = outcomeOf(res, err)
	return res, err
}

func (s *Server) traceOther(ctx context.Context, method string, req sdkmcp.Request) {
	if method != methodInitialize {
		s.logger.DebugContext(ctx, "mcp: request", "method", method)
		return
	}
	attrs := []any{"method", method}
	if p, ok := req.GetParams().(*sdkmcp.InitializeParams); ok && p != nil && p.ClientInfo != nil {
		capabilities, _ := json.Marshal(p.Capabilities)
		attrs = append(attrs, "client", p.ClientInfo.Name, "client_version", p.ClientInfo.Version, "capabilities", string(capabilities))
	}
	s.logger.InfoContext(ctx, "mcp: request", attrs...)
}

func toolNameOf(req sdkmcp.Request) string {
	switch p := req.GetParams().(type) {
	case *sdkmcp.CallToolParams:
		return p.Name
	case *sdkmcp.CallToolParamsRaw:
		return p.Name
	}
	return ""
}

func outcomeOf(res sdkmcp.Result, err error) string {
	if err != nil {
		return outcomeError
	}
	if call, ok := res.(*sdkmcp.CallToolResult); ok && call != nil && call.IsError {
		return outcomeError
	}
	return outcomeOK
}
