# Glossary

One concept, one name. Every entry points at the file or config key that
defines it. Where the repo carries two names for one thing, this file says
which is canonical.

## Surfaces

A **surface** is one way the outside world reaches the app, or one way the app reaches the
outside world on a tenant's behalf. There are seven. Rules and mount contracts live in
[`surfaces`](docs/surfaces/README.md); this table is the naming authority.

| Term            | Where                                                  | Mount                | What it is                                                                                  |
| --------------- | ------------------------------------------------------ | -------------------- | ------------------------------------------------------------------------------------------- |
| `console`       | `internal/web/handlers/`                               | `/`                  | Browser surface: templ + HTMX, session-cookie auth, i18n, `/static/`.                       |
| `control plane` | `internal/controlplane/`                               | `/api/`              | Connect-RPC, contracts in `api/*/v1/*.proto`. Gated by `api.enabled`.                       |
| `data plane`    | `internal/dataplane/`                                  | `/api/v1/`           | REST for integrators, API-key auth. Gated by `dataplane.enabled`.                           |
| `ingest`        | `internal/ingest/` — seam only, no provider registered | `/hooks/{provider}/` | Inbound third-party pushes, verified by provider signature.                                 |
| `dispatch`      | `internal/webhook/` on `internal/platform/outbox/`     | — (egress)           | Outbound tenant deliveries, durable and retried. See [`webhooks`](docs/webhooks/README.md). |
| `cli`           | `internal/cli/`                                        | — (dual-mode)        | Operator surface. Contract in [`cli`](docs/cli/README.md).                                  |
| `mcp`           | `internal/mcp/` + `mcp/` (root)                        | `/mcp`               | Tools for an MCP host. See [`mcp`](docs/mcp/README.md).                                     |

NOTE: five of the seven speak HTTP and share one listener: console, control plane, data
plane, ingest and mcp. `web.NewServer` owns the outer mux — it mounts `/healthz`, `/readyz`
and `/robots.txt` unprefixed, each machine surface at its reserved prefix, and the console
under `basePath`. Every mount gets its **own** middleware chain via `web.SurfaceChains`; there
is no global chain. `dispatch` is egress and has no mount. The `cli` surface is not behind the
listener at all: it either boots the graph in-process or speaks HTTP to a remote.

NOTE: the dispatch surface sends to **tenants**. `internal/platform/notify` sends to
**operators**. They are never the same code path.

Three names S7 introduces, all detailed in [`mcp`](docs/mcp/README.md):

| Term           | Where                                           | What it is                                                                                                               |
| -------------- | ----------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| tool           | `option (mcp.v1.tool)` on an RPC in `api/*/v1/` | A control-plane RPC republished for an MCP host. A tool and its RPC are one verb reached two ways — never two impls.     |
| `mcp.audience` | derived: `baseURL` + `basePath` + `/mcp`        | The RFC 8707 resource id the token verifier pins. `mcp.audienceOverride` accepts one that differs from the mount.        |
| Apps UI        | `mcp.appsUI`, `internal/mcp/ui/`                | Optional: publishes the UI resource and binds `_meta.ui`. Assets are digest-pinned; re-vendor with `make mcp-ui-vendor`. |

## Architecture

| Term                | Where                                                      | What it is                                                                                                                                                          |
| ------------------- | ---------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| domain module       | `internal/<name>/`                                         | A bounded context with business logic. Shape fixed by [`modules`](docs/modules/README.md); reference impl `internal/todo/`.                                         |
| platform package    | see note                                                   | A cross-cutting primitive. Shape fixed by [`platform`](docs/platform/README.md).                                                                                    |
| aggregate           | `internal/<name>/<name>.go`                                | The root type plus `New(...)` enforcing creation invariants. Mutations are methods on it. No JSON tags.                                                             |
| `Store`             | `internal/<name>/store.go`                                 | The driven port — persistence interface the domain declares and adapters implement. Verbs only: `Save`, `ByID`, `List`, `Delete`.                                   |
| `Service`           | `internal/<name>/service.go`                               | The driving port — application methods the surfaces call. Holds a `Store`, never SQL.                                                                               |
| workflow            | e.g. `internal/user/onboard.go`                            | A stateful multi-step operation spanning more than one `Store` or an external system (`OnboardWorkflow`, `invite.SendWorkflow`).                                    |
| `Kernel`            | `internal/platform/platform.go:29`                         | The platform bag handed to every service: Pool, PgConn, Log, Reporter, Sessions, Sealer, Verifier, Mail, AltAuth, Tracer, Meter, Notify, Nano, Caps, Outbox, Queue. |
| composition root    | `internal/boot/`                                           | The only place that knows the whole graph. `BootServer` wires Kernel + services + jobs + handlers onto one `worker.Supervisor`.                                     |
| `Capabilities`      | `internal/platform/capabilities/`                          | Config-derived feature flags handed to templates so views never read config directly.                                                                               |
| awareness tag       | `awareness:"..."` on every `Config` field                  | Declares a field's operational role — `required`, `bootstrap`, `secret`, `mode:<x>`, or `-`. Drives `.env.example` generation and mode validation.                  |
| precondition        | `ifVersion int`, last param of a mutating `Service` method | Optimistic concurrency. `0` writes unconditionally; a non-zero value must match the stored row version or the write is refused.                                     |
| `StaleVersionError` | `internal/blog/errors.go:145`                              | The refusal a non-zero `ifVersion` returns when the row moved underneath the caller. Carries `Want` (asked for) and `Got` (stored).                                 |

NOTE: "platform package" is a **category, not a directory**. Some live under
`internal/platform/<name>/` (`authn`, `capabilities`, `config`, `db`, `events`, `notify`,
`outbox`, `queue`, `sealer`, `session`, `surfaces`, `tenant`, `tokens`); others are exported
roots (`worker/`, `scheduler/`, `logger/`, `telemetry/`, `mailer/`, `nanoid/`,
`reqid/`, `httpclient/`, `mcp/`).

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

## Authorization

Tenant scope answers _whose rows_. A scope answers _which verbs_. They are independent
checks and both run.

| Term                  | Where                                      | What it is                                                                                                                                                                                                                                                       |
| --------------------- | ------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| scope                 | `internal/platform/authn/scope.go`         | A permission string minted into an API key or JWT: `sheets:read`, `sheets:write`, `sheets:admin`, `spreadsheets:read`, `spreadsheets:write`, `credentials:read`, `credentials:write`, `cache:purge`, `apikeys:read`, `members:read`; `apikeys:write` is retired. |
| scope catalog         | `authn.MintableScopes()` / `authn.Valid()` | The closed set minting is gated on. A **wire contract** — additive only; a retired scope still validates but is never minted.                                                                                                                                    |
| scope level           | `authn.LevelOf()`                          | `project` (acts on data inside the projects a key reaches) or `org` (acts on the org itself; refused on a project key).                                                                                                                                          |
| project key           | `apikey.KindProject`                       | An API key bound to one project for life.                                                                                                                                                                                                                        |
| org key               | `apikey.KindOrg`                           | An API key reaching its grant (`apikey.ProjectGrant`: all projects, or named ones in `api_key_projects`). Created by an owner or admin; the grant only widens.                                                                                                   |
| personal access token | `apikey.KindPersonal`                      | A key a member mints for themselves in one org, under Settings. Never exceeds its owner; dies when the owner leaves.                                                                                                                                             |
| reach                 | `session.Principal.Reaches*`               | The one rule every surface asks: may this caller act inside this org, project, resource.                                                                                                                                                                         |
| `ScopeTable`          | `internal/controlplane/scopes.go:12`       | Maps each RPC procedure to the scope it requires. Read by both the control plane interceptor and the MCP surface.                                                                                                                                                |
| `Principal`           | `internal/platform/session/session.go:21`  | The authenticated caller. A key principal keeps `UserID == uuid.Nil`, so it is never mistaken for a signed-in human.                                                                                                                                             |
| `authn.Chain`         | `internal/platform/authn/authn.go:48`      | Tries each `Authenticator` in order and returns the first `Principal`; otherwise `UnauthorizedError`.                                                                                                                                                            |

NOTE: scopes do **not** imply one another — the check is `slices.Contains`. A key needing
read plus delete holds both strings. Full catalog and per-surface enforcement:
[`scopes`](docs/scopes/README.md).

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

## Webhooks

Receiver contract: [`webhooks`](docs/webhooks/README.md).

| Term           | Where                                      | What it is                                                                                                                       |
| -------------- | ------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------- |
| event          | `events.Type` (`internal/platform/events`) | Always a **webhook** event sent to tenants, e.g. `blog.post.published`. Never a queue job or broadcast; they never share a type. |
| catalog        | `internal/platform/events/catalog.go`      | The hardcoded list of event types, payload versions and which are subscribable. A public contract; additive within a version.    |
| endpoint       | `webhook.Endpoint`, `webhook_endpoints`    | A tenant's HTTPS URL on one project, subscribed to a set of event types. At most 10 per project.                                 |
| delivery       | `outbox.Entry`, `webhook.Delivery`         | One event bound for one endpoint: `dlv_<outbox entry id>`. The unit receivers dedupe on and the console retries.                 |
| attempt        | `webhook.Attempt`, `webhook_deliveries`    | One POST of a delivery, with status code, error and duration. Up to `outbox.MaxAttempts` (8) per delivery.                       |
| signing secret | `whsec_…`, sealed in `secret_primary`      | The HMAC key for `X-Opensheet-Signature`. Shown once; a rotation keeps the old one as secondary until retired.                   |

NOTE: the table `webhook_deliveries` holds **attempts**, one row per POST. "Delivery" always
means the outbox row.

## Queue

Contract: [`queue`](docs/queue/README.md). Internal work only; nothing here is seen by tenants.

| Term        | Where                                           | What it is                                                                                                   |
| ----------- | ----------------------------------------------- | ------------------------------------------------------------------------------------------------------------ |
| job         | `queue.Job`, declared in `<domain>/consumer.go` | Internal work that must happen once, e.g. `todo.log_completion`. Not a `scheduler.Job`, not an event.        |
| broadcast   | `queue.Broadcast`                               | A notice every running instance reacts to by refreshing in-memory state, e.g. `system.onboarding_completed`. |
| `Submit`    | `(*queue.Client).Submit`                        | Publishes a job to `WORK`, after the commit. One instance runs it, with retries and a dead letter.           |
| `Emit`      | `(*queue.Client).Emit`                          | Publishes a broadcast to `BROADCAST`, after the commit. Every instance hears it once; no retry.              |
| consumer    | `queue.Consumer`, worker `queue.consumer`       | Runs each job's durable pull consumer and its `queue.Handler`. Off under `serve --no-consumer`.              |
| listener    | `queue.Listener`, worker `queue.listener`       | Binds a broadcast to the code that refreshes this instance's state. Runs on every instance.                  |
| dead letter | `DLQ` stream, `queue.DeadLetter`                | A job that failed permanently or ran out of attempts (5), moved to `dlq.<subject>` and reported.             |
| subject     | `Job.Subject()`, `Broadcast.Subject()`          | The NATS address: `jobs.<name>.v<version>` or `broadcast.<name>.v<version>`.                                 |

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
| module path | `altalune.id/opensheet`                                                                                                                                                              |
| binary      | `opensheet`                                                                                                                                                                          |
| fork        | Downstream services fork this repo and swap the domain modules. Signatures under the exported roots and `internal/platform/` are copied verbatim, so changing them costs every fork. |

## Business terms

opensheet publishes a Google Sheets tab as a JSON API. A project connects a **credential**,
registers a **spreadsheet**, publishes a **sheet**, and mints **API keys** for it.

### Registry

| Term          | Where                                           | What it is                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| ------------- | ----------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `credential`  | `internal/credential/`                          | One Google account a project can read as. Holds a name, `Kind` (`service_account` / `google_oauth`), `Status` (`active` / `reauth_needed`), the Google account email, and `Sealed` — **ciphertext only**, AES-256-GCM bound by `SealAAD(org, project, credential)`. It is not a user, holds no opensheet permissions, and never exposes the plaintext: `TokenSourceFor(ctx, id, scope)` hands the refresh token straight to `oauth2`. `scope` binds a `service_account` key; a `google_oauth` refresh grant ignores it and returns the consented set. |
| `spreadsheet` | `internal/spreadsheet/`                         | One **Google file**, registered in a project by its Drive file id and bound to one `credential`. Registering publishes nothing.                                                                                                                                                                                                                                                                                                                                                                                                                       |
| `sheet`       | `internal/sheet/`                               | One **published tab**: a `spreadsheet`, a `Tab`, and a `Slug`. This — not the Google file — is what the data plane serves. Never say "sheet" for the Google file; that is a `spreadsheet`.                                                                                                                                                                                                                                                                                                                                                            |
| `tab`         | `Sheet.Tab` (`internal/sheet/sheet.go:39`)      | The tab's **name inside the Google file**, a plain string, never an aggregate. Empty means "the spreadsheet's first tab", resolved at read time. `spreadsheet.Service.ListTabs` asks Google for them.                                                                                                                                                                                                                                                                                                                                                 |
| `slug`        | `Sheet.Slug`                                    | The public name a sheet is published under: `^[a-z0-9][a-z0-9-]{0,63}$`, unique on `(project_id, slug)`. `reservedSlug` rejects names that would collide with a fixed data-plane path segment.                                                                                                                                                                                                                                                                                                                                                        |
| `visibility`  | `Sheet.Visibility`                              | `key` — a caller must present an API key; or `public` — no credential, and additionally gated by `sheets.publicEnabled` on every read, not only at publish.                                                                                                                                                                                                                                                                                                                                                                                           |
| `snapshot`    | `internal/sheet/snapshot.go`                    | One cached serialization of a tab's rows, keyed by `SnapshotKey{SheetID, Tab}` with the **resolved** tab name. Carries the payload bytes, the `ETag` over exactly those bytes, `FetchedAt` and `ExpiresAt`. Driver from `cache.driver`: `postgres` (`sheet_snapshots`) or `memory`. An expired snapshot still reports as found, so a read can serve it stale when Google is unreachable.                                                                                                                                                              |
| `projection`  | `internal/sheet/rows.go` (`sheet_rows`)         | The **queryable** form of the same rows, one database row per spreadsheet row, keyed by `SnapshotKey` plus the row's `id`. Always in the database on both drivers — never in memory, unlike a `snapshot`. `RowIndex` is the row's position among the tab's data rows, so an interior blank row consumes an index; the spreadsheet row is `RowIndex + 2`. `DeletedAt` is the tombstone, and `deleted_at` is stripped from `Data`.                                                                                                                      |
| `contract`    | `Sheet.ContractOK`, `internal/sheet/publish.go` | The **table contract**: the tab has exactly one `id` header, at most one `deleted_at` header, and no duplicate or empty ids. Checked once at publish, which fails closed, and re-checked on every refresh — a tab that breaks it later is _drift_, recorded in `ContractOK` / `ContractReason` and served stale rather than as an error.                                                                                                                                                                                                              |
| `generation`  | `Sheet.Generation`                              | The per-sheet counter that is both the projection's staleness guard and its write lock. A refresh captures it before fetching and its write is discarded if it moved; a `PATCH` holds it as a lock across the Google write. Never in `MutableColumns` — an upsert must not reset it.                                                                                                                                                                                                                                                                  |

### API keys and scopes

| Term           | Where                                         | What it is                                                                                                                                                                                                                                                                                                       |
| -------------- | --------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `osk_`         | `api.keyPrefix`                               | opensheet's API key prefix: the template's key scheme, configured.                                                                                                                                                                                                                                               |
| resource grant | `APIKey.ResourceIDs`                          | In opensheet a resource is a **sheet**. A non-empty grant limits the key to those sheets; an empty one means every sheet the key reaches. A sheet-restricted key fails `AuthorizeProject`, so the tabs routes refuse it.                                                                                         |
| scope catalog  | `internal/platform/authn/scope.go`            | opensheet's domain scopes `sheets:read`, `sheets:write`, `sheets:admin`, `spreadsheets:read`, `spreadsheets:write`, `credentials:read`, `credentials:write` and `cache:purge`, in place of the template's `posts:*` examples. Wire contract with the scopes registered on opensheet's resource servers in Othra. |
| OAuth scope    | `gworkspace.Scopes()`, `gsheet.ScopeReadOnly` | A Google permission URL consented to on Google's screen and pinned on the authorize URL. Unrelated to API key scopes.                                                                                                                                                                                            |

### Google integration

| Term           | Where                         | What it is                                                                                                                                                                                                                                                                     |
| -------------- | ----------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `gworkspace`   | `gworkspace/` (exported root) | The shared client layer over `google.golang.org/api`: one transport, one `Connector` for the OAuth authorization-code flow, and the provider error types. See [`gworkspace/README.md`](gworkspace/README.md).                                                                  |
| `gsheet`       | `gworkspace/gsheet/`          | The Sheets client — `Rows`, `Tabs`, `FirstTab`, `Title`. `Row` is `map[string]string` keyed by the normalized header.                                                                                                                                                          |
| provider error | `gworkspace/errors.go`        | One of six typed errors every Google failure becomes: `NotFoundError`, `PermissionDeniedError`, `QuotaExceededError`, `UnavailableError`, `AuthExpiredError`, plus `gsheet.TabNotFoundError`. `gworkspace.Translate` does the mapping.                                         |
| `gwerr`        | `internal/gwerr/`             | The adapter that attaches this app's `apperror` envelope to a provider error. A root package cannot import `internal/apperror`, so every consumer of a provider error wraps it with `gwerr.AppError` or the failure is reported as an incident instead of an expected outcome. |
| `Grant`        | `gworkspace.Grant`            | What the token endpoint returned: refresh token, access token, expiry, the `id_token` email claim, and the scopes actually consented to. An empty `RefreshToken` or `AccountEmail` is not an error here — the app decides.                                                     |
