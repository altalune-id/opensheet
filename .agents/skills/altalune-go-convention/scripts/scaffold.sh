#!/usr/bin/env bash
# Scaffold a new domain module from internal/todo/.
#
#   scripts/scaffold.sh <name> [Type]
#     name  lowercase package name, e.g. "article"
#     Type  exported aggregate type, defaults to the capitalised name
#
# Produces a compiling-but-wrong module: the todo invariants, columns and error
# codes come along and must be replaced. It saves the boilerplate, not the thinking.
set -euo pipefail

name="${1:-}"
[ -n "$name" ] || { echo "usage: scaffold.sh <name> [Type]" >&2; exit 2; }
[[ "$name" =~ ^[a-z][a-z0-9]*$ ]] || { echo "name must be lowercase alphanumeric: $name" >&2; exit 2; }

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
[ -d "$root/internal/todo" ] || { echo "run from inside the repo; internal/todo not found at $root" >&2; exit 1; }

src="$root/internal/todo"
dst="$root/internal/$name"
[ -e "$dst" ] || true
[ ! -e "$dst" ] || { echo "$dst already exists" >&2; exit 1; }

cap() { printf '%s%s' "$(printf '%s' "${1:0:1}" | tr '[:lower:]' '[:upper:]')" "${1:1}"; }
Type="${2:-$(cap "$name")}"

cp -r "$src" "$dst"
# Drop todo-specific extras; a new module starts without periodic work or a hijack test.
rm -f "$dst"/scheduler.go "$dst"/scheduler_test.go "$dst"/service_extra_test.go "$dst"/sqlite_hijack_test.go

for f in "$dst"/*.go; do
  sed -i '' \
    -e "s/\btodo\b/$name/g" \
    -e "s/\bTodo\b/$Type/g" \
    -e "s/\btodos\b/${name}s/g" \
    -e "s/\bTodos\b/${Type}s/g" \
    "$f"
done

mv "$dst/todo.go" "$dst/$name.go"
mv "$dst/todo_test.go" "$dst/${name}_test.go"

command -v gofmt >/dev/null && gofmt -w "$dst"

cat <<EOF
scaffolded internal/$name/ (aggregate $Type)

It will not compile until you do the rest, in this order:
  1. migrations + RLS + jet bindings   -> references/schema.md
  2. error codes + ../../../../docs/errors/README.md -> references/errors.md
  3. replace the copied invariants, columns and Store methods
  4. fake, boot wiring, depguard glob  -> references/wiring.md
  5. surfaces                          -> references/surfaces.md

Then: scripts/verify.sh
EOF
