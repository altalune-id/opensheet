package authz

import "slices"

func admits(scopes []string, scope string) bool {
	return scope != "" && slices.Contains(scopes, scope)
}

// PermissionFor returns the base permission scope admits, never an extra one sharing that scope.
func PermissionFor(scope string) (Permission, bool) {
	for _, d := range catalogue() {
		if d.Scope == scope && scope != "" {
			return d.Permission, true
		}
	}
	return "", false
}
