package mcp_test

import (
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"altalune.id/opensheet/mcp"
)

func annotatedTool(t *testing.T, spec mcp.ToolSpec) *sdkmcp.Tool {
	t.Helper()

	spec.Handler = okHandler
	reg := mcp.NewRegistry()
	reg.Register(spec, nil)

	res, err := connect(t, mcp.NewServer(mcp.WithRegistry(reg), mcp.WithScopes(scopesFromContext))).
		ListTools(t.Context(), &sdkmcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) != 1 {
		t.Fatalf("ListTools returned %d tools, want 1", len(res.Tools))
	}
	return res.Tools[0]
}

func TestToolAnnotationsFollowTheSpec(t *testing.T) {
	tests := []struct {
		name            string
		spec            mcp.ToolSpec
		wantReadOnly    bool
		wantDestructive bool
	}{
		{"read", mcp.ToolSpec{Name: "blog_list", Scope: "posts:read"}, true, false},
		{"additive mutation", mcp.ToolSpec{Name: "blog_publish", Scope: "posts:write", Mutation: true}, false, false},
		{"destructive mutation", mcp.ToolSpec{Name: "blog_delete", Scope: "posts:write", Mutation: true, Destructive: true}, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tool := annotatedTool(t, tc.spec)
			if tool.Annotations.ReadOnlyHint != tc.wantReadOnly {
				t.Errorf("readOnlyHint = %v, want %v", tool.Annotations.ReadOnlyHint, tc.wantReadOnly)
			}
			if tool.Annotations.DestructiveHint == nil {
				t.Fatal("destructiveHint is absent; the MCP spec defaults it to true, so an additive tool must say false explicitly")
			}
			if *tool.Annotations.DestructiveHint != tc.wantDestructive {
				t.Errorf("destructiveHint = %v, want %v; an inaccurate hint desensitizes the host confirmation prompt", *tool.Annotations.DestructiveHint, tc.wantDestructive)
			}
			if tool.Annotations.OpenWorldHint == nil {
				t.Fatal("openWorldHint is absent; the MCP spec defaults it to true, but these tools touch only their own database")
			}
			if *tool.Annotations.OpenWorldHint {
				t.Error("openWorldHint = true, want false")
			}
		})
	}
}

func TestToolTitleIsHumanReadable(t *testing.T) {
	tests := []struct {
		name string
		spec mcp.ToolSpec
		want string
	}{
		{"derived from a snake_case name", mcp.ToolSpec{Name: "blog_publish", Scope: "posts:write"}, "Blog publish"},
		{"derived from a single word", mcp.ToolSpec{Name: "ping", Scope: "posts:read"}, "Ping"},
		{"explicit title wins", mcp.ToolSpec{Name: "api_key_create", Scope: "posts:write", Title: "Create an API key"}, "Create an API key"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := annotatedTool(t, tc.spec).Title; got != tc.want {
				t.Errorf("Title = %q, want %q; hosts show Name when Title is empty", got, tc.want)
			}
		})
	}
}
