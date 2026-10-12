#!/usr/bin/env bash
# mcp-ui-vendor.sh — download the pinned MCP Apps assets and bundle the pinned Lit core.
# These are COMMITTED: internal/mcp/ui embeds them by NAME, so a fresh clone cannot
# `go build` without them. Do NOT gitignore them by analogy with scripts/ui-vendor.sh.
#
# Pinned versions (bump here, re-run, update the //go:embed names and the sums):
#   @modelcontextprotocol/ext-apps  2.0.0
#   lit                             3.3.3   (bundled with esbuild 0.25.10; both pin the digest)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ASSETS="$ROOT/internal/mcp/ui/assets"
SCHEMA_DIR="$ROOT/mcp/testdata"
mkdir -p "$ASSETS" "$SCHEMA_DIR"

EXT_APPS_VERSION="${EXT_APPS_VERSION:-2.0.0}"
LIT_VERSION="${LIT_VERSION:-3.3.3}"
ESBUILD_VERSION="${ESBUILD_VERSION:-0.25.10}"
BASE="https://cdn.jsdelivr.net/npm/@modelcontextprotocol/ext-apps@${EXT_APPS_VERSION}/dist/src"

EXT_APPS_SHA256="fb56376b7583ecafb4820bdebc150abee18feb6258ff84b83c2c944ebd9c3602"
SCHEMA_SHA256="e9af2289956641a266d99273facb746c2023bae48567d64bb83c63aadecb856f"
LIT_SHA256="04ad9e0306ed703c52c779d38830a14913cc959f489153837c964111794490a9"

fetch() {
	local url="$1" out="$2"
	echo "  → $url"
	curl -sSfL "$url" -o "$out"
}

verify() {
	local file="$1" want="$2" got
	got="$(shasum -a 256 "$file" | cut -d' ' -f1)"
	if [ "$got" != "$want" ]; then
		echo "FATAL: $file sha256 $got, expected $want" >&2
		echo "The source served different bytes. Re-verify the artifact before committing." >&2
		exit 1
	fi
	echo "  ok sha256 $got"
}

echo "==> ext-apps $EXT_APPS_VERSION"
fetch "$BASE/app-with-deps.js" "$ASSETS/ext-apps-${EXT_APPS_VERSION}.js"
verify "$ASSETS/ext-apps-${EXT_APPS_VERSION}.js" "$EXT_APPS_SHA256"

# NOTE: the schema lands in mcp/testdata, not internal/, because mcp/ is copied verbatim
# into forks and must not import internal/.
fetch "$BASE/generated/schema.json" "$SCHEMA_DIR/ext-apps-schema-${EXT_APPS_VERSION}.json"
verify "$SCHEMA_DIR/ext-apps-schema-${EXT_APPS_VERSION}.json" "$SCHEMA_SHA256"

# NOTE: npm publishes lit only as multi-file ESM with bare specifiers, so the single inlinable
# file is produced here; esbuild's version is pinned because it is an input to the digest.
echo "==> lit $LIT_VERSION (esbuild $ESBUILD_VERSION)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
(
	cd "$WORK"
	npm install --no-save --silent --no-audit --no-fund "lit@${LIT_VERSION}" >/dev/null
	echo 'export {LitElement, html, css, nothing, noChange} from "lit";' >entry.js
	npx --yes "esbuild@${ESBUILD_VERSION}" entry.js --bundle --format=esm --minify \
		--target=es2022 --legal-comments=none --outfile=lit-core.js >/dev/null
)
cp "$WORK/lit-core.js" "$ASSETS/lit-${LIT_VERSION}.js"
verify "$ASSETS/lit-${LIT_VERSION}.js" "$LIT_SHA256"

echo "==> done; review the diff and run: go test ./mcp/... ./internal/mcp/ui/..."
