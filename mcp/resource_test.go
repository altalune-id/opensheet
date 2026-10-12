package mcp_test

import (
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"altalune.id/opensheet/mcp"
)

const testUIBody = "<!doctype html><html><head><title>t</title></head><body><div id=\"root\"></div></body></html>"

func uiServer(t *testing.T, r mcp.UIResource, opts ...mcp.Option) *mcp.Server {
	t.Helper()

	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolSpec{Name: "blog_list", Scope: "posts:read", UI: "ui://blog/list", Handler: okHandler}, nil)
	srv := mcp.NewServer(append(opts, mcp.WithRegistry(reg), mcp.WithScopes(scopesFromContext))...)
	srv.AddUIResource(r)
	return srv
}

func wantPanic(t *testing.T, contains string, f func()) {
	t.Helper()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("want a panic containing %q, got none", contains)
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, contains) {
			t.Fatalf("panic = %v, want one containing %q", r, contains)
		}
	}()
	f()
}

func TestReadResourceServesTheBundle(t *testing.T) {
	session := connect(t, uiServer(t, testUIResource(), mcp.WithUI(true)))

	res, err := session.ReadResource(t.Context(), &sdkmcp.ReadResourceParams{URI: "ui://blog/list"})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(res.Contents) != 1 {
		t.Fatalf("ReadResource returned %d contents, want 1", len(res.Contents))
	}
	c := res.Contents[0]
	if c.URI != "ui://blog/list" {
		t.Errorf("uri = %q, want ui://blog/list", c.URI)
	}
	if c.MIMEType != mcp.MIMEApp {
		t.Errorf("mimeType = %q, want %q", c.MIMEType, mcp.MIMEApp)
	}
	if c.Text != testUIBody {
		t.Errorf("text = %q, want the published body", c.Text)
	}
}

func TestListResourcesAdvertisesTheBundle(t *testing.T) {
	session := connect(t, uiServer(t, testUIResource(), mcp.WithUI(true)))

	res, err := session.ListResources(t.Context(), &sdkmcp.ListResourcesParams{})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(res.Resources) != 1 {
		t.Fatalf("ListResources returned %d resources, want 1", len(res.Resources))
	}
	if got := res.Resources[0].URI; got != "ui://blog/list" {
		t.Errorf("uri = %q, want ui://blog/list", got)
	}
}

func TestPrefersBorderIsCarriedOnBothListAndRead(t *testing.T) {
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
	for name, meta := range map[string]sdkmcp.Meta{
		"resources/list": list.Resources[0].Meta,
		"resources/read": read.Contents[0].Meta,
	} {
		ui, ok := meta[metaKeyUI].(map[string]any)
		if !ok {
			t.Fatalf("%s: _meta.%s = %v, want an object", name, metaKeyUI, meta[metaKeyUI])
		}
		if ui["prefersBorder"] != false {
			t.Errorf("%s: prefersBorder = %v, want false", name, ui["prefersBorder"])
		}
		if len(ui) != 1 {
			t.Errorf("%s: _meta.%s carries %d keys; McpUiResourceMeta is additionalProperties:false", name, metaKeyUI, len(ui))
		}
	}
}

func TestResourceMetaIsAbsentWhenNothingIsDeclared(t *testing.T) {
	session := connect(t, uiServer(t, testUIResource(), mcp.WithUI(true)))

	res, err := session.ReadResource(t.Context(), &sdkmcp.ReadResourceParams{URI: "ui://blog/list"})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if meta := res.Contents[0].Meta; meta != nil {
		t.Errorf("_meta = %v, want none when the resource declares nothing", meta)
	}
}

// TestUnpublishedUIReferenceFailsTheBuild is the guard that this backlog item existed for: an advertised ui:// link that resolves to nothing renders a blank panel in every host.
func TestUnpublishedUIReferenceFailsTheBuild(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolSpec{Name: "blog_list", Scope: "posts:read", UI: "ui://blog/list", Handler: okHandler}, nil)
	srv := mcp.NewServer(mcp.WithRegistry(reg), mcp.WithScopes(scopesFromContext), mcp.WithUI(true))

	wantPanic(t, "references unpublished UI resource ui://blog/list", func() { _ = srv.Handler() })
}

func TestUnpublishedUIReferenceIsToleratedWithoutWithUI(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolSpec{Name: "blog_list", Scope: "posts:read", UI: "ui://blog/list", Handler: okHandler}, nil)
	srv := mcp.NewServer(mcp.WithRegistry(reg), mcp.WithScopes(scopesFromContext))

	if _, err := connect(t, srv).ListTools(t.Context(), nil); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
}

func TestAddUIResourceRejectsWhatAHostCannotRender(t *testing.T) {
	tests := []struct {
		name, contains string
		res            mcp.UIResource
	}{
		{"empty uri", "URI must not be empty", mcp.UIResource{Body: testUIBody}},
		{"wrong scheme", "scheme must be ui", mcp.UIResource{URI: "https://blog/list", Body: testUIBody}},
		{"empty body", "Body must not be empty", mcp.UIResource{URI: "ui://blog/list"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wantPanic(t, tc.contains, func() { mcp.NewServer().AddUIResource(tc.res) })
		})
	}
}

func TestAddUIResourceRejectsADuplicate(t *testing.T) {
	srv := mcp.NewServer()
	srv.AddUIResource(testUIResource())

	wantPanic(t, "duplicate UI resource ui://blog/list", func() { srv.AddUIResource(testUIResource()) })
}

func TestAddUIResourceAfterBuildPanicsRatherThanVanishing(t *testing.T) {
	srv := mcp.NewServer()
	_ = srv.SDK()

	wantPanic(t, "published after the server was built", func() { srv.AddUIResource(testUIResource()) })
}
