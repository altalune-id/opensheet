# Deployment

## Docker

Single container (SQLite in the image):

```bash
make docker
docker run --rm -p 5150:5150 \
  -e ALT_DB_DRIVER=sqlite -e ALT_DB_DSN=/data/altempl.db \
  -e ALT_GENESIS_EMAIL=admin@local -e ALT_GENESIS_PASSWORD=change-me \
  -v altempl-data:/data altempl:dev
```

CLI usage inside the image: `docker run --rm altempl:dev version`,
`docker run --rm altempl:dev migrate status` (etc.).

Registry tags:

| Tag                                   | Source                               |
| ------------------------------------- | ------------------------------------ |
| `ghcr.io/<owner>/altempl:edge`        | latest push to `main`                |
| `ghcr.io/<owner>/altempl:<short-sha>` | that push, pinned                    |
| `ghcr.io/<owner>/altempl:<version>`   | tagged release (`v0.1.0` → `:0.1.0`) |
| `ghcr.io/<owner>/altempl:latest`      | most recent tagged release           |

Base images are digest-pinned in `Dockerfile` — refresh with
`docker manifest inspect <ref>` and update the two `ARG` lines.

## Local dev stack (compose)

`compose.yaml` starts Postgres + [Mailpit](https://mailpit.axllent.org/)

- altempl. Works with `docker compose` or `podman-compose`:

```bash
make compose-up          # build + start everything
open http://127.0.0.1:5150/login    # altempl
open http://127.0.0.1:8025          # mailpit — outbound email lands here

make compose-logs
make compose-down        # stop, keep volumes
make compose-nuke        # stop + wipe docker/data/pg
```

Postgres data lives at `./docker/data/pg` (bind-mounted, `.gitignore`d).
The stack runs `selfhosted` with `ALT_DB_ALLOW_BYPASS_RLS=true` (RLS off)
and Mailpit's open SMTP — production settings go under `mail.smtp.*` (or
`mail.resend.*` with `mail.driver=resend`) and the three-role split below.

## Postgres roles

**Dev**: point `ALT_DB_DSN` at any role (superuser is fine) and set
`ALT_DB_ALLOW_BYPASS_RLS=true`. RLS is off.

**Production** — role graph provisioned via `scripts/db/provision.sh`:

- `altempl_owner` (`NOLOGIN`) owns every schema object.
- `altempl_migrator` (`LOGIN`, member of `altempl_owner`) runs migrations under `SET ROLE altempl_owner`, issued once per connection from `db.migrator.role` rather than inside the migration SQL.
- `altempl_service` (`LOGIN`, `NOBYPASSRLS`) is the runtime DSN. DML granted via `ALTER DEFAULT PRIVILEGES`.

Provision idempotently (interactive; prompts for admin URL, DB name, passwords):

```bash
APP=altempl DB_NAME=altempl scripts/db/provision.sh
```

Then set:

```
ALT_DB_DSN=postgres://altempl_service:<svc-pw>@host:5432/altempl?sslmode=require
ALT_DB_MIGRATOR_DSN=postgres://altempl_migrator:<mig-pw>@host:5432/altempl?sslmode=require
ALT_DB_MIGRATOR_ROLE=altempl_owner
ALT_DB_ALLOW_BYPASS_RLS=false
```

`db.migrator.role` is the sole source of the migration role; `db.role` applies only to runtime
connections and never reaches migrations.

Boot fails if the runtime role has `BYPASSRLS` and `db.allowBypassRLS` is `false`.

### Cross-tenant reads

Tenant-scoped scheduler jobs must first ask "which tenants exist?" — a
question no single tenant's scope can answer. `altempl_service` is
`NOBYPASSRLS`, so under `tenant.rlsEnforce=true` a direct read of
`<prefix>orgs` returns zero rows.

`SECURITY DEFINER` wrapper functions answer it instead. Migration 005 creates
them owned by `altempl_owner`, which holds `BYPASSRLS`, with `search_path`
pinned and `EXECUTE` revoked from `PUBLIC`. `altempl_service` reaches them
through the `ALTER DEFAULT PRIVILEGES … GRANT EXECUTE ON FUNCTIONS` grant in
`scripts/db/provision.sh`.

No fourth credential is involved. `BYPASSRLS` is a role attribute, not a
privilege, so granting `altempl_owner` to another role does not confer it —
only executing a function owned by `altempl_owner` does.

Migration 005 refuses to apply if the migration role lacks `BYPASSRLS`, and
names the `ALTER ROLE` that fixes it.

## Reader replica

`db.Pool{W, R}` wraps writer + reader. SQLite always aliases `R` to `W`.
For Postgres, `ALT_DB_READER_DSN` routes non-tenant reads (`users`,
`onboard`) to a replica; empty aliases to `W`. Tenant-scoped reads run
on `W` — `BeginTenanted` requires a tx on the primary for `set_config`.

Unit-of-Work code pattern for module authors:
[`MODULE_TEMPLATE.md`](MODULE_TEMPLATE.md#store-driven-port).

## RLS

Every tenant-scoped Postgres table has `ROW LEVEL SECURITY ENABLED FORCE`
plus a policy keyed on `current_setting('app.current_org_id')::uuid`.
Stores call `tenant.PgConn.BeginTenanted(ctx)` which opens a tx after
`SET LOCAL app.current_org_id = $1`. The `internal/schema` boot guard
fails startup if the connecting role can bypass RLS.

SQLite (dev only) has no RLS; stores filter by `org_id` in the `WHERE`
clause. Both dialects satisfy the same domain interface.

## Health endpoints

`/healthz`, `/readyz`, and `/robots.txt` are mounted at the outer mux
root — NOT under `http.basePath`. This is deliberate:

- Orchestrator probes (compose, k8s kubelet, LB target groups) reach
  altempl on its listen port directly. Basepath-independence keeps their
  config stable when you remount the app.
- Public reverse proxies typically route only `example.com/<basePath>/*`
  to altempl, so `/healthz` stays off the public surface by default.

`/healthz` is liveness: it returns 200 whenever the process is serving, and
never touches the database. Do not point a DB-dependent probe at it.

`/readyz` is readiness: 200 only when every DB handle passed the most
recent probe, 503 otherwise. Boot probes once synchronously before the
listener accepts traffic, so `/readyz` is accurate from the very first
request — no unready window on rollout. The `db-health` worker then
refreshes the snapshot every `db.health.interval`, so readiness lags a DB
outage by up to one interval; lower the interval if you need it tighter.

`db-health` is a standalone `Worker` on the same `Supervisor` as the HTTP
listener, not a scheduler job. It runs in **every** replica regardless of
`scheduler.enabled` or `serve --no-scheduler`, so `/readyz` is DB-aware in
every deployment shape. Each replica probes its own pool; the snapshot is
never shared. A probe failure is logged and the worker keeps ticking — it
never escalates to the notification sinks and never fails the process.

Public status page needed? Add the proxy route explicitly:

```nginx
location = /altempl/healthz { proxy_pass http://altempl:5150/healthz; }
```

`altempl healthz` is a self-contained probe binary (works in distroless,
no `curl` needed) — the compose/k8s healthcheck.

## Scheduler

Jobs run in-process, registered as one `Worker` on the same `Supervisor` as
the HTTP listener. `altempl scheduler list` prints what is registered.

**Multiple replicas.** A job is either singleton or per-replica, and the
choice is per job, not per deployment:

| Job                       | Singleton | Runs on                                                            |
| ------------------------- | --------- | ------------------------------------------------------------------ |
| `todo-autocomplete-stale` | yes       | exactly one replica per tick — the one that wins the advisory lock |

Scale replicas freely. Do not try to designate a "scheduler replica" for
correctness; leader election is per tick, in Postgres.

**Leader election** uses `pg_try_advisory_lock` on the writer handle. No
migration and no lock table — nothing to provision. Under
`driver: sqlite` the locker is a no-op, since there is one writing process.

**Pool sizing caveat.** An advisory lock is session-scoped, so each
in-flight singleton job **pins one writer connection** for its whole run.
If `db.maxOpenConns` is capped at all, it must exceed the number of
concurrent singleton jobs, or a job will block waiting for a connection it
can never get while holding none. `0` (unlimited) is unaffected.

**`--scheduler-only` as a deployment shape.** `altempl serve
--scheduler-only` runs the jobs and nothing else — no web UI, no API. It
still binds `http.addr` and still serves `/healthz` and `/readyz` — the
`db-health` worker runs here too — so the same orchestrator probes and the
same `altempl healthz` healthcheck work unchanged. `--scheduler-only` requires the scheduler: combined with
`scheduler.enabled=false` (or `WithScheduler(false)`) it is rejected at
boot, because the process would serve probes and do no work.

Pick one of two shapes for the request-serving replicas, and know what each
costs:

- **Scheduler in-process everywhere** (the default; no `--no-scheduler`).
  Job load shares the latency path, but singleton jobs still run on exactly
  one replica per tick.
- **`serve --no-scheduler` on the serving replicas**, paired with a
  `--scheduler-only` job replica. This keeps job load off the latency path.

Readiness is not a factor in that choice: `db-health` is a worker, not a
job, so `/readyz` stays DB-aware in both shapes.

## Observability

- **Traces** — `ALT_OBSERVABILITY_OTEL_ENDPOINT` → OTLP collector.
  HTTP, Connect, workers, DB spans all propagate via `context.Context`.
- **Metrics** — Prometheus at `basePath + /metrics`; gate with
  `api.metrics.requireBasicAuth` when the scrape target isn't private.
- **Logs** — slog + `logger.Redact` (patterns in `log.redactPatterns`).
  Every request carries `request_id` + `trace_id`.

Unhandled errors fan out via `apperror.Reporter` to sinks in
`internal/platform/notify` (`slack`, `discord`, `googlechat`, `email`,
`stdout`). Enable under `errorReporter.sinks`.

## API surface

Connect-RPC mounts under `basePath + /api`. `todo.v1.TodoService` and
`auth.v1.AuthService` ship in the scaffold; command tree in
[`CLI_CONTRACT.md`](CLI_CONTRACT.md).

OpenAPI 3.1 is embedded at build time — served at
`basePath + /api/openapi.{yaml,json}`. Gate with
`api.openapi.requireBasicAuth: true` + `basicAuthUser` /
`basicAuthPassword`. Set `api.openapi.enabled: false` to 404 both.

## OIDC (altalune-auth)

1. In altalune-auth's console, create an OAuth client — `confidential`
   for servers, `public` for CLI-only. Copy client ID + secret.
2. Register redirect URIs — `http://<host>:<port>/oauth/callback` (web)
   and `http://127.0.0.1:0/callback` (CLI loopback, RFC 8252).
3. Create a resource server for `urn:altempl:api`.
4. In `config.yaml`: `oidc.issuer`, `oidc.clientID`, `oidc.clientSecret`,
   `oidc.resource: urn:altempl:api`, `tokens.audience: urn:altempl:api`.
5. Restart — log in at `/login`.
