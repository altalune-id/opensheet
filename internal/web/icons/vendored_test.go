package icons

import (
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// NOTE: Icon renders an invisible <span data-missing-icon> for an unknown name, so nothing else catches a missing icon.
var (
	reTemplIcon   = regexp.MustCompile(`icons\.Icon\(\s*"([a-z0-9-]+)"`)
	reIconFunc    = regexp.MustCompile(`(?s)func \w*[Ii]con\w*\([^)]*\) string \{(.*?)\n\}`)
	reIconReturn  = regexp.MustCompile(`return "([a-z0-9-]+)"`)
	reAllowedList = regexp.MustCompile(`(?s)var AllowedIcons = \[\]string\{(.*?)\}`)
	reQuoted      = regexp.MustCompile(`"([a-z0-9-]+)"`)
)

func repoRoot(t *testing.T) string {
	t.Helper()

	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return root
}

func globAll(t *testing.T, root string, patterns ...string) []string {
	t.Helper()

	var out []string
	for _, p := range patterns {
		matches, err := filepath.Glob(filepath.Join(root, p))
		if err != nil {
			t.Fatalf("glob %s: %v", p, err)
		}
		out = append(out, matches...)
	}
	return out
}

func referencedIcons(t *testing.T) map[string]string {
	t.Helper()

	root := repoRoot(t)
	out := map[string]string{}

	for _, f := range globAll(t, root, "internal/web/templates/*.templ") {
		b, err := os.ReadFile(f) //nolint:gosec // G304: path comes from a repo-local glob
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, m := range reTemplIcon.FindAllStringSubmatch(string(b), -1) {
			out[m[1]] = filepath.Base(f)
		}
	}
	if len(out) == 0 {
		t.Fatal("found no icons.Icon literal in any template — the scraper pattern has gone stale")
	}

	// NOTE: handlers and template helpers pick icon names in Go, so template literals alone miss them.
	goSources := globAll(t, root,
		"internal/web/templates/*.templ",
		"internal/web/handlers/*.go",
		"internal/web/*.go",
	)
	for _, f := range goSources {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f) //nolint:gosec // G304: path comes from a repo-local glob
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, fn := range reIconFunc.FindAllStringSubmatch(string(b), -1) {
			for _, m := range reIconReturn.FindAllStringSubmatch(fn[1], -1) {
				out[m[1]] = filepath.Base(f)
			}
		}
	}

	// NOTE: a fork declares its user-pickable set as `var AllowedIcons = []string{…}`; every name must be vendored.
	for _, f := range allowedIconLists(t, root) {
		b, err := os.ReadFile(f) //nolint:gosec // G304: path comes from a repo-local walk
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		block := reAllowedList.FindSubmatch(b)
		if block == nil {
			continue
		}
		for _, m := range reQuoted.FindAllStringSubmatch(string(block[1]), -1) {
			out[m[1]] = filepath.Base(f) + ":AllowedIcons"
		}
	}

	return out
}

func allowedIconLists(t *testing.T, root string) []string {
	t.Helper()

	var out []string
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path) //nolint:gosec // G304: path comes from a repo-local walk
		if err != nil {
			return err
		}
		if reAllowedList.Match(b) {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/: %v", err)
	}
	return out
}

func TestEveryReferencedIconIsVendored(t *testing.T) {
	t.Parallel()

	refs := referencedIcons(t)
	names := make([]string, 0, len(refs))
	for n := range refs {
		names = append(names, n)
	}
	slices.Sort(names)

	for _, n := range names {
		t.Run(n, func(t *testing.T) {
			if !Has(n) {
				t.Fatalf("icon %q is referenced by %s but is not vendored — run: make icons-add NAME=%s", n, refs[n], n)
			}
			if inner, ok := InnerFor(n); !ok || inner == "" {
				t.Fatalf("icon %q has an empty SVG body", n)
			}
		})
	}
}

// NOTE: an allowlist, not a denylist — bodies are injected with templ.Raw, so anything unrecognized is refused.
//
//nolint:gochecknoglobals // Immutable manifest; not runtime state.
var (
	allowedSVGElements = []string{"circle", "ellipse", "g", "line", "path", "polygon", "polyline", "rect"}
	allowedSVGAttrs    = []string{
		"cx", "cy", "d", "fill", "height", "opacity", "points", "r", "rx", "ry",
		"stroke", "stroke-dasharray", "stroke-linecap", "stroke-linejoin", "stroke-width",
		"transform", "width", "x", "x1", "x2", "y", "y1", "y2",
	}
)

// SECURITY: bodies are injected with templ.Raw, so a script element or handler attribute would execute.
func TestVendoredIconsCarryOnlyAllowedMarkup(t *testing.T) {
	t.Parallel()

	for _, n := range Names() {
		t.Run(n, func(t *testing.T) {
			inner, ok := InnerFor(n)
			if !ok {
				t.Fatalf("icon %q vanished between Names and InnerFor", n)
			}
			dec := xml.NewDecoder(strings.NewReader("<root>" + inner + "</root>"))
			for {
				tok, err := dec.Token()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatalf("icon %q is not well-formed XML: %v", n, err)
				}
				el, isStart := tok.(xml.StartElement)
				if !isStart || el.Name.Local == "root" {
					continue
				}
				if !slices.Contains(allowedSVGElements, el.Name.Local) {
					t.Fatalf("icon %q uses element <%s>, which is not on the allowlist", n, el.Name.Local)
				}
				for _, a := range el.Attr {
					if !slices.Contains(allowedSVGAttrs, a.Name.Local) {
						t.Fatalf("icon %q sets attribute %q on <%s>, which is not on the allowlist", n, a.Name.Local, el.Name.Local)
					}
					if strings.Contains(strings.ToLower(a.Value), "javascript:") {
						t.Fatalf("icon %q sets %s to a javascript: URL", n, a.Name.Local)
					}
				}
			}
		})
	}
}
