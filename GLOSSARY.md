# Glossary

One concept, one name. Every entry points at the file or config key that
defines it. Where the repo carries two names for one thing, this file says
which is canonical.

## Surfaces

A **surface** is one way the outside world reaches the app. There are three,
and they all live in one process behind one HTTP listener.

| Term  | Where           | What it is                                                                                                                                                                                                                      |
| ----- | --------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `web` | `internal/web/` | Browser surface: templ-rendered HTML, HTMX partials, session-cookie auth (`webmw.Session`), i18n middleware, `/static/`.                                                                                                        |
| `api` | `internal/api/` | Machine surface: Connect-RPC (gRPC-compatible + JSON), contracts in `api/*/v1/*.proto` generated into `gen/`, bearer-token auth (`internal/api/interceptor/auth.go` + `tokens.Verifier`), OpenAPI docs. Gated by `api.enabled`. |
| `cli` | `internal/cli/` | Operator surface: Cobra tree, contract in [`docs/CLI_CONTRACT.md`](docs/CLI_CONTRACT.md). Some commands boot the full server graph locally (`ServerBootFn`); others talk to a remote over the API (`ClientBootFn`).             |

NOTE: web and api are **not** peers. `web.NewServer` is also the outer mux — it
owns `/healthz`, `/readyz` and `/robots.txt` unprefixed, mounts the API at
`<basePath>/api/` (`internal/web/server.go:71-72`), and mounts the app itself
(including `/static/`) under `basePath`. The api surface is nested inside the
web one.

## Architecture

| Term             | Where                                     | What it is                                                                                                                                         |
| ---------------- | ----------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| domain module    | `internal/<name>/`                        | A bounded context with business logic. Shape fixed by [`docs/MODULE_TEMPLATE.md`](docs/MODULE_TEMPLATE.md); reference impl `internal/todo/`.       |
| platform package | see note                                  | A cross-cutting primitive. Shape fixed by [`docs/PLATFORM_TEMPLATE.md`](docs/PLATFORM_TEMPLATE.md).                                                |
| aggregate        | `internal/<name>/<name>.go`               | The root type plus `New(...)` enforcing creation invariants. Mutations are methods on it. No JSON tags.                                            |
| `Store`          | `internal/<name>/store.go`                | The driven port — persistence interface the domain declares and adapters implement. Verbs only: `Save`, `ByID`, `List`, `Delete`.                  |
| `Service`        | `internal/<name>/service.go`              | The driving port — application methods the surfaces call. Holds a `Store`, never SQL.                                                              |
| workflow         | e.g. `internal/user/onboard.go`           | A stateful multi-step operation spanning more than one `Store` or an external system (`OnboardWorkflow`, `invite.SendWorkflow`).                   |
| `Kernel`         | `internal/platform/platform.go`           | The platform bag handed to every service: Pool, PgConn, Log, Reporter, Sessions, Verifier, Mail, AltAuth, Tracer, Meter, Notify, Nano, Caps.       |
| composition root | `internal/boot/`                          | The only place that knows the whole graph. `BootServer` wires Kernel + services + jobs + handlers onto one `worker.Supervisor`.                    |
| `Capabilities`   | `internal/platform/capabilities/`         | Config-derived feature flags handed to templates so views never read config directly.                                                              |
| awareness tag    | `awareness:"..."` on every `Config` field | Declares a field's operational role — `required`, `bootstrap`, `secret`, `mode:<x>`, or `-`. Drives `.env.example` generation and mode validation. |

NOTE: "platform package" is a **category, not a directory**. Some live under
`internal/platform/<name>/` (`config`, `db`, `session`, `tenant`, `tokens`,
`capabilities`, `notify`); others are exported roots (`worker/`, `scheduler/`,
`logger/`, `telemetry/`, `mailer/`, `nanoid/`, `reqid/`, `authl/`).

## Tenancy

| Term                 | Where                                                       | What it is                                                                                                 |
| -------------------- | ----------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| org                  | `internal/org/`                                             | The top tenant. Every tenant-scoped row carries its `org_id`.                                              |
| project              | `internal/project/`                                         | A workspace inside an org. Not itself an RLS boundary.                                                     |
| tenant scope         | `tenant.Context` (`internal/platform/tenant/context.go:11`) | The triple OrgID / ProjectID / UserID carried on `context.Context`.                                        |
| RLS                  | `schema/rls_guard.go`                                       | PostgreSQL row-level security. The app role must be `NOBYPASSRLS`; enforced when `tenant.rlsEnforce=true`. |
| tenant-scoped table  | `schema/tenant_tables_gen.go`                               | A table with an `org_id` column and an RLS policy. Regenerate with `make tenant-tables` after adding one.  |
| `app.current_org_id` | `internal/platform/tenant/pgconn.go:11`                     | The Postgres GUC RLS policies read. Set per transaction via `set_config`.                                  |
| `BeginTenanted`      | `internal/platform/tenant/pgconn.go:22`                     | Opens a transaction with that GUC applied. The only sanctioned way to read tenant data.                    |

Three DB credentials, three jobs:

| Key               | Role                                                     |
| ----------------- | -------------------------------------------------------- |
| `db.dsn`          | The app. Must be `NOBYPASSRLS`.                          |
| `db.migrator.dsn` | Schema changes only; opened at boot, then closed.        |
| `db.reader.dsn`   | Replica reads. Falls back to the writer pool when empty. |

## Identity and lifecycle

Four names, two concepts. The distinction is **once per deployment** versus
**once per user**.

| Term                   | Where                                                                                | What it is                                                                                    |
| ---------------------- | ------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------- |
| genesis                | `genesis.email` / `genesis.password`                                                 | The built-in first admin, created at boot when no users exist.                                |
| break-glass            | `genesis.breakGlass`                                                                 | Forces local password login to stay reachable even when OIDC is configured.                   |
| **instance bootstrap** | `/onboard`, `OnboardHandler` + `OnboardingGate` (`internal/web/handlers/onboard.go`) | Happens **once for the deployment**: first admin, first org, first project.                   |
| **user acceptance**    | `/welcome`, `WelcomeHandler` + `WelcomeGate` (`internal/web/handlers/welcome.go`)    | Happens **per user**: T&C acceptance + display name. Gated by `compliance.requireAcceptance`. |
| signup completion      | `/signup/complete`, `SignupHandler` (`internal/web/handlers/signup.go`)              | Cloud-only. An OIDC user with no pre-existing membership names their org and first project.   |

NOTE: `/onboarding` (`OnboardingHandler`, `internal/web/handlers/onboarding.go`)
duplicates `/welcome`. It is registered in `internal/boot/http.go`, but nothing
gates it, and its `RequireOnboarded` middleware is unused in production while
still exercised by tests (8 references in
`internal/web/handlers/handlers_test.go`) — so it is unreachable, not dead
code. **Canonical name for the per-user flow is `welcome`.** The duplicate is
left in place deliberately — removing it touches auth flows.

## Scheduling

| Term           | Where                       | What it is                                                                                                                    |
| -------------- | --------------------------- | ----------------------------------------------------------------------------------------------------------------------------- |
| `Runner`       | `scheduler/scheduler.go`    | Owns every `Job`, one goroutine per job, and the shutdown drain.                                                              |
| `Job`          | `scheduler/scheduler.go:50` | One unit of periodic work: `Name`, `Scope`, `Schedule`, `Timeout`, `Singleton`, `Run`.                                        |
| `Scope`        | `scheduler/scheduler.go:24` | `ScopeSystem` (`"system"`) runs once per tick; `ScopeTenant` (`"tenant"`) fans out over every tenant with a tenant-bound ctx. |
| `Singleton`    | `Job.Singleton`             | Take the cross-process lock first; skip the tick if another replica holds it.                                                 |
| `Provider`     | `scheduler/provider.go`     | The zero-arg port a domain's `Scheduler` adapter implements to contribute jobs (`SchedulerJobs() []Job`).                     |
| `Tenants`      | `scheduler/provider.go`     | Enumerates tenants for a `ScopeTenant` job. Impl `tenant.Enumerator`, reading through `tenant.OrgReader`.                     |
| `Locker`       | `scheduler/provider.go`     | Serializes a `Singleton` job across processes. Impl `db.PgLocker` on `pg_try_advisory_lock`.                                  |
| `LocationFunc` | `scheduler/timezone.go:6`   | `func(jobName string) *time.Location` — resolves a job's wall-clock zone.                                                     |
| `Status`       | `scheduler/scheduler.go:34` | Run outcome: `success`, `error`, `overlap`, `not_leader`, `panic`.                                                            |
| `Worker`       | `worker/worker.go:7`        | `Name() string` + `Run(ctx) error` — one long-running loop owned for the process lifetime.                                    |
| `Supervisor`   | `worker/supervisor.go:11`   | Runs every registered `Worker` and shuts them all down together.                                                              |

A `Job` is not a `Worker`: a Worker is one loop that lives as long as the
process, a Job is a unit of periodic work the Runner invokes on a schedule.
`*scheduler.Runner` is itself a `worker.Worker` — it satisfies the interface
structurally, with no adapter and no import of `worker`.

## Deployment

| Term       | Where                                            | What it is                                                                                                                                                                                                                         |
| ---------- | ------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| mode       | `mode` (`internal/platform/config/config.go:21`) | `selfhosted` or `cloud`. `Mode.IsProduction()` is derived — it is true only for `cloud`.                                                                                                                                           |
| `basePath` | `http.basePath`                                  | URL path prefix the app is mounted under, e.g. `/app`. Affects routing.                                                                                                                                                            |
| `baseURL`  | `http.baseURL`                                   | Absolute external URL of the deployment. Used for links in mail and the OIDC redirect.                                                                                                                                             |
| `healthz`  | `GET /healthz`                                   | Liveness. DB-independent — always 200 while the process serves.                                                                                                                                                                    |
| `readyz`   | `GET /readyz`                                    | Readiness. Returns 503 when the DB health snapshot (`db.HealthMonitor.Ready`) is unhealthy. Boot probes once so the answer is never unset, and the `db-health` worker refreshes it in every replica, independent of the scheduler. |

## Naming

| Term        | What it is                                                                                                                                                                           |
| ----------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| module path | `altalune.id/template`                                                                                                                                                               |
| binary      | `altempl`                                                                                                                                                                            |
| fork        | Downstream services fork this repo and swap the domain modules. Signatures under the exported roots and `internal/platform/` are copied verbatim, so changing them costs every fork. |

## Business terms

<!-- TODO: nothing in this repo establishes product or company brand names. Fill in. -->
