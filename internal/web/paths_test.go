package web_test

import (
	"testing"

	"altalune.id/template/internal/web"
)

func TestPath(t *testing.T) {
	cases := []struct {
		basePath, sub, want string
	}{
		{"", "", "/"},
		{"", "/", "/"},
		{"", "/login", "/login"},
		{"", "login", "/login"},
		{"/", "", "/"},
		{"/", "/login", "/login"},
		{"/app", "", "/app"},
		{"/app", "/", "/app/"},
		{"/app/", "/", "/app/"},
		{"/app", "/login", "/app/login"},
		{"/app/", "/login", "/app/login"},
		{"/app/", "login", "/app/login"},
		{"/app", "static/htmx.min.js", "/app/static/htmx.min.js"},
	}
	for _, c := range cases {
		got := web.Path(c.basePath, c.sub)
		if got != c.want {
			t.Errorf("Path(%q,%q)=%q, want %q", c.basePath, c.sub, got, c.want)
		}
	}
}
