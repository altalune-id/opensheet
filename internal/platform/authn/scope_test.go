package authn

import (
	"slices"
	"testing"
)

func TestValid_AndAllScopes(t *testing.T) {
	if !Valid(ScopeSheetsRead) {
		t.Errorf("Valid(%q) = false", ScopeSheetsRead)
	}
	if Valid("sheets:destroy") {
		t.Error("Valid accepted an unknown scope")
	}
	all := AllScopes()
	if len(all) != 10 {
		t.Fatalf("AllScopes() = %d scopes, want 10", len(all))
	}
	seen := map[string]bool{}
	for _, s := range all {
		if seen[s] {
			t.Errorf("AllScopes() contains %q twice", s)
		}
		seen[s] = true
		if !Valid(s) {
			t.Errorf("AllScopes() contains %q but Valid says no", s)
		}
	}
}

func TestAllScopes_ReturnsCopy(t *testing.T) {
	first := AllScopes()
	first[0] = "mutated"
	second := AllScopes()
	if slices.Contains(second, "mutated") {
		t.Fatal("AllScopes() returned the backing slice; a caller mutated the catalog")
	}
	if second[0] != ScopeSheetsRead {
		t.Fatalf("AllScopes()[0] = %q, want %q", second[0], ScopeSheetsRead)
	}
}
