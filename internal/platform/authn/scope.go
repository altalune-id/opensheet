package authn

import "slices"

// The scope catalog. NOTE: these strings are a wire contract with every minted key and with authl's Resource Server catalog.
const (
	ScopeSheetsRead        = "sheets:read"
	ScopeSheetsWrite       = "sheets:write"
	ScopeSheetsAdmin       = "sheets:admin"
	ScopeSpreadsheetsRead  = "spreadsheets:read"
	ScopeSpreadsheetsWrite = "spreadsheets:write"
	ScopeCredentialsRead   = "credentials:read"
	ScopeCredentialsWrite  = "credentials:write"
	ScopeCachePurge        = "cache:purge"
	ScopeAPIKeysRead       = "apikeys:read"
	ScopeAPIKeysWrite      = "apikeys:write"
	ScopeMembersRead       = "members:read"
)

// ScopeLevel names where a scope's authority lives: inside one project, or across the org itself.
type ScopeLevel string

// The scope levels. An org-level scope acts on the org (its projects, its members), never on one project's data.
const (
	LevelProject ScopeLevel = "project"
	LevelOrg     ScopeLevel = "org"
)

type scopeSpec struct {
	name    string
	level   ScopeLevel
	retired bool
}

// NOTE: apikeys:write is retired because only a person may manage keys; keys holding it keep validating.
var catalog = []scopeSpec{ //nolint:gochecknoglobals // immutable catalog, read only through the functions below.
	{name: ScopeSheetsRead, level: LevelProject},
	{name: ScopeSheetsWrite, level: LevelProject},
	{name: ScopeSheetsAdmin, level: LevelProject},
	{name: ScopeSpreadsheetsRead, level: LevelProject},
	{name: ScopeSpreadsheetsWrite, level: LevelProject},
	{name: ScopeCredentialsRead, level: LevelProject},
	{name: ScopeCredentialsWrite, level: LevelProject},
	{name: ScopeCachePurge, level: LevelProject},
	{name: ScopeAPIKeysRead, level: LevelProject},
	{name: ScopeAPIKeysWrite, level: LevelProject, retired: true},
	{name: ScopeMembersRead, level: LevelOrg},
}

// AllScopes returns every scope in the catalog, retired ones included.
func AllScopes() []string {
	out := make([]string, 0, len(catalog))
	for _, s := range catalog {
		out = append(out, s.name)
	}
	return out
}

// MintableScopes returns the scopes a new key may be granted, in catalog order.
func MintableScopes() []string {
	out := make([]string, 0, len(catalog))
	for _, s := range catalog {
		if !s.retired {
			out = append(out, s.name)
		}
	}
	return out
}

// Valid reports whether scope is in the catalog.
func Valid(scope string) bool {
	return slices.ContainsFunc(catalog, func(s scopeSpec) bool { return s.name == scope })
}

// Mintable reports whether a new key may be granted scope.
func Mintable(scope string) bool { return slices.Contains(MintableScopes(), scope) }

// LevelOf returns scope's level, or false for a scope outside the catalog.
func LevelOf(scope string) (ScopeLevel, bool) {
	i := slices.IndexFunc(catalog, func(s scopeSpec) bool { return s.name == scope })
	if i < 0 {
		return "", false
	}
	return catalog[i].level, true
}
