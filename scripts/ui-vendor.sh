#!/usr/bin/env bash
# Vendors the pinned static assets into internal/web/static/ and compiles app.css.
# The fetched assets are COMMITTED; their sha256 is pinned here AND in
# internal/web/vendor_test.go — bump both together.
# Flags: --css-only (recompile app.css alone), --force (re-download cached assets),
#        --verify (offline: check on-disk assets against the pins, fetch nothing).
# Pinned versions:
#   htmx        v4.0.0  (Sep 2026)
#   easymde     v2.21.0 (MIT; CodeMirror bundled)
#   basecoat    v1.0.2
#   tailwind    v3 CLI standalone binary (build tool, not embedded, not committed)
set -euo pipefail

CSS_ONLY=0
FORCE=0
VERIFY_ONLY=0
for arg in "$@"; do
	case "$arg" in
		--css-only) CSS_ONLY=1 ;;
		--force) FORCE=1 ;;
		--verify) VERIFY_ONLY=1 ;;
		*) echo "unknown argument: $arg" >&2; exit 2 ;;
	esac
done

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
STATIC="$ROOT/internal/web/static"
mkdir -p "$STATIC"

HTMX_VERSION="${HTMX_VERSION:-4.0.0}"
EASYMDE_VERSION="${EASYMDE_VERSION:-2.21.0}"
BASECOAT_VERSION="${BASECOAT_VERSION:-1.0.2}"
TAILWIND_VERSION="${TAILWIND_VERSION:-v3.4.15}"

VENDORED=(
	"htmx.min.js	e484d9171a9db30a39c8f16e3d709d4137f3211c659f8e6125816635033d593f	https://unpkg.com/htmx.org@${HTMX_VERSION}/dist/htmx.min.js"
	"hx-csp.min.js	279f659b9ec8658e3d47230cc545b0bb4e3efa4c2d780454d54db104a5257b7d	https://unpkg.com/htmx.org@${HTMX_VERSION}/dist/ext/hx-csp.min.js"
	"easymde.min.js	2c06bddfd0c89176db08ccf9d42e2beaa9b4f1a4ff8fb2ec0bf1bed25ce08e05	https://cdn.jsdelivr.net/npm/easymde@${EASYMDE_VERSION}/dist/easymde.min.js"
	"easymde.min.css	6eee36340432776d682e7372ec4a7eb29be4fdb3ffada32c0bfa5e31a5ef34a2	https://cdn.jsdelivr.net/npm/easymde@${EASYMDE_VERSION}/dist/easymde.min.css"
	"basecoat.css	8123677adb9bba43be3298e1543bcc5fc763e8cda3d32dc74c806046a3537ca0	https://cdn.jsdelivr.net/npm/basecoat-css@${BASECOAT_VERSION}/dist/basecoat.cdn.min.css"
)

TW_BIN="$ROOT/bin/tailwindcss"

sha256_of() {
	if command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
		return
	fi
	sha256sum "$1" | cut -d' ' -f1
}

ensure() {
	local url="$1" out="$2" want="$3" got tmp
	if [ "$FORCE" -eq 0 ] && [ -f "$out" ]; then
		got="$(sha256_of "$out")"
		if [ "$got" = "$want" ]; then
			echo "  ok $(basename "$out") cached sha256 $got"
			return
		fi
	fi
	echo "  → $url"
	tmp="$(mktemp "${out}.XXXXXX")"
	if ! curl -sSfL "$url" -o "$tmp"; then
		rm -f "$tmp"
		echo "FATAL: $(basename "$out") download failed from $url" >&2
		exit 1
	fi
	got="$(sha256_of "$tmp")"
	if [ "$got" != "$want" ]; then
		rm -f "$tmp"
		echo "FATAL: $(basename "$out") sha256 $got, expected $want" >&2
		echo "The source served different bytes. Re-verify the artifact before committing." >&2
		exit 1
	fi
	mv "$tmp" "$out"
	echo "  ok $(basename "$out") sha256 $got"
}

verify_committed() {
	local entry name want got rc=0
	for entry in "${VENDORED[@]}"; do
		IFS=$'\t' read -r name want _ <<<"$entry"
		if [ ! -f "$STATIC/$name" ]; then
			echo "FATAL: $STATIC/$name is missing" >&2
			rc=1
			continue
		fi
		got="$(sha256_of "$STATIC/$name")"
		if [ "$got" != "$want" ]; then
			echo "FATAL: $STATIC/$name sha256 $got, expected $want" >&2
			rc=1
			continue
		fi
		echo "  ok $name sha256 $got"
	done
	return "$rc"
}

tailwind_platform() {
	local os arch
	os="$(uname -s | tr '[:upper:]' '[:lower:]')"
	arch="$(uname -m)"
	case "$arch" in
		arm64|aarch64) arch="arm64" ;;
		x86_64|amd64) arch="x64" ;;
	esac
	case "$os" in
		darwin) os="macos" ;;
		linux) os="linux" ;;
		*) echo "unsupported OS: $os" >&2; return 1 ;;
	esac
	printf "%s-%s" "$os" "$arch"
}

# https://github.com/tailwindlabs/tailwindcss/releases/download/v3.4.15/sha256sums.txt
tailwind_sha256() {
	case "$1" in
		macos-arm64) echo "06de33ef89ae7391e32dbaec786668a9670d1d7421006e833f61f3db9608e186" ;;
		macos-x64)   echo "7c7cc2372d8abad3ade85afa1af3480e07de3dbfe66704bdbaff4c49d9f2af7f" ;;
		linux-arm64) echo "5cc1a4d6d1188aa5630ba91af2046b1df0cb2a9c082889c049ed0f6c6080c5b5" ;;
		linux-x64)   echo "244c0ae588397c6812f41fee5277f8ddbfd5aa186e5c71673724a1a0328e2a0e" ;;
		*) return 1 ;;
	esac
}

fetch_vendor() {
	local entry name want url platform tw_want
	for entry in "${VENDORED[@]}"; do
		IFS=$'\t' read -r name want url <<<"$entry"
		echo "==> $name"
		ensure "$url" "$STATIC/$name" "$want"
	done

	platform="$(tailwind_platform)"
	if ! tw_want="$(tailwind_sha256 "$platform")"; then
		echo "FATAL: no pinned tailwindcss $TAILWIND_VERSION digest for $platform" >&2
		echo "Add it from the release's sha256sums.txt before building." >&2
		exit 1
	fi
	echo "==> tailwindcss CLI $TAILWIND_VERSION ($platform)"
	mkdir -p "$ROOT/bin"
	ensure "https://github.com/tailwindlabs/tailwindcss/releases/download/${TAILWIND_VERSION}/tailwindcss-${platform}" "$TW_BIN" "$tw_want"
	chmod +x "$TW_BIN"
}

if [ "$VERIFY_ONLY" -eq 1 ]; then
	echo "==> verifying committed assets in $STATIC"
	verify_committed
	echo "==> ok — every committed asset matches its pin"
	exit 0
fi

if [ "$CSS_ONLY" -eq 0 ] || [ ! -x "$TW_BIN" ]; then
	fetch_vendor
fi

SRC="$STATIC/app.tailwind.css"
if [ ! -f "$SRC" ]; then
	cat > "$SRC" <<'EOF'
@tailwind base;
@tailwind components;
@tailwind utilities;
EOF
fi

"$TW_BIN" \
	-c "$ROOT/internal/web/tailwind.config.js" \
	-i "$SRC" \
	-o "$STATIC/app.css" \
	--content "$ROOT/internal/web/**/*.templ"

echo "==> done — vendored assets in $STATIC"
