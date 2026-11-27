package authz

import (
	"context"
	"slices"

	"altalune.id/opensheet/internal/platform/authn"
)

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

// ScopeHeld reports whether held lets its holder put scope on a key: a permission the scope admits, apikeys:write's console permission, never an unknown scope.
func ScopeHeld(held []Permission, scope string) bool {
	if scope == authn.ScopeAPIKeysWrite {
		return slices.Contains(held, APIKeysWrite)
	}
	for _, d := range catalogue() {
		if d.Scope != scope {
			continue
		}
		if slices.Contains(held, d.Permission) {
			return true
		}
	}
	return false
}

// CanPutScope refuses with *DeniedError unless the principal bound on ctx may put scope on a key it mints.
func CanPutScope(ctx context.Context, scope string) error {
	if sys, _ := ctx.Value(systemKey{}).(bool); sys {
		return nil
	}
	pr, bound := boundPrincipal(ctx)
	var held []Permission
	if bound {
		held = toPermissions(pr.Held)
	}
	if ScopeHeld(held, scope) {
		return nil
	}
	p, ok := PermissionFor(scope)
	if !ok {
		p = APIKeysWrite
	}
	return &DeniedError{Permission: p, Org: pr.HeldOrg, Side: sideOf(pr, p), Scopes: slices.Clone(pr.Scopes)}
}
