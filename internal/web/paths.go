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

// DataPath returns the path the data plane serves a published sheet's rows from.
// NOTE: internal/data owns this route shape and is mounted at <basePath>/api/v1/.
func DataPath(basePath, orgSlug, projectSlug, sheetSlug string) string {
	return Path(basePath, "/api/v1/orgs/"+orgSlug+"/projects/"+projectSlug+"/sheets/"+sheetSlug)
}
