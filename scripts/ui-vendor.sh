#!/usr/bin/env bash
# ui-vendor.sh — download pinned static assets into
# internal/web/static/ so `ALT_UI_MODE=vendored` deployments have no
# runtime dependency on external CDNs.
#
# Pinned versions (bump here + re-run):
#   htmx        v2.0.4  (Aug 2025)
#   basecoat    latest tagged release
#   tailwind    v3 CLI standalone binary
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
STATIC="$ROOT/internal/web/static"
mkdir -p "$STATIC"

HTMX_VERSION="${HTMX_VERSION:-2.0.4}"
BASECOAT_VERSION="${BASECOAT_VERSION:-latest}"
TAILWIND_VERSION="${TAILWIND_VERSION:-v3.4.15}"

fetch() {
	local url="$1" out="$2"
	echo "  → $url"
	curl -sSfL "$url" -o "$out"
}

echo "==> htmx $HTMX_VERSION"
fetch "https://unpkg.com/htmx.org@${HTMX_VERSION}/dist/htmx.min.js" "$STATIC/htmx.min.js"

echo "==> basecoat ${BASECOAT_VERSION}"
fetch "https://cdn.jsdelivr.net/npm/basecoat-css@${BASECOAT_VERSION}/dist/basecoat.min.css" "$STATIC/basecoat.css"

echo "==> tailwindcss CLI $TAILWIND_VERSION"
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
	arm64|aarch64) ARCH="arm64" ;;
	x86_64|amd64) ARCH="x64" ;;
esac
case "$OS" in
	darwin) OSNAME="macos" ;;
	linux) OSNAME="linux" ;;
	*) echo "unsupported OS: $OS" >&2; exit 1 ;;
esac
TW_BIN="$ROOT/bin/tailwindcss"
mkdir -p "$ROOT/bin"
fetch "https://github.com/tailwindlabs/tailwindcss/releases/download/${TAILWIND_VERSION}/tailwindcss-${OSNAME}-${ARCH}" "$TW_BIN"
chmod +x "$TW_BIN"

# Minimal source with @tailwind directives — content sniffing scans .templ files.
SRC="$STATIC/app.tailwind.css"
if [ ! -f "$SRC" ]; then
	cat > "$SRC" <<'EOF'
@tailwind base;
@tailwind components;
@tailwind utilities;
EOF
fi

# Build app.css scanning all templates for used class names.
"$TW_BIN" \
	-i "$SRC" \
	-o "$STATIC/app.css" \
	--content "$ROOT/internal/web/templates/*.templ" \
	--minify

echo "==> done — vendored assets in $STATIC"
