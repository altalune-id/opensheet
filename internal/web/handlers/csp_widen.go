package handlers

import (
	"net/http"
	"slices"
	"strings"
)

type cspSources struct {
	directive string
	sources   []string
}

// https://developers.google.com/workspace/drive/picker/guides/overview
func googlePickerCSP() []cspSources {
	return []cspSources{
		{directive: "script-src", sources: []string{"https://apis.google.com"}},
		{directive: "frame-src", sources: []string{"https://docs.google.com"}},
	}
}

func widenCSP(h http.Header, extra []cspSources) {
	for _, name := range []string{"Content-Security-Policy", "Content-Security-Policy-Report-Only"} {
		if policy := h.Get(name); policy != "" {
			h.Set(name, widenPolicy(policy, extra))
		}
	}
}

func widenPolicy(policy string, extra []cspSources) string {
	var directives []string
	for d := range strings.SplitSeq(policy, ";") {
		if d = strings.TrimSpace(d); d != "" {
			directives = append(directives, d)
		}
	}
	for _, e := range extra {
		i := slices.IndexFunc(directives, func(d string) bool {
			return d == e.directive || strings.HasPrefix(d, e.directive+" ")
		})
		if i < 0 {
			directives = append(directives, e.directive+" 'self' "+strings.Join(e.sources, " "))
			continue
		}
		directives[i] += " " + strings.Join(e.sources, " ")
	}
	return strings.Join(directives, "; ")
}
