package authn

import "slices"

// NOTE: these strings are the wire contract with authl's Resource Server catalog.
const (
	ScopeSheetsRead        = "sheets:read"
	ScopeSheetsWrite       = "sheets:write"
	ScopeSheetsAdmin       = "sheets:admin"
	ScopeSpreadsheetsRead  = "spreadsheets:read"
	ScopeSpreadsheetsWrite = "spreadsheets:write"
	ScopeCredentialsRead   = "credentials:read"
	ScopeCredentialsWrite  = "credentials:write"
	ScopeAPIKeysRead       = "apikeys:read"
	ScopeAPIKeysWrite      = "apikeys:write"
	ScopeCachePurge        = "cache:purge"
)

//nolint:gochecknoglobals // immutable catalog, returned by copy from AllScopes.
var allScopes = []string{
	ScopeSheetsRead, ScopeSheetsWrite, ScopeSheetsAdmin,
	ScopeSpreadsheetsRead, ScopeSpreadsheetsWrite,
	ScopeCredentialsRead, ScopeCredentialsWrite,
	ScopeAPIKeysRead, ScopeAPIKeysWrite,
	ScopeCachePurge,
}

// AllScopes returns every scope in the catalog.
func AllScopes() []string { return slices.Clone(allScopes) }

// Valid reports whether scope is a catalog member.
func Valid(scope string) bool { return slices.Contains(allScopes, scope) }
