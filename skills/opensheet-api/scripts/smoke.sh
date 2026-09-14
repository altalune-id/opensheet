#!/usr/bin/env bash
# Read-only check that an opensheet sheet is reachable and a key works.
# Usage: OPENSHEET_API_KEY=osk_... smoke.sh <base-url> <org> <project> <slug>
set -euo pipefail

if [ $# -ne 4 ]; then
  echo "usage: $0 <base-url> <org> <project> <slug>" >&2
  exit 2
fi
: "${OPENSHEET_API_KEY:?set OPENSHEET_API_KEY to a key with sheets:read}"

base="${1%/}"; org="$2"; project="$3"; slug="$4"
sheet="$base/api/v1/orgs/$org/projects/$project/sheets/$slug"
body=$(mktemp)
trap 'rm -f "$body"' EXIT
fail=0

probe() { # label, url
  local label="$1" url="$2" code
  code=$(curl -sS -o "$body" -w '%{http_code}' "$url" \
    -H "Authorization: Bearer $OPENSHEET_API_KEY") || code=000
  printf '%-14s %s\n' "$label" "$code"
  case "$code" in
    200|304) return 0 ;;
    404) echo "  404 means the slug is unknown OR this key has no grant on it." >&2 ;;
    401|403) echo "  check the key's scopes; 403 on a write also needs a writable sheet." >&2 ;;
    000) echo "  could not reach $base" >&2 ;;
    *) [ -s "$body" ] && sed -n '1,3p' "$body" >&2 ;;
  esac
  fail=1
}

probe rows         "$sheet"
probe filtered     "$sheet?limit=1"
probe capabilities "$sheet/capabilities"

if [ "$fail" -eq 0 ]; then
  echo "OK: $slug is readable"
else
  echo "FAILED: see above" >&2
fi
exit "$fail"
