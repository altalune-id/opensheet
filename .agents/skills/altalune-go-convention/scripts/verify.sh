#!/usr/bin/env bash
# Run the module gates in dependency order, explaining each failure.
#
#   scripts/verify.sh                 unit gates; regenerates and gofmt -w's files
#   scripts/verify.sh --check         read-only: never writes a file (use during review)
#   scripts/verify.sh --integration   also run the Postgres suite (combines with --check)
#
# TEST_PG_DSN should point at a throwaway database. Without it every pgtest.New
# starts its own container and the suite takes ~25 minutes instead of ~2.
set -uo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
cd "$root" || exit 1
fail=0
readonly_mode=0
integration=0
for a in "$@"; do
  case "$a" in
    --check) readonly_mode=1 ;;
    --integration) integration=1 ;;
    *) printf 'unknown flag: %s\n' "$a" >&2; exit 2 ;;
  esac
done

step() { printf '\n=== %s\n' "$1"; }
why()  { printf '    ↳ %s\n' "$1"; fail=1; }

if [ "$readonly_mode" -eq 1 ]; then
  step "generated code is current (read-only)"
  if ! make config-examples-check >/dev/null 2>&1; then
    why ".env.example / config.example.yaml are stale — make config-examples"
  fi
  if ! make templ-normalize-check >/dev/null 2>&1; then
    why "generated templ FileName paths drifted — make generate && make templ-normalize"
  fi
  printf '    not checked read-only: make generate, make tenant-tables — regenerate and diff\n'

  step "gofmt + vet + race tests (read-only)"
  unformatted="$(git ls-files -co --exclude-standard '*.go' | xargs gofmt -l 2>/dev/null)"
  if [ -n "$unformatted" ]; then
    printf '%s\n' "$unformatted" | head -10
    why "unformatted Go files — make fmt"
  fi
  if ! { make vet && make test-race; } >/tmp/altalune-go-convention-check.log 2>&1; then
    tail -20 /tmp/altalune-go-convention-check.log
    why "a failing unit test or vet. Full log: /tmp/altalune-go-convention-check.log"
  fi
else
  step "generated code is current"
  make generate >/dev/null 2>&1
  make tenant-tables >/dev/null 2>&1
  make config-examples >/dev/null 2>&1
  if ! git diff --quiet -- schema/tenant_tables_gen.go .env.example config.example.yaml 2>/dev/null; then
    printf '    regenerated files changed — stage them\n'
    git diff --stat -- schema/tenant_tables_gen.go .env.example config.example.yaml
  fi

  step "make check (fmt + vet + unit tests)"
  if ! make check >/tmp/altalune-go-convention-check.log 2>&1; then
    tail -20 /tmp/altalune-go-convention-check.log
    why "a failing unit test, or gofmt/vet. Full log: /tmp/altalune-go-convention-check.log"
  fi
fi

step "make lint"
if ! make lint >/tmp/altalune-go-convention-lint.log 2>&1; then
  grep -E "^(internal|schema|cmd)/" /tmp/altalune-go-convention-lint.log | head -20
  why "depguard here usually means an aggregate file imported something outside the allow list"
fi

step "error codes documented both ways"
if ! go test ./internal/apperror/ >/dev/null 2>&1; then
  why "a Code* constant with no ../../../../docs/errors/README.md row, or a row with no constant"
fi

step "tenant-scoped upserts guarded"
if ! go test ./schema/ -run TestStoreUpserts >/tmp/altalune-go-convention-upsert.log 2>&1; then
  grep -o "no OrgID in its conflict clause" /tmp/altalune-go-convention-upsert.log | head -1
  grep -oE "\.\./internal/[a-z/]+\.go:[0-9]+:[0-9]+" /tmp/altalune-go-convention-upsert.log | head -5
  why "an ON_CONFLICT ... DO_UPDATE without a tenant predicate is a cross-tenant write"
fi

step "i18n keys present in every locale"
if ! go tool i18n-lint -check >/tmp/altalune-go-convention-i18n.log 2>&1; then
  tail -10 /tmp/altalune-go-convention-i18n.log
  why "add the key to all five locales by hand — do NOT run i18n-lint --fix, it rewrites them"
fi

step "routes registered are routes probed"
if ! go test ./internal/boot/ -run TestRoutes_ListMatchesTheMux >/dev/null 2>&1; then
  why "add the new route to probeRoutes() in internal/boot/route_scope_test.go"
fi

step "every machine-surface procedure has a scope"
if ! go test ./internal/controlplane/ -run TestEveryRPCHasAScope >/dev/null 2>&1; then
  why "add the procedure to internal/controlplane/scopes.go — absent means denied, not admitted"
fi
if ! go test ./internal/boot/ -run 'TestEveryMCPToolHasAScope|TestEveryAnnotatedProtoToolIsRegistered' >/dev/null 2>&1; then
  why "an (mcp.v1.tool) needs a scope in internal/mcp/scopes.go and rows in internal/boot/mcp_tools.go"
fi

step "comment discipline"
if ! make comment-check >/tmp/altalune-go-convention-comments.log 2>&1; then
  tail -10 /tmp/altalune-go-convention-comments.log
  why "rationale prose in a comment — keep godoc one-liners and SECURITY/NOTE/TODO markers only"
fi

if [ "$integration" -eq 1 ]; then
  step "integration suite"
  if [ -z "${TEST_PG_DSN:-}" ]; then
    printf '    TEST_PG_DSN unset — this will start a container per pgtest.New and take ~25 min\n'
  fi
  if ! make test-integration >/tmp/altalune-go-convention-integration.log 2>&1; then
    grep -E "^(FAIL|--- FAIL)" /tmp/altalune-go-convention-integration.log | head -10
    why "full log: /tmp/altalune-go-convention-integration.log"
  fi
fi

printf '\n'
if [ "$fail" -eq 0 ]; then
  printf 'all gates passed.\n\nStill required, and not checkable here:\n'
  printf '  - open the page; type checking passing is not evidence a surface works\n'
  printf '  - bash scripts/verify-serve-smoke.sh, and verify-mcp-smoke.sh if you touched S7\n'
  printf '  - for each security guard, revert it and confirm a test fails\n'
  exit 0
fi
printf 'gates failed — see above.\n'
exit 1
