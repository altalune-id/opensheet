package schema

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const storeRoot = "../internal"

var storeFileNames = map[string]bool{ //nolint:gochecknoglobals // Immutable manifest; not runtime state.
	"postgres.go": true,
	"sqlite.go":   true,
	"pgwriter.go": true,
	"pgreader.go": true,
}

const unguardedConflictConsequence = "an ON_CONFLICT ... DO_UPDATE with no tenant predicate is a cross-tenant write: a Save carrying an attacker-supplied row id from another org takes the UPDATE branch and rewrites that org's row. Postgres RLS refuses it, but SQLite has no RLS at all and a BYPASSRLS role bypasses it, so the predicate in the conflict clause is the only protection on those paths. Fix it by qualifying the action — DO_UPDATE(SET(...).WHERE(table.OrgID.EQ(<tenant org>))) — plus a RowsAffected() == 0 branch returning a NotFoundError, not by widening the exemption list"

var upsertGuardExemptions = map[string]string{ //nolint:gochecknoglobals // Immutable manifest; not runtime state.
	"user/pgwriter.go:Save":             "users is global — a user exists before and across every org, so the table has no org_id column",
	"user/sqlite.go:Save":               "users is global — a user exists before and across every org, so the table has no org_id column",
	"platform/session/postgres.go:Save": "sessions carries no org_id by design; a session is resolved before any tenant scope exists (see schema.RequiredTableSuffixes and migrations/postgres/001_init.sql)",
	"platform/session/sqlite.go:Save":   "sessions carries no org_id by design; a session is resolved before any tenant scope exists (see schema.RequiredTableSuffixes and migrations/postgres/001_init.sql)",
	"org/pgwriter.go:Save":              "the orgs table has no org_id column — its own id is the tenant id, and org.Service.Create writes a row for an org that does not exist yet",
	"org/sqlite.go:Save":                "the orgs table has no org_id column — its own id is the tenant id, and org.Service.Create writes a row for an org that does not exist yet",
}

type conflictSite struct {
	key     string
	pos     string
	guarded bool
}

// TestStoreUpserts_GuardConflictClauseByOrg fails when a store's DO_UPDATE action carries no org predicate.
func TestStoreUpserts_GuardConflictClauseByOrg(t *testing.T) {
	sites := collectConflictSites(t)
	if len(sites) == 0 {
		t.Fatalf("no DO_UPDATE call found under %s; the guard would pass vacuously", storeRoot)
	}

	used := map[string]bool{}
	for _, s := range sites {
		if reason, exempt := upsertGuardExemptions[s.key]; exempt {
			used[s.key] = true
			if s.guarded {
				t.Errorf("%s: %s is exempt (%q) but its conflict clause now names OrgID; drop the exemption", s.pos, s.key, reason)
			}
			continue
		}
		if s.guarded {
			continue
		}
		t.Errorf("%s: %s has no OrgID in its conflict clause — %s", s.pos, s.key, unguardedConflictConsequence)
	}

	for key := range upsertGuardExemptions {
		if !used[key] {
			t.Errorf("upsertGuardExemptions lists %q but no DO_UPDATE site there; the code moved or went away, so remove the entry", key)
		}
	}
}

func collectConflictSites(t *testing.T) []conflictSite {
	t.Helper()
	var out []conflictSite
	fset := token.NewFileSet()

	err := filepath.WalkDir(storeRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !storeFileNames[d.Name()] {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		rel := filepath.ToSlash(strings.TrimPrefix(filepath.ToSlash(path), storeRoot+"/"))
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || selectorName(call.Fun) != "DO_UPDATE" {
					return true
				}
				out = append(out, conflictSite{
					key:     rel + ":" + fn.Name.Name,
					pos:     fset.Position(call.Pos()).String(),
					guarded: namesOrgID(call.Args) || namesOrgID(conflictTargetArgs(call)),
				})
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", storeRoot, err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].pos < out[j].pos })
	return out
}

func conflictTargetArgs(doUpdate *ast.CallExpr) []ast.Expr {
	sel, ok := doUpdate.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	for expr := sel.X; ; {
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			return nil
		}
		if selectorName(call.Fun) == "ON_CONFLICT" {
			return call.Args
		}
		inner, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return nil
		}
		expr = inner.X
	}
}

func namesOrgID(exprs []ast.Expr) bool {
	found := false
	for _, expr := range exprs {
		ast.Inspect(expr, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && id.Name == "OrgID" {
				found = true
			}
			return !found
		})
		if found {
			return true
		}
	}
	return false
}

func selectorName(fun ast.Expr) string {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	return sel.Sel.Name
}
