package boot

import (
	"testing"

	"altalune.id/opensheet/internal/controlplane"
	mcpinternal "altalune.id/opensheet/internal/mcp"
	"altalune.id/opensheet/internal/platform/authn"
	rootmcp "altalune.id/opensheet/mcp"
)

// TestEveryMCPToolHasAScope is the S7 twin of controlplane's TestEveryRPCHasAScope, run against the registry registerTools actually builds rather than a restatement of it. SECURITY: a registered tool with no scope, or one the catalog does not name, is a tool every credential in the fleet could call unchecked.
func TestEveryMCPToolHasAScope(t *testing.T) {
	reg := rootmcp.NewRegistry()
	if err := registerTools(reg, &controlplane.Server{}); err != nil {
		t.Fatalf("registerTools: %v", err)
	}

	names := reg.Names()
	if len(names) == 0 {
		t.Fatal("registerTools registered no tools; this test cannot guard anything")
	}

	table := mcpinternal.ScopeTable()
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			spec, ok := reg.Spec(name)
			if !ok {
				t.Fatalf("tool %q is named by the registry but carries no spec", name)
			}
			if spec.Scope == "" {
				t.Errorf("tool %q is registered with an empty scope; register must fill it from mcp.ScopeTable()", name)
			}
			scope, listed := table[name]
			if !listed {
				t.Errorf("tool %q has no scope entry in mcp.ScopeTable()", name)
				return
			}
			if spec.Scope != scope {
				t.Errorf("tool %q is registered with scope %q but mcp.ScopeTable() declares %q", name, spec.Scope, scope)
			}
			if !authn.Valid(spec.Scope) {
				t.Errorf("tool %q requires scope %q, which is not in the authn catalog, so no credential can hold it", name, spec.Scope)
			}
		})
	}

	for name := range table {
		if _, ok := reg.Spec(name); !ok {
			t.Errorf("mcp.ScopeTable() declares %q but no such tool is registered", name)
		}
	}
}
