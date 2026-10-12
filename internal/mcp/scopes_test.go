package mcp_test

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	orgv1mcp "altalune.id/opensheet/gen/go/org/v1/orgv1mcp"
	projectv1mcp "altalune.id/opensheet/gen/go/project/v1/projectv1mcp"
	todov1mcp "altalune.id/opensheet/gen/go/todo/v1/todov1mcp"
	mcpinternal "altalune.id/opensheet/internal/mcp"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/published"
)

// TestScopeTableDeclaresHoldableScopes keeps every catalog entry reachable: a tool demanding a scope outside the authn catalog can never be called, because no credential can carry it.
func TestScopeTableDeclaresHoldableScopes(t *testing.T) {
	table := mcpinternal.ScopeTable()
	require.NotEmpty(t, table, "the scope catalog is empty; nothing below guards anything")

	for name, scope := range table {
		t.Run(name, func(t *testing.T) {
			require.NotEmpty(t, scope, "tool %q declares the empty scope, which every caller is denied", name)
			require.True(t, authn.Valid(scope),
				"tool %q requires %q, which is not in the authn scope catalog, so no credential can hold it", name, scope)
		})
	}
}

// TestScopeForMatchesTheTable keeps ScopeFor the single read path onto the catalog, and keeps it fail-closed for a name nobody tabulated.
func TestScopeForMatchesTheTable(t *testing.T) {
	table := mcpinternal.ScopeTable()

	for name, scope := range table {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, scope, mcpinternal.ScopeFor(name),
				"ScopeFor(%q) disagrees with ScopeTable(); registration would demand a scope the catalog does not declare", name)
		})
	}

	t.Run("an untabulated tool", func(t *testing.T) {
		require.Empty(t, mcpinternal.ScopeFor("no_such_tool"),
			"ScopeFor invented a scope for an untabulated tool; the root mcp server must deny it instead")
	})
}

// TestEveryGeneratedToolIsTabulated keeps the catalog and the protos one set, enumerated from the generator rather than restated by hand. SECURITY: an annotated RPC nobody tabulated registers with the empty scope, which is a tool the root mcp server refuses; a row naming no generated tool is a scope nothing will ever check.
func TestEveryGeneratedToolIsTabulated(t *testing.T) {
	generated := slices.Concat(projectv1mcp.ProjectServiceToolNames(), orgv1mcp.MemberServiceToolNames())
	if published.Todo {
		generated = append(generated, todov1mcp.TodoServiceToolNames()...)
	}
	require.NotEmpty(t, generated, "the generator emitted no tool names; nothing below guards anything")

	table := mcpinternal.ScopeTable()
	for _, name := range generated {
		t.Run(name, func(t *testing.T) {
			_, ok := table[name]
			require.True(t, ok, "the proto generates tool %q, but ScopeTable() has no entry for it", name)
		})
	}

	for name := range table {
		require.Contains(t, generated, name, "ScopeTable() declares %q, which no .proto generates", name)
	}
}
