package mcp_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"altalune.id/opensheet/mcp"
)

// Vendored from the MCP Apps ext-apps bundle: https://modelcontextprotocol.io/specification/ (apps.mdx).
const extAppsSchemaPath = "testdata/ext-apps-schema-2.0.0.json"

const metaKeyUI = "ui"

func extAppsSchema(t *testing.T, def string) *jsonschema.Resolved {
	t.Helper()

	bundleRaw, err := os.ReadFile(extAppsSchemaPath) //nolint:gosec // fixed test path
	if err != nil {
		t.Fatalf("read vendored schema: %v", err)
	}
	var bundle struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(bundleRaw, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	raw, ok := bundle.Defs[def]
	if !ok {
		t.Fatalf("$defs/%s missing — ext-apps moved it; re-pin against apps.mdx", def)
	}
	var s jsonschema.Schema
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("unmarshal %s: %v", def, err)
	}
	resolved, err := s.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return resolved
}

func roundTrip(t *testing.T, v any) any {
	t.Helper()

	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func TestEmittedUIMetaMatchesOfficialSchema(t *testing.T) {
	tool := uiTool(t, mcp.WithUI(true))
	if err := extAppsSchema(t, "McpUiToolMeta").Validate(roundTrip(t, tool.Meta[metaKeyUI])); err != nil {
		t.Errorf("emitted _meta[%q] violates McpUiToolMeta: %v", metaKeyUI, err)
	}
}

// TestWholeMetaAlsoValidates pins that the whole _meta object still passes an additionalProperties:false host.
func TestWholeMetaAlsoValidates(t *testing.T) {
	whole, ok := roundTrip(t, map[string]any(uiTool(t, mcp.WithUI(true)).Meta)).(map[string]any)
	if !ok {
		t.Fatal("_meta is not an object")
	}
	inner, ok := whole[metaKeyUI]
	if !ok {
		t.Fatalf("_meta has no %q key: %v", metaKeyUI, whole)
	}
	if err := extAppsSchema(t, "McpUiToolMeta").Validate(inner); err != nil {
		t.Errorf("_meta.%s violates McpUiToolMeta: %v", metaKeyUI, err)
	}
	if len(whole) != 1 {
		t.Errorf("_meta carries %d keys; a sibling of %q breaks strict hosts", len(whole), metaKeyUI)
	}
}

func TestPublishedResourceMetaMatchesOfficialSchema(t *testing.T) {
	r := testUIResource()
	r.PrefersBorder = new(bool)
	session := connect(t, uiServer(t, r, mcp.WithUI(true)))

	list, err := session.ListResources(t.Context(), &sdkmcp.ListResourcesParams{})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	read, err := session.ReadResource(t.Context(), &sdkmcp.ReadResourceParams{URI: "ui://blog/list"})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	schema := extAppsSchema(t, "McpUiResourceMeta")
	for name, meta := range map[string]sdkmcp.Meta{
		"resources/list": list.Resources[0].Meta,
		"resources/read": read.Contents[0].Meta,
	} {
		if err := schema.Validate(roundTrip(t, meta[metaKeyUI])); err != nil {
			t.Errorf("%s: _meta[%q] violates McpUiResourceMeta: %v", name, metaKeyUI, err)
		}
		whole, ok := roundTrip(t, map[string]any(meta)).(map[string]any)
		if !ok {
			t.Fatalf("%s: _meta is not an object", name)
		}
		if len(whole) != 1 {
			t.Errorf("%s: _meta carries %d keys; a sibling of %q breaks strict hosts", name, len(whole), metaKeyUI)
		}
	}
}

// TestResourceMetaRejectsAnUnmodelledKey pins that McpUiResourceMeta is additionalProperties:false, which is why UIResource carries typed fields rather than a raw map.
func TestResourceMetaRejectsAnUnmodelledKey(t *testing.T) {
	if err := extAppsSchema(t, "McpUiResourceMeta").Validate(map[string]any{"prefersBorder": false, "theme": "dark"}); err == nil {
		t.Error("McpUiResourceMeta accepted an unmodelled key; a raw map would have shipped it to strict hosts")
	}
}

// TestNoDeprecatedFlatUIKeyIsEmitted asserts the deprecated flat "ui/resourceUri" key is absent, since McpUiToolMeta is additionalProperties:false.
func TestNoDeprecatedFlatUIKeyIsEmitted(t *testing.T) {
	const flat = "ui/resourceUri"

	r := testUIResource()
	r.PrefersBorder = new(bool)
	session := connect(t, uiServer(t, r, mcp.WithUI(true)))

	list, err := session.ListResources(t.Context(), &sdkmcp.ListResourcesParams{})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	read, err := session.ReadResource(t.Context(), &sdkmcp.ReadResourceParams{URI: r.URI})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}

	for name, meta := range map[string]sdkmcp.Meta{
		"tools/list":     uiTool(t, mcp.WithUI(true)).Meta,
		"resources/list": list.Resources[0].Meta,
		"resources/read": read.Contents[0].Meta,
	} {
		if _, bad := meta[flat]; bad {
			t.Errorf("%s: _meta carries the deprecated %q sibling; a strict host rejects the pair", name, flat)
		}
		if _, ok := meta[metaKeyUI]; !ok {
			t.Errorf("%s: _meta has no %q key", name, metaKeyUI)
		}
		if len(meta) != 1 {
			t.Errorf("%s: _meta carries %d keys, want exactly 1 (%q)", name, len(meta), metaKeyUI)
		}
	}
}
