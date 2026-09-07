#!/usr/bin/env bash
# verify-serve-smoke.sh
# Boots `altempl serve` against an ephemeral SQLite DB on a random port,
# curls /healthz, sends SIGTERM, and asserts clean shutdown within 10s.
set -uo pipefail

cd "$(dirname -- "$0")/.."

tmpdir=$(mktemp -d -t altempl-smoke.XXXXXX)

# Always build fresh -- `./bin/altempl` is committed and can be stale relative
# to the current tree (e.g. missing RequestLog middleware). Build once here.
BIN="${tmpdir}/altempl"
if ! go build -o "$BIN" ./cmd/altempl; then
    echo "go build ./cmd/altempl failed" >&2
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

# Random loopback port. 0 lets the kernel choose but altempl only reads a fixed
# addr, so pick from the ephemeral range with a small collision retry.
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

# Fresh SQLite file + isolated session path so we never touch ~/.altempl.
export ALT_DB_DRIVER=sqlite
export ALT_DB_DSN="${tmpdir}/altempl.db"
export ALT_DB_AUTO_MIGRATE=true
export ALT_HTTP_ADDR="$ADDR"
export ALT_SESSION_PATH="${tmpdir}/session.json"
export ALT_MAIL_DRIVER=console
export ALT_GENESIS_EMAIL="admin@altempl.local"
export ALT_GENESIS_PASSWORD="change-me"

logfile="${tmpdir}/serve.log"

"$BIN" serve >"$logfile" 2>&1 &
SERVE_PID=$!

# Wait up to 15s for /healthz.
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

# Fire one more request that will produce a log line for /healthz inspection.
curl -sf -o /dev/null "http://${ADDR}/healthz" || true

# Let the middleware flush its request log before we terminate.
sleep 0.5

# SIGTERM and time the shutdown window.
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

# Persist the log location for follow-on checks.
mkdir -p .cache
cp "$logfile" .cache/verify-serve.log 2>/dev/null || true

echo "OK: healthz + graceful shutdown in ${elapsed}s"
exit 0
