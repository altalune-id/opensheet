// Package icons renders vendored Lucide SVG icons as templ components.
package icons

import (
	"embed"
	"regexp"
	"strings"
	"sync"
)

//go:embed svg/*.svg
var svgFS embed.FS

var (
	innerRe = regexp.MustCompile(`(?s)<svg[^>]*>(.*)</svg>`)
	inners  = sync.OnceValue(loadInners) //nolint:gochecknoglobals // sync.OnceValue memoized cache is package-scoped by design.
)

func loadInners() map[string]string {
	m := map[string]string{}
	entries, err := svgFS.ReadDir("svg")
	if err != nil {
		return m
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".svg") {
			continue
		}
		b, err := svgFS.ReadFile("svg/" + name)
		if err != nil {
			continue
		}
		match := innerRe.FindSubmatch(b)
		if len(match) < 2 {
			continue
		}
		m[strings.TrimSuffix(name, ".svg")] = strings.TrimSpace(string(match[1]))
	}
	return m
}

// Has reports whether an icon with the given name is vendored.
func Has(name string) bool {
	_, ok := inners()[name]
	return ok
}

// Names returns every vendored icon name, sorted.
func Names() []string {
	m := inners()
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// InnerFor returns the SVG inner body for name, and whether it was found.
func InnerFor(name string) (string, bool) {
	v, ok := inners()[name]
	return v, ok
}
