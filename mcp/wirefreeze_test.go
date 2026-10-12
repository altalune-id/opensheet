package mcp_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"altalune.id/opensheet/mcp"
)

//nolint:gochecknoglobals // a -update flag for golden files has to be package level.
var updateGolden = flag.Bool("update", false, "rewrite golden files")

// NOTE: freezes the bytes a host receives; this template is upstream, so a wire change here is one in every fork.
func checkWireGolden(t *testing.T, path string, v any) {
	t.Helper()

	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got = append(got, '\n')

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("mkdir golden dir: %v", err)
		}
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // fixed test path
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("wire changed for %s; rerun with -update once the change is intended.\ngot:\n%s\nwant:\n%s", path, got, want)
	}
}

func wireFreezeServer(t *testing.T, opts ...mcp.Option) *mcp.Server {
	t.Helper()

	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolSpec{
		Name:        "blog_list",
		Description: "List a project's blog posts.",
		Scope:       "posts:read",
		UI:          "ui://opensheet/app",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"projectId": map[string]any{"type": "string"}},
			"required":   []string{"projectId"},
		},
		Handler: okHandler,
	}, nil)
	reg.Register(mcp.ToolSpec{
		Name:        "blog_publish",
		Description: "Publish a draft blog post.",
		Scope:       "posts:write",
		Mutation:    true,
		Handler:     okHandler,
	}, nil)

	return mcp.NewServer(append(opts,
		mcp.WithImplementation("opensheet", "wirefreeze"),
		mcp.WithRegistry(reg),
		mcp.WithScopes(scopesFromContext),
	)...)
}

func TestToolsListWireIsFrozen(t *testing.T) {
	res, err := connect(t, wireFreezeServer(t)).ListTools(t.Context(), &sdkmcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	checkWireGolden(t, "testdata/golden/tools_list.json", res.Tools)
}

func wireFreezeUIServer(t *testing.T) *mcp.Server {
	t.Helper()

	srv := wireFreezeServer(t, mcp.WithUI(true))
	srv.AddUIResource(mcp.UIResource{
		URI:           "ui://opensheet/app",
		Name:          "opensheet app",
		Body:          testUIBody,
		PrefersBorder: new(bool),
	})
	return srv
}

func TestToolsListWireIsFrozenWithUI(t *testing.T) {
	res, err := connect(t, wireFreezeUIServer(t)).ListTools(t.Context(), &sdkmcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	checkWireGolden(t, "testdata/golden/tools_list_ui.json", res.Tools)
}

func TestResourcesListWireIsFrozen(t *testing.T) {
	res, err := connect(t, wireFreezeUIServer(t)).ListResources(t.Context(), &sdkmcp.ListResourcesParams{})
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	checkWireGolden(t, "testdata/golden/resources_list.json", res.Resources)
}

func TestResourcesReadWireIsFrozen(t *testing.T) {
	res, err := connect(t, wireFreezeUIServer(t)).ReadResource(t.Context(), &sdkmcp.ReadResourceParams{URI: "ui://opensheet/app"})
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}
	checkWireGolden(t, "testdata/golden/resources_read.json", res.Contents)
}

func TestInitializeCapabilitiesAreFrozen(t *testing.T) {
	checkWireGolden(t, "testdata/golden/initialize.json", connect(t, wireFreezeServer(t)).InitializeResult().Capabilities)
}

// TestInitializeCapabilitiesWithUIAreFrozen pins that publishing a bundle is what advertises the resources capability.
func TestInitializeCapabilitiesWithUIAreFrozen(t *testing.T) {
	checkWireGolden(t, "testdata/golden/initialize_ui.json", connect(t, wireFreezeUIServer(t)).InitializeResult().Capabilities)
}

func TestToolErrorWireIsFrozen(t *testing.T) {
	session := connect(t, wireFreezeServer(t, mcp.WithErrorMapper(testMapper)), "posts:read")
	res, err := session.CallTool(t.Context(), &sdkmcp.CallToolParams{Name: "blog_publish", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	checkWireGolden(t, "testdata/golden/tool_error.json", res)
}
