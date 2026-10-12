package ui

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

//go:embed testdata/*.json testdata/dom_stub.js
var fixtures embed.FS

// NOTE: pins the bytes scripts/mcp-ui-vendor.sh fetched or bundled; bump both together.
//
//nolint:gochecknoglobals // a digest table has to be package level.
var vendorSHA256 = map[string]string{
	"assets/ext-apps-2.0.0.js": "fb56376b7583ecafb4820bdebc150abee18feb6258ff84b83c2c944ebd9c3602",
	"assets/lit-3.3.3.js":      "04ad9e0306ed703c52c779d38830a14913cc959f489153837c964111794490a9",
}

func mustRead(t *testing.T, name string) string {
	t.Helper()
	b, err := files.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func fixtureJSON(t *testing.T, name string) string {
	t.Helper()
	raw, err := fixtures.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(raw)
}

func modelFixture(t *testing.T, vm *goja.Runtime, tool, fixture string) (map[string]any, map[string]any) {
	t.Helper()
	v, err := vm.RunString("renderTool(" + strconv.Quote(tool) + ", " + fixtureJSON(t, fixture) + ")")
	if err != nil {
		t.Fatalf("renderTool(%s): %v", tool, err)
	}
	out, ok := v.Export().(map[string]any)
	if !ok {
		t.Fatalf("renderTool returned %T, want an object", v.Export())
	}
	model, ok := out["model"].(map[string]any)
	if !ok {
		t.Fatalf("renderTool(%s).model = %T, want an object", tool, out["model"])
	}
	actions, _ := out["actions"].(map[string]any)
	return model, actions
}

func TestDocumentAssembles(t *testing.T) {
	doc := Document()
	for _, want := range []string{"<!doctype html>", `id="root"`, "ext-apps", "litElementVersions"} {
		if !strings.Contains(doc, want) {
			t.Errorf("Document() missing %q", want)
		}
	}
	if n := strings.Count(doc, `id="root"`); n != 1 {
		t.Errorf(`id="root" appears %d times, want 1`, n)
	}
	if strings.Contains(doc, "{{.") {
		t.Error("Document() has an unexpanded template action")
	}
}

// TestDocumentIsSelfContained: the host renders the bundle under a default CSP of connect-src 'none', so a single external reference blanks the panel.
func TestDocumentIsSelfContained(t *testing.T) {
	doc := Document()
	for _, bad := range []string{`src="http`, `src='http`, `href="http`, `href='http`, "@import", "//unpkg.com", "//cdn."} {
		if strings.Contains(doc, bad) {
			t.Errorf("Document() references %q; the bundle must be one self-contained document", bad)
		}
	}
}

func TestDocumentIncludesEveryScriptPartOnce(t *testing.T) {
	doc := Document()
	for _, p := range scriptParts {
		src := mustRead(t, p)
		if n := strings.Count(doc, src); n != 1 {
			t.Errorf("%s appears %d times in Document(), want 1", p, n)
		}
	}
}

// TestEachVendorBundleGetsItsOwnModuleScript: two minified bundles share single-letter top-level names, so one scope would collide.
func TestEachVendorBundleGetsItsOwnModuleScript(t *testing.T) {
	doc := Document()
	if n := strings.Count(doc, `<script type="module">`); n != len(vendorParts)+1 {
		t.Errorf("Document() has %d module scripts, want %d", n, len(vendorParts)+1)
	}
	for _, v := range vendorParts {
		if !strings.Contains(doc, "globalThis."+v.global+"={") {
			t.Errorf("Document() never exposes %s's exports", v.path)
		}
	}
}

func TestNoHostSpecificAPIs(t *testing.T) {
	for _, p := range scriptParts {
		if strings.Contains(mustRead(t, p), "window.openai") {
			t.Errorf("%s uses window.openai; the bundle must stay portable across hosts", p)
		}
	}
}

func TestExposeExportsBindsEveryNameTheBundleUses(t *testing.T) {
	doc := Document()
	for _, name := range []string{"App", "applyDocumentTheme", "applyHostStyleVariables", "applyHostFonts", "LitElement", "html", "css", "nothing"} {
		if !strings.Contains(doc, name+":") {
			t.Errorf("no vendored bundle binds %q — the upstream renamed or dropped it", name)
		}
	}
}

// TestExposeExportsRemovesTheExportStatement: goja cannot parse an ESM export, and the harness evaluates the vendored Lit verbatim.
func TestExposeExportsRemovesTheExportStatement(t *testing.T) {
	for _, v := range vendorParts {
		if exportStmt.MatchString(exposeExports(mustRead(t, v.path), v.global)) {
			t.Errorf("%s still ends in an export statement", v.path)
		}
	}
}

func TestEveryEmbeddedPartIsSafeToInline(t *testing.T) {
	parts := append([]string{"app.css"}, scriptParts...)
	for _, v := range vendorParts {
		parts = append(parts, v.path)
	}
	for _, p := range parts {
		src := mustRead(t, p)
		for _, bad := range []string{"</script", "<!--"} {
			if strings.Contains(src, bad) {
				t.Errorf("%s contains %q, which would truncate the inlined document", p, bad)
			}
		}
	}
}

// TestVendorMatchesPinnedDigest fails on any byte the vendoring script did not verify.
func TestVendorMatchesPinnedDigest(t *testing.T) {
	for _, v := range vendorParts {
		want, ok := vendorSHA256[v.path]
		if !ok {
			t.Errorf("%s has no pinned digest", v.path)
			continue
		}
		sum := sha256.Sum256([]byte(mustRead(t, v.path)))
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf("vendored %s sha256 = %s, want %s — re-run scripts/mcp-ui-vendor.sh and bump both pins together", v.path, got, want)
		}
	}
}

func TestResourceURIMatchesBufPrefix(t *testing.T) {
	const want = "ui://opensheet/app"
	if ResourceURI != want {
		t.Errorf("ResourceURI = %q, want %q — it must match buf.gen.yaml's ui_prefix plus the proto's ui name", ResourceURI, want)
	}
}

// TestScriptPartsLoadTheirDependenciesFirst: const is in the TDZ until its line runs, so a part loading early blanks the panel.
func TestScriptPartsLoadTheirDependenciesFirst(t *testing.T) {
	for _, dep := range []struct{ provider, kind string }{
		{"src/lit.js", "src/views/"},
		{"src/styles.js", "src/views/"},
		{"src/registry.js", "src/views/"},
	} {
		at := slices.Index(scriptParts, dep.provider)
		if at < 0 {
			t.Fatalf("%s is not in scriptParts", dep.provider)
		}
		for i, p := range scriptParts {
			if strings.HasPrefix(p, dep.kind) && i < at {
				t.Errorf("%s loads before %s", p, dep.provider)
			}
		}
	}
	for _, after := range []string{"src/app.js", "src/views/blog_list.js"} {
		if slices.Index(scriptParts, after) < slices.Index(scriptParts, "src/lit.js") {
			t.Errorf("%s loads before src/lit.js", after)
		}
	}
}
