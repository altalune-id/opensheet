# Glossary

One concept, one name. Every entry points at the file or config key that
defines it. Where the repo carries two names for one thing, this file says
which is canonical.

## Surfaces

A **surface** is one way the outside world reaches the app. There are four,
and they all live in one process behind one HTTP listener.

| Term   | Where            | What it is                                                                                                                                                                                                                                                                                                                                        |
| ------ | ---------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `web`  | `internal/web/`  | Browser surface: templ-rendered HTML, HTMX partials, session-cookie auth (`webmw.Session`), i18n middleware, `/static/`.                                                                                                                                                                                                                          |
| `api`  | `internal/api/`  | Machine surface: Connect-RPC (gRPC-compatible + JSON), contracts in `api/*/v1/*.proto` generated into `gen/`, `authn.Chain` auth over `Authorization: Bearer` or `X-API-Key` (`internal/platform/authn/interceptor.go` — an API key, else a JWT through `tokens.Verifier`), OpenAPI docs. Gated by `api.enabled`.                                 |
| `data` | `internal/data/` | Data plane: a published sheet's rows read, appended and patched as JSON, its `capabilities` reported, plus a spreadsheet's tabs listed and created; plain HTTP, API-key auth (`internal/platform/authn` + the resource-level `Authorizer`), scope from the path. Mounted at `<basePath>/api/v1/`. Not gated by `api.enabled` — it is the product. |
| `cli`  | `internal/cli/`  | Operator surface: Cobra tree, contract in [`docs/CLI_CONTRACT.md`](docs/CLI_CONTRACT.md). Some commands boot the full server graph locally (`ServerBootFn`); others talk to a remote over the API (`ClientBootFn`).                                                                                                                               |

NOTE: web, api and data are **not** peers. `web.NewServer` is also the outer mux
— it owns `/healthz`, `/readyz` and `/robots.txt` unprefixed, mounts the API at
`<basePath>/api/` and the data plane at `<basePath>/api/v1/`
(`internal/web/server.go:70-79`), and mounts the app itself (including
`/static/`) under `basePath`. ServeMux resolves `/api/v1/` over `/api/` by
specificity. The api and data surfaces are nested inside the web one.

Those four surfaces group into two planes, by what they serve:

| Term              | Where                           | What it is                                                                                                                                                                                                            |
| ----------------- | ------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **control plane** | `internal/web` + `internal/api` | The registry: creating and editing credentials, spreadsheets, sheets and API keys. Two surfaces over the same services — a signed-in human on `web`, a scoped machine caller on `api`.                                |
| **data plane**    | `internal/data`                 | The product: a published sheet's rows read, appended, patched and reported on, its cache dropped, plus a spreadsheet's tabs listed and created. Seven routes, no registry writes, and no dependency on `api.enabled`. |

## Architecture

| Term             | Where                                     | What it is                                                                                                                                           |
| ---------------- | ----------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| domain module    | `internal/<name>/`                        | A bounded context with business logic. Shape fixed by [`docs/MODULE_TEMPLATE.md`](docs/MODULE_TEMPLATE.md); reference impl `internal/todo/`.         |
| platform package | see note                                  | A cross-cutting primitive. Shape fixed by [`docs/PLATFORM_TEMPLATE.md`](docs/PLATFORM_TEMPLATE.md).                                                  |
| aggregate        | `internal/<name>/<name>.go`               | The root type plus `New(...)` enforcing creation invariants. Mutations are methods on it. No JSON tags.                                              |
| `Store`          | `internal/<name>/store.go`                | The driven port — persistence interface the domain declares and adapters implement. Verbs only: `Save`, `ByID`, `List`, `Delete`.                    |
| `Service`        | `internal/<name>/service.go`              | The driving port — application methods the surfaces call. Holds a `Store`, never SQL.                                                                |
| workflow         | e.g. `internal/user/onboard.go`           | A stateful multi-step operation spanning more than one `Store` or an external system (`OnboardWorkflow`, `invite.SendWorkflow`).                     |
| `Kernel`         | `internal/platform/platform.go`           | The platform bag handed to every service: Pool, PgConn, Log, Reporter, Sessions, Verifier, Mail, AltAuth, Tracer, Meter, Notify, Nano, Caps, Sealer. |
| composition root | `internal/boot/`                          | The only place that knows the whole graph. `BootServer` wires Kernel + services + jobs + handlers onto one `worker.Supervisor`.                      |
| `Capabilities`   | `internal/platform/capabilities/`         | Config-derived feature flags handed to templates so views never read config directly.                                                                |
| awareness tag    | `awareness:"..."` on every `Config` field | Declares a field's operational role — `required`, `bootstrap`, `secret`, `mode:<x>`, or `-`. Drives `.env.example` generation and mode validation.   |

NOTE: "platform package" is a **category, not a directory**. Some live under
`internal/platform/<name>/` (`authn`, `capabilities`, `config`, `db`, `notify`,
`sealer`, `session`, `tenant`, `tokens`); others are exported roots (`worker/`,
`scheduler/`, `logger/`, `telemetry/`, `mailer/`, `nanoid/`, `reqid/`, `authl/`,
`httpclient/`).

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

## Scope

Four unrelated things wear this word. Never write the bare noun — say which.

| Phrase            | Where                                                       | What it means                                                                                                                                                                                                |
| ----------------- | ----------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **tenant scope**  | `tenant.Context` (`internal/platform/tenant/context.go:11`) | The OrgID / ProjectID / UserID triple on `context.Context`. See [Tenancy](#tenancy).                                                                                                                         |
| **API key scope** | `internal/platform/authn/scope.go`                          | A permission string from this app's fixed catalog — `sheets:read`, `cache:purge`, … `authn.AllScopes()` is the catalog, `authn.Valid` gates minting, `session.Principal.Scopes` carries what a caller holds. |
| **OAuth scope**   | `gworkspace.Scopes()`, `gsheet.ScopeReadOnly`               | A Google permission URL consented to on Google's screen and pinned on the authorize URL. Unrelated to API key scopes.                                                                                        |
| **job scope**     | `scheduler.Scope` (`scheduler/scheduler.go:24`)             | `ScopeSystem` or `ScopeTenant` — how the Runner fans a tick out. See [Scheduling](#scheduling).                                                                                                              |

NOTE: two unexported request-plumbing types also carry the name —
`projectScope` (`internal/web/handlers/scope.go:15`) bundles the resolved org,
project and principal for a web handler, and `scope`
(`internal/data/scope.go:33`) bundles the org id, project id and sheet a
data-plane path resolved to. Neither is a domain term.

## Registry

The product's own bounded contexts. A project connects a **credential**,
registers a **spreadsheet**, publishes a **sheet**, and mints **API keys**.

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

## API keys

`apikey.Mint` returns the plaintext exactly once. Nothing can recover it later.

| Term        | Where                                     | What it is                                                                                                                                                                                                                                                                                                     |
| ----------- | ----------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| label       | `apikey.Label` = `osk`                    | The fixed first segment of `osk_<prefix>_<secret>`. `authn.KeyPrefix` (`"osk_"`) is what `authn.Looks` shape-tests before any database lookup.                                                                                                                                                                 |
| key prefix  | `APIKey.KeyPrefix`, column `key_prefix`   | The **public half**: 16 hex characters over 8 random bytes. Stored in clear, unique, and the column a lookup keys on. Safe to display; apart from the one-time plaintext at mint, it is the only half any wire message carries.                                                                                |
| secret      | the third segment                         | 43 base64url characters over 32 random bytes. Only its SHA-256 persists, in `APIKey.SecretHash` / column `secret_hash`; `APIKey.Verify` compares in constant time.                                                                                                                                             |
| sheet grant | `APIKey.SheetIDs`, table `api_key_sheets` | The per-sheet restriction. A non-empty grant limits the key to exactly those sheets; **an empty grant means every sheet in the project**. Enforced by `APIKey.Allows` via `Authenticator.Authorize` — a `session.Principal` cannot carry it, so a caller reached through plain `Authenticate` is project-wide. |

## Google integration

| Term           | Where                         | What it is                                                                                                                                                                                                                                                                     |
| -------------- | ----------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `gworkspace`   | `gworkspace/` (exported root) | The shared client layer over `google.golang.org/api`: one transport, one `Connector` for the OAuth authorization-code flow, and the provider error types. See [`gworkspace/README.md`](gworkspace/README.md).                                                                  |
| `gsheet`       | `gworkspace/gsheet/`          | The Sheets client — `Rows`, `Tabs`, `FirstTab`, `Title`. `Row` is `map[string]string` keyed by the normalized header.                                                                                                                                                          |
| provider error | `gworkspace/errors.go`        | One of six typed errors every Google failure becomes: `NotFoundError`, `PermissionDeniedError`, `QuotaExceededError`, `UnavailableError`, `AuthExpiredError`, plus `gsheet.TabNotFoundError`. `gworkspace.Translate` does the mapping.                                         |
| `gwerr`        | `internal/gwerr/`             | The adapter that attaches this app's `apperror` envelope to a provider error. A root package cannot import `internal/apperror`, so every consumer of a provider error wraps it with `gwerr.AppError` or the failure is reported as an incident instead of an expected outcome. |
| `Grant`        | `gworkspace.Grant`            | What the token endpoint returned: refresh token, access token, expiry, the `id_token` email claim, and the scopes actually consented to. An empty `RefreshToken` or `AccountEmail` is not an error here — the app decides.                                                     |

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
| module path | `altalune.id/opensheet`                                                                                                                                                              |
| binary      | `opensheet`                                                                                                                                                                          |
| fork        | Downstream services fork this repo and swap the domain modules. Signatures under the exported roots and `internal/platform/` are copied verbatim, so changing them costs every fork. |

## Business terms

<!-- TODO: nothing in this repo establishes product or company brand names. Fill in. -->
