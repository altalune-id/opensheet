package mcp

import (
	"context"
	"log/slog"
)

// ScopesFunc resolves the scopes the caller carries, for the per-tool scope check.
type ScopesFunc func(ctx context.Context) []string

// ErrorMapper maps a tool failure onto the caller-visible payload; a payload with no Code leaves it unmapped.
type ErrorMapper func(ctx context.Context, err error) ErrorPayload

// Option configures a Server.
type Option func(*Server)

// WithScopes wires the caller's per-tool scope check into the server; without it every tool call is denied.
func WithScopes(f ScopesFunc) Option {
	return func(s *Server) { s.scopesFrom = f }
}

// WithRegistry attaches reg's tools to the server.
func WithRegistry(reg *Registry) Option {
	return func(s *Server) { s.registry = reg }
}

// WithImplementation sets the name and version the server reports to MCP hosts.
func WithImplementation(name, version string) Option {
	return func(s *Server) {
		s.name = name
		s.version = version
	}
}

// WithErrorMapper wires the app's mapping from a tool failure to the caller-visible payload.
func WithErrorMapper(m ErrorMapper) Option {
	return func(s *Server) { s.mapErr = m }
}

// WithLogger sets the logger the server records unmapped tool failures on.
func WithLogger(l *slog.Logger) Option {
	return func(s *Server) {
		if l != nil {
			s.logger = l
		}
	}
}

// WithUnmappedCode sets the code an unmapped tool failure answers with, for a fork whose error registry does not number DefaultUnmappedCode.
func WithUnmappedCode(code string) Option {
	return func(s *Server) {
		if code != "" {
			s.unmappedCode = code
		}
	}
}

// WithUI publishes ToolSpec.UI as the MCP Apps `_meta.ui` link when enabled is true.
func WithUI(enabled bool) Option {
	return func(s *Server) { s.ui = enabled }
}
