#!/usr/bin/env bash
# check-module-shape.sh — verify each internal domain module carries its canonical file set per docs/MODULE_TEMPLATE.md.
#
# Canonical set for a store-backed module <name> under internal/<name>/:
#   <name>.go     aggregate types + package doc
#   store.go      Store interface + fake
#   service.go    business logic (NewService constructor)
#   errors.go     typed domain errors
#   factory.go    NewStoreFactory dispatch
#   postgres.go   Postgres implementation
#   sqlite.go     SQLite implementation
#
# `auth` is stateless (delegates to `user` store) so it has no store/factory/
# driver files -- exempted below.
set -uo pipefail

cd "$(dirname -- "$0")/.."

fail=0
declare -a missing_notes

# List of modules that follow the full store-backed shape.
STORE_BACKED=(todo user org project invite)

# Modules exempt from store-backed conventions.
STATELESS=(auth)

check_file() {
    local module="$1"; local file="$2"
    if [ ! -f "internal/${module}/${file}" ]; then
        missing_notes+=("internal/${module}/${file} missing")
        return 1
    fi
    return 0
}

check_store_backed() {
    local mod="$1"
    local local_fail=0
    for f in "${mod}.go" store.go service.go errors.go factory.go postgres.go sqlite.go; do
        check_file "$mod" "$f" || local_fail=1
    done
    # Test files -- at minimum a service_test.go and a driver test.
    for f in service_test.go; do
        check_file "$mod" "$f" || local_fail=1
    done
    # One of sqlite_test.go or postgres_integration_test.go is required.
    if [ ! -f "internal/${mod}/sqlite_test.go" ] && [ ! -f "internal/${mod}/postgres_integration_test.go" ]; then
        missing_notes+=("internal/${mod}/{sqlite_test.go,postgres_integration_test.go} both missing")
        local_fail=1
    fi
    return "$local_fail"
}

check_stateless() {
    local mod="$1"
    local local_fail=0
    for f in "${mod}.go" service.go errors.go; do
        check_file "$mod" "$f" || local_fail=1
    done
    # And at least one test file.
    if ! ls "internal/${mod}"/*_test.go >/dev/null 2>&1; then
        missing_notes+=("internal/${mod}/*_test.go missing")
        local_fail=1
    fi
    return "$local_fail"
}

for mod in "${STORE_BACKED[@]}"; do
    if ! check_store_backed "$mod"; then
        fail=1
    fi
done

for mod in "${STATELESS[@]}"; do
    if ! check_stateless "$mod"; then
        fail=1
    fi
done

if [ "$fail" -ne 0 ]; then
    echo "module shape violations:" >&2
    for n in "${missing_notes[@]}"; do
        echo "  - $n" >&2
    done
    exit 1
fi

echo "OK: every module under internal/{todo,user,org,project,invite,auth}/ has its canonical file set"
exit 0
