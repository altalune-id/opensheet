// Package ui assembles this template's MCP Apps bundle: one self-contained HTML document published at ui://opensheet/app.
package ui

import (
	"embed"
	"regexp"
	"strings"
	"sync"
	"text/template"
)

// ResourceURI is the ui:// URI the bundle is published at; it must match buf.gen.yaml's ui_prefix plus the proto's ui name.
const ResourceURI = "ui://opensheet/app"

//go:embed shell.html app.css assets/ext-apps-2.0.0.js assets/lit-3.3.3.js
//go:embed src/lit.js src/styles.js src/format.js src/color.js src/registry.js
//go:embed src/views/blog_list_model.js src/views/blog_list.js src/app.js src/bridge.js src/boot.js
var files embed.FS

type vendorPart struct {
	path   string
	global string
}

// NOTE: each vendored bundle gets its own <script type="module"> — concatenating two minified bundles into one scope collides on their single-letter top-level names.
//
//nolint:gochecknoglobals // an ordered embed manifest has to be package level.
var vendorParts = []vendorPart{
	{"assets/ext-apps-2.0.0.js", "__extApps"},
	{"assets/lit-3.3.3.js", "__lit"},
}

// NOTE: src/lit.js, src/styles.js and src/registry.js must precede what reads them — const is in the TDZ until its line runs.
//
//nolint:gochecknoglobals // an ordered embed manifest has to be package level.
var scriptParts = []string{
	"src/lit.js",
	"src/styles.js",
	"src/format.js",
	"src/color.js",
	"src/registry.js",
	"src/views/blog_list_model.js",
	"src/views/blog_list.js",
	"src/app.js",
	"src/bridge.js",
	"src/boot.js",
}

const litPart = "assets/lit-3.3.3.js"

//nolint:gochecknoglobals // sync.OnceValue memoises the assembled document.
var document = sync.OnceValue(build)

// Document returns the assembled single-file HTML bundle.
func Document() string { return document() }

func build() string {
	// NOTE: text/template, never html/template — the latter escapes inside <script> and <style>
	// and would mangle the embedded JS and CSS.
	tmpl := template.Must(template.New("shell").Parse(read("shell.html")))

	vendors := make([]string, 0, len(vendorParts))
	for _, v := range vendorParts {
		vendors = append(vendors, exposeExports(read(v.path), v.global))
	}

	var scripts strings.Builder
	for _, p := range scriptParts {
		scripts.WriteString(read(p))
		scripts.WriteString("\n")
	}

	var out strings.Builder
	if err := tmpl.Execute(&out, struct {
		Style   string
		Vendors []string
		Scripts string
	}{
		Style:   read("app.css"),
		Vendors: vendors,
		Scripts: scripts.String(),
	}); err != nil {
		panic("ui: assembling the bundle: " + err.Error())
	}
	return out.String()
}

//nolint:gochecknoglobals // compiled once; a package-level regexp is the idiom.
var exportStmt = regexp.MustCompile(`export\{([^}]*)\};?\s*$`)

// NOTE: an inlined module's exports are unreachable, so the trailing export statement is rewritten into globalThis bindings.
func exposeExports(src, global string) string {
	loc := exportStmt.FindStringSubmatchIndex(src)
	if loc == nil {
		panic("ui: vendored bundle has no trailing export statement")
	}
	pairs := make([]string, 0, 64)
	for _, part := range strings.Split(src[loc[2]:loc[3]], ",") {
		local, external, found := strings.Cut(part, " as ")
		if !found {
			external = part
		}
		pairs = append(pairs, strings.TrimSpace(external)+":"+strings.TrimSpace(local))
	}
	return src[:loc[0]] + "\nglobalThis." + global + "={" + strings.Join(pairs, ",") + "};\n"
}

func read(name string) string {
	b, err := files.ReadFile(name)
	if err != nil {
		panic("ui: embedded file missing: " + name)
	}
	return string(b)
}
