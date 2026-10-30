// Package mcp wraps the Model Context Protocol Go SDK's streamable-HTTP server with a scope-checked tool registry.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultName    = "opensheet"
	defaultVersion = "0.0.0"
)

// NOTE: McpUiToolMeta is additionalProperties:false, so any sibling key breaks strict hosts.
const metaKeyUI = "ui"

// Server is a streamable-HTTP MCP server built by NewServer and mounted through Handler.
type Server struct {
	name       string
	version    string
	ui         bool
	scopesFrom ScopesFunc
	mapErr     ErrorMapper
	logger     *slog.Logger
	registry   *Registry

	unmappedCode string
	allowedHosts []string

	mu        sync.Mutex
	sealed    bool
	resources map[string]UIResource

	once sync.Once
	sdk  *sdkmcp.Server
}

// NewServer builds a Server from opts.
func NewServer(opts ...Option) *Server {
	s := &Server{
		name:         defaultName,
		version:      defaultVersion,
		registry:     NewRegistry(),
		resources:    map[string]UIResource{},
		logger:       slog.New(slog.DiscardHandler),
		unmappedCode: DefaultUnmappedCode,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Registry returns the server's tool registry.
func (s *Server) Registry() *Registry { return s.registry }

// SDK returns the underlying SDK server and seals the registry, so a later Register panics rather than vanishing.
func (s *Server) SDK() *sdkmcp.Server {
	s.once.Do(s.build)
	return s.sdk
}

// Handler returns the stateless streamable-HTTP handler for this server, sealing the registry.
func (s *Server) Handler() http.Handler {
	sdk := s.SDK()
	opts := &sdkmcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, Logger: s.logger}
	if len(s.allowedHosts) == 0 {
		return sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return sdk }, opts)
	}
	opts.DisableLocalhostProtection = true
	next := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return sdk }, opts)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.rebound(r) {
			http.Error(w, fmt.Sprintf("Forbidden: invalid Host header %q", r.Host), http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// SECURITY: the SDK's DNS-rebinding check plus the allowed hosts; see
// https://modelcontextprotocol.io/specification/2025-11-25/basic/security_best_practices
func (s *Server) rebound(r *http.Request) bool {
	local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok || local == nil || !isLoopback(local.String()) || isLoopback(r.Host) {
		return false
	}
	return !slices.Contains(s.allowedHosts, strings.ToLower(hostOf(r.Host)))
}

func hostOf(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return strings.Trim(addr, "[]")
	}
	return host
}

func isLoopback(addr string) bool {
	host := hostOf(addr)
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

func (s *Server) build() {
	s.sdk = sdkmcp.NewServer(
		&sdkmcp.Implementation{Name: s.name, Version: s.version},
		&sdkmcp.ServerOptions{Logger: s.logger},
	)
	s.sdk.AddReceivingMiddleware(s.trace)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sealed = true
	specs := s.registry.seal()
	s.mustResolved(specs)
	for _, spec := range specs {
		s.sdk.AddTool(s.tool(spec), s.dispatch(spec))
	}
	s.addResources()
}

func (s *Server) tool(spec ToolSpec) *sdkmcp.Tool {
	schema := spec.InputSchema
	if schema == nil {
		schema = map[string]any{"type": "object"}
	}
	title := spec.Title
	if title == "" {
		title = titleOf(spec.Name)
	}
	// NOTE: both hints default to true when absent, so both are always sent; an inaccurate destructiveHint desensitizes the host's confirmation prompt.
	destructive := spec.Destructive
	openWorld := false
	tool := &sdkmcp.Tool{
		Name:        spec.Name,
		Title:       title,
		Description: spec.Description,
		InputSchema: schema,
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:    !spec.Mutation,
			DestructiveHint: &destructive,
			OpenWorldHint:   &openWorld,
		},
	}
	if s.ui && spec.UI != "" {
		// NOTE: exactly one key. The deprecated flat "ui/resourceUri" sibling is not emitted — a host
		// validating _meta against additionalProperties:false rejects the pair and renders nothing.
		tool.Meta = sdkmcp.Meta{metaKeyUI: map[string]any{"resourceUri": spec.UI}}
	}
	return tool
}

// SECURITY: failures answer in the result with IsError — a bare error yields WireError code zero, which is invalid.
func (s *Server) dispatch(spec ToolSpec) sdkmcp.ToolHandler {
	return func(ctx context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		if err := s.authorize(ctx, spec); err != nil {
			return s.failure(ctx, spec.Name, err), nil
		}
		out, err := spec.Handler(ctx, normalize(req.Params.Arguments))
		if err != nil {
			return s.failure(ctx, spec.Name, err), nil
		}
		return s.success(ctx, spec.Name, out), nil
	}
}

func (s *Server) authorize(ctx context.Context, spec ToolSpec) error {
	if spec.Scope == "" {
		return &ScopeUndeclaredError{Tool: spec.Name}
	}
	if s.scopesFrom == nil {
		return &ScopeDeniedError{Tool: spec.Name, Scope: spec.Scope}
	}
	if !slices.Contains(s.scopesFrom(ctx), spec.Scope) {
		return &ScopeDeniedError{Tool: spec.Name, Scope: spec.Scope}
	}
	return nil
}

func (s *Server) success(ctx context.Context, tool string, out json.RawMessage) *sdkmcp.CallToolResult {
	raw := normalize(out)
	var structured map[string]any
	if err := json.Unmarshal(raw, &structured); err != nil {
		s.logger.ErrorContext(ctx, "mcp: tool returned JSON that is not an object", "tool", tool, "error", err)
		return s.unmappedResult()
	}
	if structured == nil {
		s.logger.ErrorContext(ctx, "mcp: tool returned a null result", "tool", tool)
		return s.unmappedResult()
	}
	return &sdkmcp.CallToolResult{
		StructuredContent: structured,
		Content:           []sdkmcp.Content{&sdkmcp.TextContent{Text: string(raw)}},
	}
}

// SECURITY: an unmapped cause is logged, never returned — a raw Go error here is information disclosure.
func (s *Server) failure(ctx context.Context, tool string, cause error) *sdkmcp.CallToolResult {
	payload := s.mapped(ctx, cause)
	if payload.Code == "" {
		s.logger.ErrorContext(ctx, "mcp: unmapped tool failure", "tool", tool, "error", cause)
		return s.unmappedResult()
	}
	return errorResult(payload)
}

func (s *Server) mapped(ctx context.Context, cause error) ErrorPayload {
	if s.mapErr == nil {
		return ErrorPayload{}
	}
	return s.mapErr(ctx, cause)
}

func (s *Server) unmappedResult() *sdkmcp.CallToolResult {
	return errorResult(ErrorPayload{Code: s.unmappedCode, Message: unmappedMessage})
}

func titleOf(name string) string {
	title := strings.ReplaceAll(name, "_", " ")
	if title == "" {
		return title
	}
	return strings.ToUpper(title[:1]) + title[1:]
}

func errorResult(payload ErrorPayload) *sdkmcp.CallToolResult {
	raw, _ := json.Marshal(payload)
	return &sdkmcp.CallToolResult{
		IsError: true,
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: string(raw)}},
	}
}

func normalize(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("{}")
	}
	return raw
}
