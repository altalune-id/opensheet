package web_test

import (
	"testing"

	"altalune.id/opensheet/internal/web"
)

func TestOrigin_DropsTheDefaultPort(t *testing.T) {
	for raw, want := range map[string]string{
		"https://id.example/p/acme":     "https://id.example",
		"https://ID.example:443/logout": "https://id.example",
		"http://id.example:80/":         "http://id.example",
		"https://id.example:8443/x":     "https://id.example:8443",
		"http://127.0.0.1:5150/p/acme":  "http://127.0.0.1:5150",
		"http://[::1]:80/":              "http://[::1]",
		"https://[::1]:8443/x":          "https://[::1]:8443",
		"::bad":                         "",
		"/relative":                     "",
	} {
		if got := web.Origin(raw); got != want {
			t.Errorf("Origin(%q) = %q, want %q", raw, got, want)
		}
	}
}
