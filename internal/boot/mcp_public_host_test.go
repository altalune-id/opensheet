package boot

import (
	"slices"
	"testing"

	"altalune.id/opensheet/internal/platform/config"
)

func TestMCPPublicHost(t *testing.T) {
	for base, want := range map[string][]string{
		"https://app.example.com":      {"app.example.com"},
		"https://app.example.com:8443": {"app.example.com"},
		"":                             nil,
	} {
		if got := mcpPublicHost(config.HTTPConfig{BaseURL: base}); !slices.Equal(got, want) {
			t.Errorf("mcpPublicHost(%q) = %v, want %v", base, got, want)
		}
	}
}
