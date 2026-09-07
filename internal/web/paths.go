// Package web is the SSR HTTP surface: templ-rendered pages, HTMX handlers, and the http.Server assembly.
package web

import "strings"

// Path joins basePath and sub with a single leading `/` and no trailing `/`.
func Path(basePath, sub string) string {
	bp := strings.TrimRight(basePath, "/")
	s := sub
	if s == "" {
		if bp == "" {
			return "/"
		}
		return bp
	}
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	return bp + s
}
