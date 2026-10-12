#!/usr/bin/env bash
# Boots `opensheet serve` on ephemeral SQLite, curls /healthz, then asserts a clean SIGTERM shutdown.
set -uo pipefail

cd "$(dirname -- "$0")/.."

tmpdir=$(mktemp -d -t opensheet-smoke.XXXXXX)

# NOTE: build fresh -- the committed ./bin/opensheet can be stale relative to the tree.
BIN="${tmpdir}/opensheet"
if ! go build -o "$BIN" ./cmd/opensheet; then
    echo "go build ./cmd/opensheet failed" >&2
    exit 1
fi
cleanup() {
    if [ -n "${SERVE_PID:-}" ] && kill -0 "$SERVE_PID" 2>/dev/null; then
        kill -TERM "$SERVE_PID" 2>/dev/null || true
        wait "$SERVE_PID" 2>/dev/null || true
    fi
    rm -rf "$tmpdir"
}
trap cleanup EXIT

# opensheet only reads a fixed addr, so pick from the ephemeral range with a collision retry.
pick_port() {
    for _ in 1 2 3 4 5; do
        p=$(( (RANDOM % 20000) + 40000 ))
        if ! nc -z 127.0.0.1 "$p" 2>/dev/null; then
            printf "%s" "$p"
            return 0
        fi
    done
    return 1
}

PORT=$(pick_port) || { echo "no free port"; exit 1; }
ADDR="127.0.0.1:${PORT}"

# Fresh SQLite file + isolated session path so we never touch ~/.opensheet.
export OPENSHEET_DB_DRIVER=sqlite
export OPENSHEET_DB_DSN="${tmpdir}/opensheet.db"
export OPENSHEET_DB_AUTO_MIGRATE=true
export OPENSHEET_HTTP_ADDR="$ADDR"
export OPENSHEET_SESSION_PATH="${tmpdir}/session.json"
export OPENSHEET_MAIL_DRIVER=console
export OPENSHEET_GENESIS_EMAIL="admin@opensheet.local"
export OPENSHEET_GENESIS_PASSWORD="change-me"

logfile="${tmpdir}/serve.log"

"$BIN" serve >"$logfile" 2>&1 &
SERVE_PID=$!

ready=0
for _ in $(seq 1 60); do
    if ! kill -0 "$SERVE_PID" 2>/dev/null; then
        echo "server exited before becoming ready; log:" >&2
        cat "$logfile" >&2
        exit 1
    fi
    if curl -sf -o /dev/null "http://${ADDR}/healthz" 2>/dev/null; then
        ready=1
        break
    fi
    sleep 0.25
done

if [ "$ready" -ne 1 ]; then
    echo "server did not become ready on ${ADDR}; log:" >&2
    cat "$logfile" >&2
    exit 1
fi

curl -sf -o /dev/null "http://${ADDR}/healthz" || true

# NOTE: d.Static only builds a URL, so an unvendored build serves a 200 page whose assets all 404.
check_asset() {
    local asset floor result code size
    asset="$1"
    floor="$2"
    result=$(curl -s -o "${tmpdir}/${asset}" -w "%{http_code} %{size_download}" "http://${ADDR}/static/${asset}")
    code="${result%% *}"
    size="${result##* }"
    if [ "$code" != "200" ]; then
        echo "GET /static/${asset} returned ${code}, want 200 -- was \`make ui-vendor\` run before the build?" >&2
        cat "$logfile" >&2
        exit 1
    fi
    case "$size" in
        ''|*[!0-9]*)
            echo "GET /static/${asset} reported a non-numeric size ${size}" >&2
            exit 1
            ;;
    esac
    if [ "$size" -lt "$floor" ]; then
        echo "GET /static/${asset} returned ${size} bytes, want >= ${floor}" >&2
        exit 1
    fi
}

check_asset app.css          5000
check_asset basecoat.css   100000
check_asset htmx.min.js     20000
check_asset hx-csp.min.js     500
check_asset easymde.min.js 300000
check_asset easymde.min.css 10000

# A relative @import resolves against /static/ at render time and 404s silently.
for css in app.css basecoat.css; do
    if grep -q '@import' "${tmpdir}/${css}"; then
        echo "/static/${css} contains an @import -- vendored CSS must be self-contained" >&2
        grep -n '@import' "${tmpdir}/${css}" >&2
        exit 1
    fi
done

sleep 0.5

kill -TERM "$SERVE_PID"
shutdown_start=$(date +%s)
for _ in $(seq 1 40); do
    if ! kill -0 "$SERVE_PID" 2>/dev/null; then
        break
    fi
    sleep 0.25
done
shutdown_end=$(date +%s)
elapsed=$(( shutdown_end - shutdown_start ))

if kill -0 "$SERVE_PID" 2>/dev/null; then
    echo "server did not exit within 10s of SIGTERM" >&2
    kill -KILL "$SERVE_PID" 2>/dev/null || true
    exit 1
fi

wait "$SERVE_PID" 2>/dev/null
rc=$?

# Accepted:
#   0   -- ideal
#   1   -- current CLI maps context.Canceled -> ExitGeneral (see TODO in cli/exit.go)
#   143 -- SIGTERM propagated without signal.NotifyContext catch (not this build)
if [ "$rc" -ne 0 ] && [ "$rc" -ne 1 ] && [ "$rc" -ne 143 ]; then
    echo "server exited with unexpected code ${rc}; log:" >&2
    cat "$logfile" >&2
    exit 1
fi

if [ "$elapsed" -gt 10 ]; then
    echo "shutdown took ${elapsed}s (>10s budget)" >&2
    exit 1
fi

if grep -E '"level":"ERROR"| ERROR ' "$logfile" >/dev/null; then
    echo "a clean shutdown logged at ERROR; log:" >&2
    cat "$logfile" >&2
    exit 1
fi

mkdir -p .cache
cp "$logfile" .cache/verify-serve.log 2>/dev/null || true

echo "OK: healthz + graceful shutdown in ${elapsed}s"
exit 0
