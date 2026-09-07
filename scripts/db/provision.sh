#!/usr/bin/env bash
# Provision an application database + role graph on any Postgres provider.
#
# Env:
#   APP         Role prefix (e.g. auth, billing). Roles created: <APP>_owner,
#               _migrator, _service, _editor, _reader, _ops.
#   ADMIN_URL   Provider admin URL WITHOUT /<db> suffix.
#   DB_NAME     Application database name (e.g. authalunedb).
#   ADMIN_DB    Provider default DB used for CREATE DATABASE
#               (auto: neondb if host contains neon.tech, else postgres).
#   MIG_PW      <APP>_migrator password (>=16 chars).
#   SVC_PW      <APP>_service password  (>=16 chars).
#   PSQL        psql binary (default: psql on PATH).
#
# Interactive when any of APP/ADMIN_URL/DB_NAME/MIG_PW/SVC_PW is unset.

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PSQL="${PSQL:-psql}"

is_tty() { [[ -t 0 && -t 2 ]]; }

# Insert /<db> into a postgresql:// URL between the host and the query string.
#   with_db "postgresql://host?sslmode=require" "authdb"
#     → "postgresql://host/authdb?sslmode=require"
with_db() {
  local url="$1" db="$2" base qs
  if [[ "$url" == *"?"* ]]; then
    base="${url%%\?*}"
    qs="${url#*\?}"
    base="${base%/}"
    printf '%s/%s?%s' "$base" "$db" "$qs"
  else
    printf '%s/%s' "${url%/}" "$db"
  fi
}

mask() {
  local v="${1-}"
  if [[ -z "$v" ]]; then printf '(unset)'; return; fi
  local n=${#v}
  if (( n <= 8 )); then printf '****'; else printf '%s…%s' "${v:0:3}" "${v: -2}"; fi
}

ask_line() {
  local label="$1" default="${2-}" varname="$3" val
  if [[ -n "$default" ]]; then
    read -e -r -p "$label [$default]: " val || true
    val="${val:-$default}"
  else
    while true; do
      read -e -r -p "$label: " val || true
      [[ -n "$val" ]] && break
      echo "  required — please enter a value" >&2
    done
  fi
  printf -v "$varname" '%s' "$val"
}

ask_secret() {
  local label="$1" varname="$2" gen_flag_varname="${3-}" val gen=0
  while true; do
    printf '%s (type "gen" to auto-generate, ≥16 chars): ' "$label" >&2
    IFS= read -s -r val || true
    printf '\n' >&2
    if [[ "$val" == "gen" ]]; then
      val="$(openssl rand -base64 32 | tr -d '=/+' | head -c 40)"
      gen=1
      echo "  → generated ($(mask "$val")); shown in full at the end" >&2
    elif [[ ${#val} -lt 16 ]]; then
      echo "  password must be ≥16 chars (got ${#val}); try again" >&2
      continue
    fi
    printf -v "$varname" '%s' "$val"
    [[ -n "$gen_flag_varname" ]] && printf -v "$gen_flag_varname" '%s' "$gen"
    return
  done
}

APP="${APP-}"
ADMIN_URL="${ADMIN_URL-}"
DB_NAME="${DB_NAME-}"
ADMIN_DB="${ADMIN_DB-}"
MIG_PW="${MIG_PW-}"
SVC_PW="${SVC_PW-}"
MIG_PW_GEN=0
SVC_PW_GEN=0

interactive=false
if [[ -z "$APP" || -z "$ADMIN_URL" || -z "$DB_NAME" || -z "$MIG_PW" || -z "$SVC_PW" ]]; then
  if is_tty; then
    interactive=true
    echo
    echo "DB provisioner"
    echo "--------------"
  else
    echo "Missing required env vars and stdin is not a TTY." >&2
    echo "Set APP, ADMIN_URL, DB_NAME, MIG_PW, SVC_PW — or run interactively." >&2
    exit 1
  fi
fi

if [[ -z "$APP" ]]; then
  ask_line "APP (role prefix, e.g. auth / billing / notif)" "" APP
fi

if [[ ! "$APP" =~ ^[a-z][a-z0-9_]{0,20}$ ]]; then
  echo "APP must match [a-z][a-z0-9_]{0,20} (got: $APP)" >&2; exit 1
fi

if [[ -z "$ADMIN_URL" ]]; then
  ask_line "ADMIN_URL (postgresql://<admin>:<pw>@<host>?sslmode=require, NO /<db>)" "" ADMIN_URL
fi

if [[ -z "$DB_NAME" ]]; then
  ask_line "DB_NAME" "${APP}db" DB_NAME
fi

if [[ -z "$ADMIN_DB" ]]; then
  admin_db_default=postgres
  [[ "$ADMIN_URL" == *neon.tech* ]] && admin_db_default=neondb
  if $interactive; then
    ask_line "ADMIN_DB (used only for CREATE DATABASE)" "$admin_db_default" ADMIN_DB
  else
    ADMIN_DB="$admin_db_default"
  fi
fi

if [[ -z "$MIG_PW" ]]; then
  ask_secret "MIG_PW (${APP}_migrator password)" MIG_PW MIG_PW_GEN
fi
if [[ -z "$SVC_PW" ]]; then
  ask_secret "SVC_PW (${APP}_service password)"  SVC_PW SVC_PW_GEN
fi

if [[ ${#MIG_PW} -lt 16 ]]; then
  echo "MIG_PW must be at least 16 characters (got ${#MIG_PW})" >&2; exit 1
fi
if [[ ${#SVC_PW} -lt 16 ]]; then
  echo "SVC_PW must be at least 16 characters (got ${#SVC_PW})" >&2; exit 1
fi
if [[ ! "$DB_NAME" =~ ^[a-zA-Z_][a-zA-Z0-9_]{0,62}$ ]]; then
  echo "DB_NAME must match [a-zA-Z_][a-zA-Z0-9_]{0,62} (got: $DB_NAME)" >&2; exit 1
fi

pooler_hint=""
case "$ADMIN_URL" in
  *-pooler.*neon.tech*)           pooler_hint="Neon pooler (host contains -pooler). Use the host without -pooler." ;;
  *pooler.supabase.com*)          pooler_hint="Supabase pooler (host contains pooler.supabase.com). Use the direct host." ;;
  *.proxy-*.rds.amazonaws.com*)   pooler_hint="AWS RDS Proxy (host contains .proxy-). Use the underlying RDS endpoint." ;;
  *:6432*|*:6432/*)               pooler_hint="port 6432 is the PgBouncer default. Point at the direct Postgres port (usually 5432)." ;;
esac
if [[ -n "$pooler_hint" ]]; then
  echo
  echo "WARNING: ADMIN_URL looks like a connection pooler — $pooler_hint"
  echo "         Bootstrap does DDL + role/ownership changes that transaction"
  echo "         pooling can drop or mis-route. Runtime services can keep the"
  echo "         pooled endpoint separately."
  if $interactive; then
    read -e -r -p "Continue anyway? [y/N] " ans || ans=""
    case "$ans" in y|Y|yes|YES) ;; *) echo "Aborted."; exit 130 ;; esac
  fi
fi

if $interactive; then
  cat <<EOF

Ready to provision:
  APP         $APP
  ADMIN_URL   ${ADMIN_URL%%\?*}   (query string omitted for display)
  ADMIN_DB    $ADMIN_DB
  DB_NAME     $DB_NAME
  MIG_PW      $(mask "$MIG_PW")   → ${APP}_migrator
  SVC_PW      $(mask "$SVC_PW")   → ${APP}_service

Will run:
  1) CREATE DATABASE "$DB_NAME"                (idempotent)
  2) bootstrap.template.sql   (roles, ownership, default privileges, hardening)
  3) ops.template.sql         (${APP}_ops schema + procedures)

EOF
  read -e -r -p "Proceed? [y/N] " confirm || confirm=""
  case "$confirm" in
    y|Y|yes|YES) ;;
    *) echo "Aborted."; exit 130 ;;
  esac
fi

render() { sed "s/@@APP@@/$APP/g" "$1"; }

db_existed=false
if "$PSQL" -tAX "$(with_db "$ADMIN_URL" "$ADMIN_DB")" \
     -c "SELECT 1 FROM pg_database WHERE datname = '$DB_NAME'" 2>/dev/null | grep -q 1; then
  db_existed=true
fi

echo
echo "==> [1/2] ensuring database $DB_NAME exists (via $ADMIN_DB)"
"$PSQL" -v ON_ERROR_STOP=1 "$(with_db "$ADMIN_URL" "$ADMIN_DB")" <<SQL
SELECT 'CREATE DATABASE ' || quote_ident('$DB_NAME')
 WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '$DB_NAME')\gexec
SQL

# Bootstrap + ops share one transaction — either both apply or neither.
# CREATE DATABASE above can't join that tx; we drop it on failure only if
# we created it in this run.
rollback_db() {
  if ! $db_existed; then
    echo
    echo "==> rolling back: dropping $DB_NAME (created this run)"
    "$PSQL" -v ON_ERROR_STOP=1 "$(with_db "$ADMIN_URL" "$ADMIN_DB")" \
      -c "DROP DATABASE IF EXISTS $(printf '%s' "$DB_NAME" | sed 's/"/""/g' | awk '{print "\"" $0 "\""}')" || \
      echo "   (drop failed — remove manually)"
  else
    echo "   (leaving $DB_NAME in place — it existed before this run)"
  fi
}

echo "==> [2/2] applying bootstrap + ops in a single transaction (prefix ${APP}_)"
{
  echo "BEGIN;"
  render "$script_dir/bootstrap.template.sql"
  render "$script_dir/ops.template.sql"
  echo "COMMIT;"
} | "$PSQL" -v ON_ERROR_STOP=1 \
            -v "migrator_password=$MIG_PW" \
            -v "service_password=$SVC_PW" \
            "$(with_db "$ADMIN_URL" "$DB_NAME")" || {
  echo
  echo "==> bootstrap/ops failed — transaction rolled back"
  rollback_db
  exit 1
}

echo
echo "==> done."

if (( MIG_PW_GEN == 1 )) || (( SVC_PW_GEN == 1 )); then
  echo
  echo "GENERATED PASSWORDS — save these to your secrets manager NOW:"
  (( MIG_PW_GEN == 1 )) && echo "  MIG_PW=$MIG_PW"
  (( SVC_PW_GEN == 1 )) && echo "  SVC_PW=$SVC_PW"
fi

cat <<EOF

Next steps:
  1) Run migrations as ${APP}_migrator with SET ROLE ${APP}_owner.
  2) Verify:
       APP=$APP sed "s/@@APP@@/\$APP/g" $script_dir/verify.template.sql | psql "\$ADMIN_URL/$DB_NAME"
  3) Point runtime services at ${APP}_service (pooled endpoint on Neon).
  4) Cross-tenant reads run through SECURITY DEFINER functions owned by
     ${APP}_owner, which holds BYPASSRLS. ${APP}_service needs no bypass of
     its own and there is no separate maintenance DSN.
EOF
