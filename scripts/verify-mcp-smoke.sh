#!/usr/bin/env bash
# Boots `opensheet serve` with the MCP surface (S7) on ephemeral SQLite and asserts its wire shape.
set -uo pipefail

cd "$(dirname -- "$0")/.."

for tool in go curl jq python3; do
    if ! command -v "$tool" >/dev/null 2>&1; then
        echo "verify-mcp-smoke: ${tool} is required but not on PATH" >&2
        exit 1
    fi
done

tmpdir=$(mktemp -d -t opensheet-mcp-smoke.XXXXXX)

BIN="${tmpdir}/opensheet"
if ! go build -o "$BIN" ./cmd/opensheet; then
    echo "go build ./cmd/opensheet failed" >&2
    exit 1
fi

cleanup() {
    for pid in "${SERVE_PID:-}" "${ISSUER_PID:-}"; do
        if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
            kill -TERM "$pid" 2>/dev/null || true
            wait "$pid" 2>/dev/null || true
        fi
    done
    rm -rf "$tmpdir"
}
trap cleanup EXIT

servelog="${tmpdir}/serve.log"

fail() {
    if [ -s "$servelog" ]; then
        echo "--- serve log ---" >&2
        cat "$servelog" >&2
        echo "--- end serve log ---" >&2
    fi
    if [ -n "${fail_context:-}" ]; then
        echo "--- offending payload ---" >&2
        printf "%s\n" "$fail_context" >&2
    fi
    echo "verify-mcp-smoke: $*" >&2
    exit 1
}

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

ISSUER_PORT=$(pick_port) || fail "no free port for the stub issuer"
APP_PORT=$(pick_port) || fail "no free port for opensheet"
[ "$ISSUER_PORT" != "$APP_PORT" ] || fail "stub issuer and opensheet drew the same port"

ISSUER="http://127.0.0.1:${ISSUER_PORT}"
BASE="http://127.0.0.1:${APP_PORT}"
AUDIENCE="${BASE}/mcp"

# mcp.enabled requires tokens.issuer; discovery is lazy and API keys carry the calls.
mkdir -p "${tmpdir}/issuer/.well-known"
cat >"${tmpdir}/issuer/.well-known/openid-configuration" <<JSON
{"issuer":"${ISSUER}",
 "authorization_endpoint":"${ISSUER}/authorize",
 "token_endpoint":"${ISSUER}/token",
 "jwks_uri":"${ISSUER}/jwks",
 "response_types_supported":["code"],
 "subject_types_supported":["public"],
 "code_challenge_methods_supported":["S256"],
 "id_token_signing_alg_values_supported":["RS256"]}
JSON

python3 -m http.server "$ISSUER_PORT" --bind 127.0.0.1 --directory "${tmpdir}/issuer" \
    >"${tmpdir}/issuer.log" 2>&1 &
ISSUER_PID=$!

issuer_ready=0
for _ in $(seq 1 60); do
    if curl -sf -o /dev/null "${ISSUER}/.well-known/openid-configuration"; then
        issuer_ready=1
        break
    fi
    sleep 0.25
done
[ "$issuer_ready" -eq 1 ] || fail "stub issuer did not come up on ${ISSUER}"

export OPENSHEET_DB_DRIVER=sqlite
export OPENSHEET_DB_DSN="${tmpdir}/opensheet.db"
export OPENSHEET_DB_AUTO_MIGRATE=true
export OPENSHEET_HTTP_ADDR="127.0.0.1:${APP_PORT}"
export OPENSHEET_HTTP_BASE_URL="$BASE"
export OPENSHEET_SESSION_PATH="${tmpdir}/session.json"
export OPENSHEET_MAIL_DRIVER=console
export OPENSHEET_GENESIS_EMAIL="admin@opensheet.local"
export OPENSHEET_GENESIS_PASSWORD="mcp-smoke-genesis-pw"
export OPENSHEET_SECURITY_ENCRYPTION_KEY="0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
export OPENSHEET_API_KEY_PREFIX="key_"
export OPENSHEET_TOKENS_ISSUER="$ISSUER"
export OPENSHEET_MCP_ENABLED=true
export OPENSHEET_MCP_APPS_UI=true

if ! "$BIN" init --email admin@opensheet.local --org-slug smoke-org --org-name "Smoke Org" \
    >"${tmpdir}/init.log" 2>&1; then
    cat "${tmpdir}/init.log" >&2
    fail "opensheet init failed"
fi

"$BIN" serve >"$servelog" 2>&1 &
SERVE_PID=$!

ready=0
for _ in $(seq 1 60); do
    if ! kill -0 "$SERVE_PID" 2>/dev/null; then
        fail "server exited before becoming ready"
    fi
    if curl -sf -o /dev/null "${BASE}/healthz" 2>/dev/null; then
        ready=1
        break
    fi
    sleep 0.25
done
[ "$ready" -eq 1 ] || fail "server did not become ready on ${BASE}"

jar="${tmpdir}/cookies"

console_post() {
    local path out code
    path="$1"
    out="$2"
    shift 2
    code=$(curl -s -b "$jar" -c "$jar" -o "$out" -w "%{http_code}" "$@" "${BASE}${path}")
    printf "%s" "$code"
}

code=$(console_post /login "${tmpdir}/login.html" \
    --data-urlencode "email=${OPENSHEET_GENESIS_EMAIL}" --data-urlencode "password=${OPENSHEET_GENESIS_PASSWORD}")
[ "$code" = "303" ] || fail "console login returned ${code}, want 303"

code=$(console_post /orgs/smoke-org/projects "${tmpdir}/project.html" \
    --data-urlencode "slug=smoke-proj" --data-urlencode "name=Smoke Project")
[ "$code" = "303" ] || fail "project create returned ${code}, want 303"

mint_key() {
    local name scopes out code key
    name="$1"
    scopes="$2"
    out="${tmpdir}/mint-${name}.html"
    code=$(console_post /orgs/smoke-org/projects/smoke-proj/apikeys "$out" \
        --data-urlencode "name=${name}" --data-urlencode "scopes=${scopes}" --data-urlencode "expires_in=30")
    [ "$code" = "200" ] || fail "minting API key ${name} returned ${code}, want 200"
    key=$(grep -oE 'select-all[^>]*>key_[A-Za-z0-9_-]{20,}' "$out" | grep -oE 'key_[A-Za-z0-9_-]{20,}' | head -1)
    [ -n "$key" ] || fail "minting API key ${name} revealed no plaintext"
    printf "%s" "$key"
}

READ_KEY=$(mint_key "mcp-reader" "sheets:read")
NOSCOPE_KEY=$(mint_key "mcp-keys-only" "apikeys:read")

rpc() {
    local out cred body code args
    out="$1"
    cred="$2"
    body="$3"
    args=(-s -o "$out" -w "%{http_code}" -X POST
        -H "Content-Type: application/json"
        -H "Accept: application/json, text/event-stream"
        --data-binary "$body")
    if [ -n "$cred" ]; then
        args+=(-H "Authorization: Bearer ${cred}")
    fi
    code=$(curl "${args[@]}" "${BASE}/mcp")
    printf "%s" "$code"
}

jqx() {
    local file filter
    file="$1"
    filter="$2"
    jq -er "$filter" "$file" 2>/dev/null
}

require_true() {
    local file filter what
    file="$1"
    filter="$2"
    what="$3"
    if ! jq -e "$filter" "$file" >/dev/null 2>&1; then
        fail_context=$(head -c 4000 "$file")
        fail "$what"
    fi
}

init_out="${tmpdir}/initialize.json"
code=$(rpc "$init_out" "$READ_KEY" \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"verify-mcp-smoke","version":"0"}}}')
[ "$code" = "200" ] || fail "initialize returned HTTP ${code}, want 200"

caps=$(jqx "$init_out" '.result.capabilities | keys | join(",")') \
    || fail "initialize returned no capabilities object"
[ "$caps" = "logging,resources,tools" ] \
    || fail "initialize advertised capabilities [${caps}], want [logging,resources,tools]"
require_true "$init_out" '.result.capabilities.tools.listChanged == true' \
    "initialize did not advertise tools.listChanged"
require_true "$init_out" '.result.capabilities.resources.listChanged == true' \
    "initialize did not advertise resources.listChanged — mcp.appsUI publishes resources"
require_true "$init_out" '.result.protocolVersion | type == "string" and length > 0' \
    "initialize returned no protocolVersion"
require_true "$init_out" '.result.serverInfo.name == "opensheet"' \
    "initialize named a server other than opensheet"

tools_out="${tmpdir}/tools-list.json"
code=$(rpc "$tools_out" "$READ_KEY" '{"jsonrpc":"2.0","id":2,"method":"tools/list"}')
[ "$code" = "200" ] || fail "tools/list returned HTTP ${code}, want 200"

names=$(jqx "$tools_out" '[.result.tools[].name] | sort | join(",")') \
    || fail "tools/list returned no tools array"
[ "$names" = "member_list,project_list" ] \
    || fail "tools/list published [${names}], want [member_list,project_list]"

for tool in member_list project_list; do
    require_true "$tools_out" \
        ".result.tools[] | select(.name == \"${tool}\") | .description | type == \"string\" and length > 0" \
        "tool ${tool} carries no description — a host renders it unlabelled"
    require_true "$tools_out" \
        ".result.tools[] | select(.name == \"${tool}\") | .inputSchema.type == \"object\" and (.inputSchema.properties | type == \"object\")" \
        "tool ${tool} carries no object inputSchema"
done

projects_out="${tmpdir}/project-list.json"
code=$(rpc "$projects_out" "$READ_KEY" \
    '{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"project_list","arguments":{}}}')
[ "$code" = "200" ] || fail "project_list returned HTTP ${code}, want 200"
require_true "$projects_out" '.result.isError != true' \
    "project_list failed — an agent cannot discover a projectId without it"
require_true "$projects_out" '[.result.content[].text] | join(" ") | test("smoke-proj")' \
    "project_list did not name the caller's own project"

res_out="${tmpdir}/resources-list.json"
code=$(rpc "$res_out" "$READ_KEY" '{"jsonrpc":"2.0","id":3,"method":"resources/list"}')
[ "$code" = "200" ] || fail "resources/list returned HTTP ${code}, want 200"
require_true "$res_out" \
    '[.result.resources[] | select(.uri == "ui://opensheet/app" and .mimeType == "text/html;profile=mcp-app")] | length == 1' \
    'resources/list does not publish ui://opensheet/app at text/html;profile=mcp-app'

read_out="${tmpdir}/resources-read.json"
code=$(rpc "$read_out" "$READ_KEY" \
    '{"jsonrpc":"2.0","id":4,"method":"resources/read","params":{"uri":"ui://opensheet/app"}}')
[ "$code" = "200" ] || fail "resources/read returned HTTP ${code}, want 200"
require_true "$read_out" \
    '.result.contents[0].uri == "ui://opensheet/app" and .result.contents[0].mimeType == "text/html;profile=mcp-app"' \
    'resources/read did not return ui://opensheet/app at text/html;profile=mcp-app'

bundle="${tmpdir}/app.html"
jqx "$read_out" '.result.contents[0].text' >"$bundle" || fail "resources/read returned no text body"
bundle_size=$(wc -c <"$bundle" | tr -d ' ')
if [ "$bundle_size" -lt 400000 ]; then
    fail "ui://opensheet/app is ${bundle_size} bytes, want >= 400000 — a vendored bundle is missing"
fi
for global in __extApps __lit; do
    grep -q "globalThis\.${global}=" "$bundle" \
        || fail "ui://opensheet/app does not inline globalThis.${global} — its vendored bundle is missing"
done

# A host renders the bundle in a sandboxed frame with no network, so any external reference is dead.
if grep -oE '(src|href)[[:space:]]*=[[:space:]]*"[^"]*"' "$bundle" >"${tmpdir}/refs.txt"; then
    if [ -s "${tmpdir}/refs.txt" ]; then
        fail_context=$(sort -u "${tmpdir}/refs.txt")
        fail "ui://opensheet/app references external files — an MCP Apps bundle must be self-contained"
    fi
fi
if grep -q '@import' "$bundle"; then
    fail_context=$(grep -n '@import' "$bundle")
    fail "ui://opensheet/app contains an @import — an MCP Apps bundle must be self-contained"
fi

call_body='{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"project_list","arguments":{}}}'

unauth_headers="${tmpdir}/unauth.headers"
unauth_out="${tmpdir}/unauth.json"
code=$(curl -s -D "$unauth_headers" -o "$unauth_out" -w "%{http_code}" -X POST \
    -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" \
    --data-binary "$call_body" "${BASE}/mcp")
[ "$code" = "401" ] || fail "unauthenticated tools/call returned HTTP ${code}, want 401"

challenge=$(tr -d '\r' <"$unauth_headers" | grep -i '^www-authenticate:' | head -1)
[ -n "$challenge" ] || fail "unauthenticated tools/call carried no WWW-Authenticate challenge"
metadata_url=$(printf "%s" "$challenge" | sed -n 's/.*resource_metadata="\([^"]*\)".*/\1/p')
[ -n "$metadata_url" ] \
    || fail "WWW-Authenticate names no resource_metadata: ${challenge}"
case "$metadata_url" in
    "${BASE}/.well-known/oauth-protected-resource/mcp") ;;
    *) fail "WWW-Authenticate points at ${metadata_url}, want ${BASE}/.well-known/oauth-protected-resource/mcp" ;;
esac

noscope_out="${tmpdir}/noscope.json"
code=$(rpc "$noscope_out" "$NOSCOPE_KEY" "$call_body")
[ "$code" = "200" ] || fail "scope-denied tools/call returned HTTP ${code}, want 200"
require_true "$noscope_out" '.result.isError == true' \
    "a credential without sheets:read was allowed to run project_list"
require_true "$noscope_out" '[.result.content[].text] | join(" ") | test("scope")' \
    "the scope denial for project_list does not name a scope"

meta_out="${tmpdir}/metadata.json"
code=$(curl -s -o "$meta_out" -w "%{http_code}" "$metadata_url")
[ "$code" = "200" ] || fail "GET ${metadata_url} returned ${code}, want 200"
resource=$(jqx "$meta_out" '.resource') || fail "${metadata_url} names no resource"
[ "$resource" = "$AUDIENCE" ] \
    || fail "metadata resource is ${resource}, want ${AUDIENCE} — the verifier enforces that audience"
require_true "$meta_out" "[.authorization_servers[]] | index(\"${ISSUER}\") != null" \
    "metadata does not name ${ISSUER} as an authorization server"

api_meta_out="${tmpdir}/api-metadata.json"
code=$(curl -s -o "$api_meta_out" -w "%{http_code}" "${BASE}/.well-known/oauth-protected-resource/api")
[ "$code" = "200" ] || fail "GET the API's RFC 9728 document returned ${code}, want 200"
api_resource=$(jqx "$api_meta_out" '.resource') || fail "the API metadata names no resource"
[ "$api_resource" = "${BASE}/api" ] \
    || fail "API metadata resource is ${api_resource}, want ${BASE}/api — the control plane is its own resource"
require_true "$meta_out" '[.scopes_supported[]] | index("sheets:read") != null and index("members:read") != null' \
    "metadata scopes_supported does not cover the registered tools' scopes"

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
    kill -KILL "$SERVE_PID" 2>/dev/null || true
    fail "server did not exit within 10s of SIGTERM"
fi

wait "$SERVE_PID" 2>/dev/null
rc=$?
SERVE_PID=""

# 1 is the CLI's current context.Canceled mapping; 143 is an uncaught SIGTERM. See verify-serve-smoke.sh.
if [ "$rc" -ne 0 ] && [ "$rc" -ne 1 ] && [ "$rc" -ne 143 ]; then
    fail "server exited with unexpected code ${rc}"
fi
if [ "$elapsed" -gt 10 ]; then
    fail "shutdown took ${elapsed}s (>10s budget)"
fi

mkdir -p .cache
cp "$servelog" .cache/verify-mcp.log 2>/dev/null || true

echo "OK: initialize + 2 tools + ui://opensheet/app (${bundle_size} bytes) + RFC 9728 challenge; shutdown in ${elapsed}s"
exit 0
