// Package authz decides what a principal may do in the org its request is bound to.
package authz

import (
	"context"
	"slices"
	"sync"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
)

// Permission names one thing a principal may do in an org.
type Permission string

// Holder is the least credential that may hold a permission.
type Holder string

// The holders. SECURITY: a key or token holds only HolderAny; only a web session holds HolderConsole.
const (
	HolderAny     Holder = "any"
	HolderConsole Holder = "console"
)

// Def describes one permission: the locale key of its action phrase, the token scope that admits it and its least holder.
type Def struct {
	Permission Permission
	LabelKey   string
	Scope      string
	Holder     Holder
}

//nolint:gochecknoglobals // the permission catalogue, built once and read-only.
var catalogue = sync.OnceValue(func() []Def { return append(appDefs(), coreDefs()...) })

// Catalogue returns every permission, in display order.
func Catalogue() []Def {
	return slices.Clone(catalogue())
}

// Lookup returns the definition of p.
func Lookup(p Permission) (Def, bool) {
	defs := catalogue()
	i := slices.IndexFunc(defs, func(d Def) bool { return d.Permission == p })
	if i < 0 {
		return Def{}, false
	}
	return defs[i], true
}

// Builtin returns the permissions of a built-in role: owner, admin or member.
func Builtin(role string) []Permission {
	switch role {
	case "owner":
		return all()
	case "admin":
		return without(all(), RolesWrite)
	case "member":
		return memberPermissions()
	}
	return nil
}

// Covers reports whether held includes every permission in want.
func Covers(held []Permission, want ...Permission) bool {
	for _, w := range want {
		if !slices.Contains(held, w) {
			return false
		}
	}
	return true
}

type systemKey struct{}

// AsSystem marks ctx as the platform acting on its own behalf, holding every permission. SECURITY: only boot's job and scheduler wrappers may call it.
func AsSystem(ctx context.Context) context.Context {
	return context.WithValue(ctx, systemKey{}, true)
}

// Can refuses with *DeniedError unless the principal bound on ctx holds p in the tenant org on ctx. SECURITY: a held set answers only for the org it was resolved in.
func Can(ctx context.Context, p Permission) error {
	if sys, _ := ctx.Value(systemKey{}).(bool); sys {
		return nil
	}
	pr, bound := boundPrincipal(ctx)
	if bound && slices.Contains(pr.Held, string(p)) {
		return nil
	}
	return &DeniedError{Permission: p, Org: pr.HeldOrg, Side: sideOf(pr, p), Scopes: slices.Clone(pr.Scopes)}
}

func boundPrincipal(ctx context.Context) (session.Principal, bool) {
	pr := session.PrincipalFrom(ctx)
	tc, err := tenant.From(ctx)
	return pr, err == nil && tc.OrgID == pr.HeldOrgID && pr.HeldOrgID != uuid.Nil
}

// CanOrgWide is Can plus a grant that covers every project of the org.
func CanOrgWide(ctx context.Context, p Permission) error {
	return Can(ctx, p)
}

// Held returns the permissions the principal bound on ctx holds.
func Held(ctx context.Context) []Permission {
	if sys, _ := ctx.Value(systemKey{}).(bool); sys {
		return all()
	}
	return toPermissions(session.PrincipalFrom(ctx).Held)
}

func sideOf(p session.Principal, perm Permission) Side {
	if !machine(p) {
		return SideRole
	}
	d, ok := Lookup(perm)
	if !ok || d.Holder == HolderConsole || !admits(p.Scopes, d.Scope) {
		return SideScope
	}
	return SideRole
}

func machine(p session.Principal) bool {
	return p.Source == session.SourceToken || p.Source == session.SourceAPIKey
}

func all() []Permission {
	out := make([]Permission, 0, len(catalogue()))
	for _, d := range catalogue() {
		out = append(out, d.Permission)
	}
	return out
}

func without(held []Permission, drop ...Permission) []Permission {
	return slices.DeleteFunc(held, func(p Permission) bool { return slices.Contains(drop, p) })
}

func toPermissions(held []string) []Permission {
	out := make([]Permission, 0, len(held))
	for _, h := range held {
		out = append(out, Permission(h))
	}
	return out
}
