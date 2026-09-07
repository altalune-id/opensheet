package version

import (
	"strings"
	"testing"
)

func TestString_Default(t *testing.T) {
	if got := String(); got == "" {
		t.Fatal("version.String() must never be empty")
	}
}

func TestString_ReflectsVersionVar(t *testing.T) {
	orig := Version
	t.Cleanup(func() { Version = orig })
	Version = "9.9.9"
	if !strings.Contains(String(), "9.9.9") {
		t.Fatalf("String() should include %q, got %q", Version, String())
	}
}

func TestInfo_HasAllFields(t *testing.T) {
	i := Get()
	if i.Version == "" {
		t.Error("Info.Version empty")
	}
}

// SECURITY: regression guard against function-call initializers on Version — those silently ignore ldflags -X.
func TestDefault_FallsBackToEmbedded(t *testing.T) {
	orig := Version
	t.Cleanup(func() { Version = orig })
	Version = ""
	got := Default()
	if got == "" {
		t.Fatal("Default() must fall back to embedded VERSION when Version unset")
	}
	if strings.Contains(got, "\n") || strings.TrimSpace(got) != got {
		t.Errorf("Default() must be trimmed, got %q", got)
	}
}

func TestDefault_PrefersLdflagsStamp(t *testing.T) {
	orig := Version
	t.Cleanup(func() { Version = orig })
	Version = "v1.2.3-stamped"
	if got := Default(); got != "v1.2.3-stamped" {
		t.Errorf("Default() = %q, want ldflags value", got)
	}
}
