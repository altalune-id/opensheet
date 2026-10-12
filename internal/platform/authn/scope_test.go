package authn_test

import (
	"errors"
	"slices"
	"testing"

	"altalune.id/opensheet/internal/platform/authn"
)

func TestValidRejectsUnknownScope(t *testing.T) {
	if authn.Valid("posts:destroy") {
		t.Fatal("Valid accepted a scope outside the catalog")
	}
	if !authn.Valid(authn.ScopeSheetsRead) {
		t.Fatalf("Valid rejected %q", authn.ScopeSheetsRead)
	}
}

func TestAllScopesReturnsACopy(t *testing.T) {
	first := authn.AllScopes()
	first[0] = "mutated"
	if slices.Contains(authn.AllScopes(), "mutated") {
		t.Fatal("AllScopes leaked its backing array")
	}
}

func TestErrorHelpers(t *testing.T) {
	var unauthorized error = &authn.UnauthorizedError{}
	if !authn.IsUnauthorizedError(unauthorized) {
		t.Fatal("IsUnauthorizedError missed its own type")
	}
	scoped := &authn.InsufficientScopeError{Scope: authn.ScopeSheetsWrite}
	if !authn.IsInsufficientScopeError(scoped) {
		t.Fatal("IsInsufficientScopeError missed its own type")
	}
	if authn.IsUnauthorizedError(scoped) {
		t.Fatal("an authz failure must not read as an authn failure")
	}
	if !errors.Is(errors.Join(nil, scoped), scoped) {
		t.Fatal("InsufficientScopeError does not survive errors.Join")
	}
}

// SECURITY: a retired scope keeps validating on existing keys but is never offered to a new one.
func TestRetiredScopeValidatesButIsNotMintable(t *testing.T) {
	if !authn.Valid(authn.ScopeAPIKeysWrite) {
		t.Fatal("a retired scope must keep validating, or keys in the field stop parsing")
	}
	if authn.Mintable(authn.ScopeAPIKeysWrite) {
		t.Fatal("apikeys:write is retired and must not be mintable")
	}
	if slices.Contains(authn.MintableScopes(), authn.ScopeAPIKeysWrite) {
		t.Fatal("MintableScopes offered a retired scope")
	}
	if !authn.Mintable(authn.ScopeSheetsRead) {
		t.Fatalf("Mintable rejected %q", authn.ScopeSheetsRead)
	}
}

func TestEveryScopeHasALevel(t *testing.T) {
	for _, s := range authn.AllScopes() {
		level, ok := authn.LevelOf(s)
		if !ok || (level != authn.LevelProject && level != authn.LevelOrg) {
			t.Errorf("%q has no valid level: %q", s, level)
		}
	}
	if _, ok := authn.LevelOf("posts:destroy"); ok {
		t.Fatal("LevelOf resolved a scope outside the catalog")
	}
	if level, _ := authn.LevelOf(authn.ScopeMembersRead); level != authn.LevelOrg {
		t.Fatalf("members:read is %q, want org: it acts on the org itself", level)
	}
	if level, _ := authn.LevelOf(authn.ScopeSheetsRead); level != authn.LevelProject {
		t.Fatalf("posts:read is %q, want project", level)
	}
}
