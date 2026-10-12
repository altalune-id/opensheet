package mcp

import (
	orgv1mcp "altalune.id/opensheet/gen/go/org/v1/orgv1mcp"
	projectv1mcp "altalune.id/opensheet/gen/go/project/v1/projectv1mcp"
	todov1mcp "altalune.id/opensheet/gen/go/todo/v1/todov1mcp"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/published"
)

// NOTE: these names are a wire contract with every MCP host.
const (
	ToolTodoCreate  = todov1mcp.TodoCreateToolName
	ToolProjectList = projectv1mcp.ProjectListToolName
	ToolMemberList  = orgv1mcp.MemberListToolName
)

// ScopeTable declares the scope a caller must hold for every tool this surface publishes. SECURITY: registration reads this table through ScopeFor, so the runtime check cannot drift from it; a tool missing here resolves to the empty scope, which the root mcp server denies.
func ScopeTable() authn.ScopeTable {
	table := authn.ScopeTable{
		// NOTE: sheets:read, not a projects:read of its own — a new scope string obliges every operator to extend an external authorization server's catalog and re-attach the client before project discovery works at all.
		ToolProjectList: authn.ScopeSheetsRead,
		ToolMemberList:  authn.ScopeMembersRead,
	}
	if published.Todo {
		table[ToolTodoCreate] = authn.ScopeSheetsWrite
	}
	return table
}

// ScopeFor returns the scope tool name requires, or the empty scope for a tool absent from the catalog.
func ScopeFor(name string) string { return ScopeTable()[name] }
